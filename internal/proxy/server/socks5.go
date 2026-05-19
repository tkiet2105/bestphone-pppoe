package server

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socks5Ver        = 0x05
	socks5MethodNone = 0x00
	socks5MethodUP   = 0x02

	socks5CmdConnect  = 0x01
	socks5AtypIPv4    = 0x01
	socks5AtypDomain  = 0x03
	socks5AtypIPv6    = 0x04

	socks5RepOK            = 0x00
	socks5RepGeneralFail   = 0x01
	socks5RepNotAllowed    = 0x02
	socks5RepNetUnreach    = 0x03
	socks5RepHostUnreach   = 0x04
	socks5RepConnRefused   = 0x05
	socks5RepCmdNotSupport = 0x07
)

func (l *listener) handleSocks5(conn net.Conn, firstByte byte) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Đọc nMethods (firstByte đã = 0x05 — caller peek)
	var nMethods [1]byte
	if _, err := io.ReadFull(conn, nMethods[:]); err != nil {
		return
	}
	methods := make([]byte, nMethods[0])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	_ = firstByte

	wantAuth := l.creds.hasAuth()
	selected := byte(0xFF) // no acceptable
	for _, m := range methods {
		if wantAuth && m == socks5MethodUP {
			selected = socks5MethodUP
			break
		}
		if !wantAuth && m == socks5MethodNone {
			selected = socks5MethodNone
			break
		}
	}
	if _, err := conn.Write([]byte{socks5Ver, selected}); err != nil {
		return
	}
	if selected == 0xFF {
		return
	}

	if selected == socks5MethodUP {
		if !l.socks5AuthUP(conn) {
			return
		}
	}

	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return
	}
	if hdr[0] != socks5Ver {
		return
	}
	if hdr[1] != socks5CmdConnect {
		_ = socks5Reply(conn, socks5RepCmdNotSupport)
		return
	}
	var host string
	switch hdr[3] {
	case socks5AtypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case socks5AtypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case socks5AtypDomain:
		var ln [1]byte
		if _, err := io.ReadFull(conn, ln[:]); err != nil {
			return
		}
		buf := make([]byte, ln[0])
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = string(buf)
	default:
		_ = socks5Reply(conn, socks5RepCmdNotSupport)
		return
	}
	var portBuf [2]byte
	if _, err := io.ReadFull(conn, portBuf[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBuf[:])
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// Access control: deny-wins
	if l.rules != nil && !l.rules.allowed(host) {
		_ = socks5Reply(conn, socks5RepNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	upstream, err := dialBound(ctx, l.iface(), "tcp", addr)
	if err != nil {
		_ = socks5Reply(conn, socks5DialErrToRep(err))
		return
	}
	defer upstream.Close()

	if err := socks5Reply(conn, socks5RepOK); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	pipe(conn, upstream)
}

func (l *listener) socks5AuthUP(conn net.Conn) bool {
	// VER(1) ULEN(1) UNAME ULEN PLEN PWD
	var ver [1]byte
	if _, err := io.ReadFull(conn, ver[:]); err != nil {
		return false
	}
	var ul [1]byte
	if _, err := io.ReadFull(conn, ul[:]); err != nil {
		return false
	}
	uname := make([]byte, ul[0])
	if _, err := io.ReadFull(conn, uname); err != nil {
		return false
	}
	var pl [1]byte
	if _, err := io.ReadFull(conn, pl[:]); err != nil {
		return false
	}
	pwd := make([]byte, pl[0])
	if _, err := io.ReadFull(conn, pwd); err != nil {
		return false
	}
	ok := l.creds.match(uname, pwd)
	status := byte(0xFF)
	if ok {
		status = 0x00
	}
	if _, err := conn.Write([]byte{0x01, status}); err != nil {
		return false
	}
	return ok
}

func socks5Reply(conn net.Conn, rep byte) error {
	// VER REP RSV ATYP(IPv4) BND.ADDR(0.0.0.0) BND.PORT(0)
	_, err := conn.Write([]byte{socks5Ver, rep, 0x00, socks5AtypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func socks5DialErrToRep(err error) byte {
	es := err.Error()
	switch {
	case containsAny(es, "no route", "network is unreachable"):
		return socks5RepNetUnreach
	case containsAny(es, "no such host", "host is unreachable"):
		return socks5RepHostUnreach
	case containsAny(es, "connection refused"):
		return socks5RepConnRefused
	default:
		return socks5RepGeneralFail
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

