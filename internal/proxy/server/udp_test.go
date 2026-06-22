package server

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// --- header parse/build ---

func TestParseUDPHeader_IPv4(t *testing.T) {
	b := []byte{0, 0, 0, socks5AtypIPv4, 1, 2, 3, 4, 0x00, 0x35, 'h', 'i'}
	host, port, frag, hdrLen, err := parseUDPHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if host != "1.2.3.4" || port != 53 || frag != 0 || hdrLen != 10 {
		t.Fatalf("got host=%s port=%d frag=%d hdrLen=%d", host, port, frag, hdrLen)
	}
	if string(b[hdrLen:]) != "hi" {
		t.Fatalf("payload mismatch: %q", b[hdrLen:])
	}
}

func TestParseUDPHeader_Domain(t *testing.T) {
	host := "example.com"
	b := []byte{0, 0, 0, socks5AtypDomain, byte(len(host))}
	b = append(b, []byte(host)...)
	b = append(b, 0x01, 0xBB) // port 443
	b = append(b, "data"...)
	gotHost, port, _, hdrLen, err := parseUDPHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != host || port != 443 {
		t.Fatalf("got host=%s port=%d", gotHost, port)
	}
	if string(b[hdrLen:]) != "data" {
		t.Fatalf("payload mismatch: %q", b[hdrLen:])
	}
}

func TestParseUDPHeader_Errors(t *testing.T) {
	if _, _, _, _, err := parseUDPHeader([]byte{0, 0}); err == nil {
		t.Fatal("expected short error")
	}
	if _, _, _, _, err := parseUDPHeader([]byte{0, 0, 0, 0x09, 1, 2}); err == nil {
		t.Fatal("expected bad atyp error")
	}
	// IPv4 atyp nhưng thiếu byte addr/port
	if _, _, _, _, err := parseUDPHeader([]byte{0, 0, 0, socks5AtypIPv4, 1, 2}); err == nil {
		t.Fatal("expected short error for truncated ipv4")
	}
}

func TestBuildParseUDPHeader_RoundTrip(t *testing.T) {
	hdr := buildUDPHeader(net.ParseIP("8.8.4.4"), 5353)
	hdr = append(hdr, "payload"...)
	host, port, frag, hdrLen, err := parseUDPHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if host != "8.8.4.4" || port != 5353 || frag != 0 {
		t.Fatalf("round-trip mismatch host=%s port=%d", host, port)
	}
	if string(hdr[hdrLen:]) != "payload" {
		t.Fatalf("payload mismatch: %q", hdr[hdrLen:])
	}
}

func TestBuildUDPHeader_IPv6(t *testing.T) {
	hdr := buildUDPHeader(net.ParseIP("2001:4860:4860::8888"), 53)
	if hdr[3] != socks5AtypIPv6 {
		t.Fatalf("expected IPv6 atyp, got %d", hdr[3])
	}
	host, port, _, _, err := parseUDPHeader(append(hdr, "x"...))
	if err != nil {
		t.Fatal(err)
	}
	if net.ParseIP(host) == nil || !net.ParseIP(host).Equal(net.ParseIP("2001:4860:4860::8888")) || port != 53 {
		t.Fatalf("got host=%s port=%d", host, port)
	}
}

// --- E2E loopback (iface="" → không bind ppp, chạy được không cần quyền) ---

func newTestUDPListener(t *testing.T) *listener {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &listener{
		ifaceFn: func() string { return "" },
		creds:   &credSet{},
		rules:   &ruleSet{},
		ctx:     ctx,
		cancel:  cancel,
	}
}

// startSocksServer — TCP listener gọi l.handle cho mỗi conn. Trả địa chỉ.
func startSocksServer(t *testing.T, l *listener) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go l.handle(c)
		}
	}()
	return ln.Addr().String()
}

// startUDPEcho — UDP echo server trên 127.0.0.1. Trả địa chỉ.
func startUDPEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteToUDP(buf[:n], addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr)
}

// socks5UDPHandshake — no-auth + CMD=UDP ASSOCIATE. Trả control-conn (mở) + BND.
func socks5UDPHandshake(t *testing.T, serverAddr string) (net.Conn, *net.UDPAddr) {
	t.Helper()
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{socks5Ver, 0x01, socks5MethodNone}); err != nil {
		t.Fatal(err)
	}
	sel := make([]byte, 2)
	if _, err := io.ReadFull(conn, sel); err != nil {
		t.Fatal(err)
	}
	if sel[0] != socks5Ver || sel[1] != socks5MethodNone {
		t.Fatalf("method negotiation failed: %v", sel)
	}
	// VER CMD RSV ATYP DST.ADDR(0.0.0.0) DST.PORT(0)
	if _, err := conn.Write([]byte{socks5Ver, socks5CmdUDPAssociate, 0x00, socks5AtypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatal(err)
	}
	if head[1] != socks5RepOK {
		t.Fatalf("expected REP OK, got %d", head[1])
	}
	var ip net.IP
	switch head[3] {
	case socks5AtypIPv4:
		b := make([]byte, 4)
		io.ReadFull(conn, b)
		ip = net.IP(b)
	case socks5AtypIPv6:
		b := make([]byte, 16)
		io.ReadFull(conn, b)
		ip = net.IP(b)
	default:
		t.Fatalf("unexpected BND atyp %d", head[3])
	}
	pb := make([]byte, 2)
	io.ReadFull(conn, pb)
	_ = conn.SetDeadline(time.Time{})
	return conn, &net.UDPAddr{IP: ip, Port: int(binary.BigEndian.Uint16(pb))}
}

func TestUDPAssociate_Echo(t *testing.T) {
	echo := startUDPEcho(t)
	l := newTestUDPListener(t)
	srv := startSocksServer(t, l)

	ctrl, bnd := socks5UDPHandshake(t, srv)
	defer ctrl.Close()

	cli, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	payload := []byte("hello-udp-associate")
	dg := append(buildUDPHeader(echo.IP, uint16(echo.Port)), payload...)
	if _, err := cli.WriteToUDP(dg, bnd); err != nil {
		t.Fatal(err)
	}

	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := cli.ReadFromUDP(buf)
	if err != nil {
		t.Fatal("no echo received:", err)
	}
	host, port, _, hdrLen, err := parseUDPHeader(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if !net.ParseIP(host).Equal(echo.IP.To4()) || port != uint16(echo.Port) {
		t.Fatalf("reply src mismatch: host=%s port=%d (want %s:%d)", host, port, echo.IP, echo.Port)
	}
	if string(buf[hdrLen:n]) != string(payload) {
		t.Fatalf("payload mismatch: got %q want %q", buf[hdrLen:n], payload)
	}
}

func TestUDPAssociate_SourceAddrLock(t *testing.T) {
	echo := startUDPEcho(t)
	l := newTestUDPListener(t)
	srv := startSocksServer(t, l)
	ctrl, bnd := socks5UDPHandshake(t, srv)
	defer ctrl.Close()

	cli, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer cli.Close()
	dg := append(buildUDPHeader(echo.IP, uint16(echo.Port)), []byte("first")...)
	cli.WriteToUDP(dg, bnd)
	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	if _, _, err := cli.ReadFromUDP(buf); err != nil {
		t.Fatal("first client should receive echo:", err)
	}

	// Socket thứ 2 (nguồn khác) → datagram phải bị drop.
	cli2, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer cli2.Close()
	cli2.WriteToUDP(dg, bnd)
	_ = cli2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := cli2.ReadFromUDP(buf); err == nil {
		t.Fatal("datagram from unlocked source should be dropped")
	}
}

func TestUDPAssociate_RuleDeny(t *testing.T) {
	l := newTestUDPListener(t)
	l.rules.set([]Rule{{Action: "deny", Kind: "domain", Pattern: "blocked.test"}})
	srv := startSocksServer(t, l)
	ctrl, bnd := socks5UDPHandshake(t, srv)
	defer ctrl.Close()

	cli, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer cli.Close()

	// ATYP=domain "blocked.test" → rule deny chặn TRƯỚC khi resolve.
	dg := []byte{0, 0, 0, socks5AtypDomain, byte(len("blocked.test"))}
	dg = append(dg, []byte("blocked.test")...)
	dg = append(dg, 0x00, 0x35) // port 53
	dg = append(dg, "q"...)
	cli.WriteToUDP(dg, bnd)

	_ = cli.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 2048)
	if _, _, err := cli.ReadFromUDP(buf); err == nil {
		t.Fatal("denied datagram should produce no reply")
	}
}

func TestUDPAssociate_TeardownOnControlClose(t *testing.T) {
	echo := startUDPEcho(t)
	l := newTestUDPListener(t)
	srv := startSocksServer(t, l)
	ctrl, bnd := socks5UDPHandshake(t, srv)

	cli, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	defer cli.Close()
	dg := append(buildUDPHeader(echo.IP, uint16(echo.Port)), []byte("ping")...)
	cli.WriteToUDP(dg, bnd)
	_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	if _, _, err := cli.ReadFromUDP(buf); err != nil {
		t.Fatal("expected echo before close:", err)
	}

	// Đóng control-conn → relay teardown → datagram tiếp theo không còn được relay.
	ctrl.Close()
	time.Sleep(200 * time.Millisecond)
	cli.WriteToUDP(dg, bnd)
	_ = cli.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if _, _, err := cli.ReadFromUDP(buf); err == nil {
		t.Fatal("relay should be torn down after control-conn close")
	}
}
