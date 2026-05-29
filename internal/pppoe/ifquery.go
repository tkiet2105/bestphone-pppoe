// Package pppoe — query iface trạng thái + đo public IP qua iface.
package pppoe

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// IsBlockedISPIp — true nếu IP nằm trong dải private/CGNAT/link-local
// (ISP NAT — proxy không thể có public IP riêng). Bao gồm:
//   - 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 (RFC1918)
//   - 100.64.0.0/10 (CGNAT, RFC6598)
//   - 169.254.0.0/16 (APIPA link-local)
//   - 127.0.0.0/8 (loopback)
func IsBlockedISPIp(ipStr string) bool {
	if ipStr == "" {
		return false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true // CGNAT 100.64/10
	}
	return false
}

// IsIfaceUp — `ip -o link show <iface>` chứa "UP" (hoặc UNKNOWN cho ppp).
func IsIfaceUp(iface string) bool {
	out, err := exec.Command("ip", "-o", "link", "show", iface).CombinedOutput()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, "state UP") || strings.Contains(s, "state UNKNOWN") || strings.Contains(s, "<POINTOPOINT")
}

// IfaceIPv4 — parse `ip -4 -o addr show <iface>` → first inet.
func IfaceIPv4(iface string) string {
	out, err := exec.Command("ip", "-4", "-o", "addr", "show", iface).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "inet" && i+1 < len(fields) {
				return strings.SplitN(fields[i+1], "/", 2)[0]
			}
		}
	}
	return ""
}

// PublicIPViaIface — curl --interface <iface> https://api.ipify.org. Timeout chặt.
func PublicIPViaIface(ctx context.Context, iface string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "curl", "-s", "--interface", iface, "--max-time", fmt.Sprintf("%d", int(timeout.Seconds())), "https://api.ipify.org")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
