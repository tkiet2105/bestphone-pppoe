// Package pppoe — query iface trạng thái + đo public IP qua iface.
package pppoe

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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
