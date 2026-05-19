package server

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// listener — wrap quanh 1 net.Listener cho 1 proxy. Multiplex SOCKS5 / HTTP CONNECT.
type listener struct {
	proxyID   uint
	port      int
	ifaceFn   func() string // closure để lookup iface mỗi connection (session.Iface có thể đổi sau rotate)
	creds     *credSet
	ln        net.Listener
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func (l *listener) iface() string {
	if l.ifaceFn == nil {
		return ""
	}
	return l.ifaceFn()
}

func (l *listener) acceptLoop() {
	defer l.wg.Done()
	for {
		l.ln.(*net.TCPListener).SetDeadline(time.Now().Add(1 * time.Second))
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.ctx.Done():
				return
			default:
			}
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			log.Printf("[proxy %d] accept: %v", l.proxyID, err)
			return
		}
		go l.handle(conn)
	}
}

func (l *listener) handle(conn net.Conn) {
	// peek 1 byte để phân biệt SOCKS5 (0x05) vs HTTP (ASCII method)
	var b [1]byte
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	n, err := conn.Read(b[:])
	if err != nil || n != 1 {
		conn.Close()
		return
	}
	if b[0] == 0x05 {
		l.handleSocks5(conn, b[0])
		return
	}
	// ASCII A-Z (HTTP methods bắt đầu uppercase)
	if b[0] >= 'A' && b[0] <= 'Z' {
		l.handleHTTP(conn, b[0])
		return
	}
	conn.Close()
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}
