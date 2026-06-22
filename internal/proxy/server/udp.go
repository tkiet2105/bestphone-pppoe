package server

// SOCKS5 UDP ASSOCIATE (RFC1928). Mỗi association = 1 control-conn TCP (đã auth)
// + 2 socket UDP:
//   - clientConn: hướng client, listen trên IP mà client đã reach được (KHÔNG
//     bind ppp) — client gửi datagram bọc header SOCKS5 tới đây.
//   - targetConn: hướng target, bind vào iface ppp<N> qua SO_BINDTODEVICE → mọi
//     UDP egress ra đúng line PPPoE (giống dialBound cho TCP).
// Association sống cùng control-conn; control-conn đóng (hoặc idle/Stop) → teardown.

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	socks5CmdUDPAssociate = 0x03
	udpIdleTimeout        = 60 * time.Second
	udpBufSize            = 65535
)

var (
	errShortUDP = errors.New("udp header too short")
	errBadATYP  = errors.New("udp unsupported atyp")
)

// newBoundListenConfig — bản UDP của newBoundDialer (dial.go): set SO_BINDTODEVICE
// trong ListenConfig.Control. iface=="" → không bind (đường loopback/test).
func newBoundListenConfig(iface string) *net.ListenConfig {
	return &net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			if iface == "" {
				return nil
			}
			var opErr error
			if ctrlErr := c.Control(func(fd uintptr) {
				opErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			}); ctrlErr != nil {
				return ctrlErr
			}
			return opErr
		},
	}
}

func listenPacketBound(ctx context.Context, iface, network, laddr string) (net.PacketConn, error) {
	return newBoundListenConfig(iface).ListenPacket(ctx, network, laddr)
}

type udpAssociation struct {
	l          *listener
	clientConn *net.UDPConn // hướng client (không bind ppp)
	targetConn *net.UDPConn // hướng target (bind ppp<N>)
	clientIP   net.IP       // IP control-conn — dùng cho access rule (kind=ip)
	clientAddr atomic.Pointer[net.UDPAddr]
	idle       atomic.Int64 // unixnano lần hoạt động gần nhất
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	once       sync.Once
	resolver   *net.Resolver

	mu    sync.Mutex
	cache map[string]net.IP // resolve cache domain→IP trong vòng đời association
}

// runUDPAssociate — xử lý nhánh CMD=0x03. Gọi từ handleSocks5 SAU khi đã auth +
// đọc xong DST.ADDR/DST.PORT của request. Block tới khi association kết thúc.
func (l *listener) runUDPAssociate(conn net.Conn) {
	// IP mà client đã reach được (đích của control-conn) → advertise làm BND.ADDR
	// để datagram client gửi tới cùng host/interface.
	var bndIP net.IP
	if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		bndIP = tcpAddr.IP
	}
	if bndIP == nil || bndIP.IsUnspecified() {
		bndIP = net.IPv4zero
	}

	// client-facing socket: KHÔNG bind ppp (phải reach được từ client).
	clientPC, err := net.ListenUDP("udp", &net.UDPAddr{IP: bndIP, Port: 0})
	if err != nil {
		_ = socks5Reply(conn, socks5RepGeneralFail)
		return
	}

	ctx, cancel := context.WithCancel(l.ctx)

	// target-facing socket: bind ppp<N> → egress đúng line.
	targetPC, err := listenPacketBound(ctx, l.iface(), "udp", ":0")
	if err != nil {
		cancel()
		_ = clientPC.Close()
		_ = socks5Reply(conn, socks5RepGeneralFail)
		return
	}
	targetUDP, ok := targetPC.(*net.UDPConn)
	if !ok {
		cancel()
		_ = clientPC.Close()
		_ = targetPC.Close()
		_ = socks5Reply(conn, socks5RepGeneralFail)
		return
	}

	bndPort := clientPC.LocalAddr().(*net.UDPAddr).Port

	a := &udpAssociation{
		l:          l,
		clientConn: clientPC,
		targetConn: targetUDP,
		clientIP:   remoteIP(conn),
		ctx:        ctx,
		cancel:     cancel,
		cache:      make(map[string]net.IP),
	}
	a.idle.Store(time.Now().UnixNano())
	a.resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialBound(ctx, a.l.iface(), network, address)
		},
	}

	if err := socks5ReplyAddr(conn, socks5RepOK, bndIP, uint16(bndPort)); err != nil {
		a.teardown()
		return
	}

	a.wg.Add(3)
	go a.clientReadLoop()
	go a.targetReadLoop()
	go a.idleWatcher()

	// Control-conn đóng → kết thúc association. (Khi listener Stop, l.ctx hủy →
	// ctx hủy → các loop thoát; control-conn read goroutine thoát khi conn.Close
	// ở defer của handleSocks5.)
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		a.cancel()
	}()

	<-a.ctx.Done()
	a.teardown()
	a.wg.Wait()
}

// clientReadLoop — client → target: parse header, lock src, rule check, forward.
func (a *udpAssociation) clientReadLoop() {
	defer a.wg.Done()
	defer a.cancel()
	buf := make([]byte, udpBufSize)
	for {
		n, raddr, err := a.clientConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Lock địa chỉ client ở gói đầu; drop gói từ nguồn khác (chống spoof).
		if a.clientAddr.Load() == nil {
			locked := *raddr
			a.clientAddr.CompareAndSwap(nil, &locked)
		}
		if cur := a.clientAddr.Load(); cur == nil || !udpAddrEqual(cur, raddr) {
			continue
		}

		host, port, frag, hdrLen, perr := parseUDPHeader(buf[:n])
		if perr != nil || frag != 0 { // không hỗ trợ fragment
			continue
		}

		// Access rule (deny-wins) — check TRƯỚC khi resolve để không leak DNS cho
		// domain bị chặn. clientIP = IP control-conn (nhất quán với nhánh CONNECT).
		if a.l.rules != nil && !a.l.rules.allowed(host, a.clientIP) {
			continue
		}

		var targetIP net.IP
		if ip := net.ParseIP(host); ip != nil {
			targetIP = ip
		} else if targetIP = a.resolve(host); targetIP == nil {
			continue
		}

		_, _ = a.targetConn.WriteToUDP(buf[hdrLen:n], &net.UDPAddr{IP: targetIP, Port: int(port)})
		a.idle.Store(time.Now().UnixNano())
	}
}

// targetReadLoop — target → client: bọc header SOCKS5 rồi gửi về client đã lock.
func (a *udpAssociation) targetReadLoop() {
	defer a.wg.Done()
	defer a.cancel()
	buf := make([]byte, udpBufSize)
	for {
		n, raddr, err := a.targetConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		client := a.clientAddr.Load()
		if client == nil {
			continue
		}
		hdr := buildUDPHeader(raddr.IP, uint16(raddr.Port))
		out := make([]byte, 0, len(hdr)+n)
		out = append(out, hdr...)
		out = append(out, buf[:n]...)
		_, _ = a.clientConn.WriteToUDP(out, client)
		a.idle.Store(time.Now().UnixNano())
	}
}

func (a *udpAssociation) idleWatcher() {
	defer a.wg.Done()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(0, a.idle.Load())) > udpIdleTimeout {
				a.cancel()
				return
			}
		}
	}
}

func (a *udpAssociation) teardown() {
	a.once.Do(func() {
		a.cancel()
		if a.clientConn != nil {
			_ = a.clientConn.Close()
		}
		if a.targetConn != nil {
			_ = a.targetConn.Close()
		}
	})
}

// resolve — phân giải domain qua resolver bind iface (DNS không leak ra default
// route). Cache theo association để tránh resolve lại mỗi datagram.
func (a *udpAssociation) resolve(host string) net.IP {
	a.mu.Lock()
	if ip, ok := a.cache[host]; ok {
		a.mu.Unlock()
		return ip
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	ips, err := a.resolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil
	}
	ip := ips[0]
	a.mu.Lock()
	a.cache[host] = ip
	a.mu.Unlock()
	return ip
}

func udpAddrEqual(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// parseUDPHeader — RSV(2) FRAG(1) ATYP(1) DST.ADDR DST.PORT, trả host/port/frag
// và hdrLen (offset bắt đầu DATA).
func parseUDPHeader(b []byte) (host string, port uint16, frag byte, hdrLen int, err error) {
	if len(b) < 4 {
		return "", 0, 0, 0, errShortUDP
	}
	frag = b[2]
	pos := 4
	switch b[3] {
	case socks5AtypIPv4:
		if len(b) < pos+4+2 {
			return "", 0, 0, 0, errShortUDP
		}
		host = net.IP(b[pos : pos+4]).String()
		pos += 4
	case socks5AtypIPv6:
		if len(b) < pos+16+2 {
			return "", 0, 0, 0, errShortUDP
		}
		host = net.IP(b[pos : pos+16]).String()
		pos += 16
	case socks5AtypDomain:
		if len(b) < pos+1 {
			return "", 0, 0, 0, errShortUDP
		}
		dlen := int(b[pos])
		pos++
		if len(b) < pos+dlen+2 {
			return "", 0, 0, 0, errShortUDP
		}
		host = string(b[pos : pos+dlen])
		pos += dlen
	default:
		return "", 0, 0, 0, errBadATYP
	}
	port = binary.BigEndian.Uint16(b[pos : pos+2])
	pos += 2
	return host, port, frag, pos, nil
}

// buildUDPHeader — prefix RSV(0,0) FRAG(0) ATYP DST.ADDR DST.PORT cho reply.
func buildUDPHeader(srcIP net.IP, srcPort uint16) []byte {
	var hdr []byte
	if ip4 := srcIP.To4(); ip4 != nil {
		hdr = make([]byte, 0, 4+4+2)
		hdr = append(hdr, 0, 0, 0, socks5AtypIPv4)
		hdr = append(hdr, ip4...)
	} else {
		hdr = make([]byte, 0, 4+16+2)
		hdr = append(hdr, 0, 0, 0, socks5AtypIPv6)
		hdr = append(hdr, srcIP.To16()...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], srcPort)
	return append(hdr, p[:]...)
}
