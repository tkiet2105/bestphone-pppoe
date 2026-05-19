// Package server — proxy listeners per session. SO_BINDTODEVICE outbound vào iface
// PPPoE để traffic ra đúng line, không qua default route (kết hợp `nodefaultroute`
// trong peer config).
package server

import (
	"context"
	"net"
	"syscall"
	"time"
)

// newBoundDialer — Dialer bind socket vào iface qua SO_BINDTODEVICE.
// Cần CAP_NET_RAW (service chạy root qua systemd).
func newBoundDialer(iface string) *net.Dialer {
	return &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
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

func dialBound(ctx context.Context, iface, network, address string) (net.Conn, error) {
	return newBoundDialer(iface).DialContext(ctx, network, address)
}
