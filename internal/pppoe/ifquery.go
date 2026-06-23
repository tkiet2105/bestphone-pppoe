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
	return ifaceUpFromLinkShow(string(out))
}

// ifaceUpFromLinkShow — parse trạng thái từ `ip -o link show`. UP nếu state UP
// hoặc UNKNOWN (ppp khi hoạt động báo "state UNKNOWN"). KHÔNG dựa vào cờ
// <POINTOPOINT>: cờ này luôn có ở MỌI iface ppp kể cả khi state DOWN (zombie —
// addr còn bám nhưng link đã rớt). Bug cũ dùng <POINTOPOINT> → mọi ppp luôn bị
// coi là UP → watchdog không bao giờ redial line DOWN → egress chết vĩnh viễn.
func ifaceUpFromLinkShow(out string) bool {
	if strings.Contains(out, "state DOWN") {
		return false
	}
	return strings.Contains(out, "state UP") || strings.Contains(out, "state UNKNOWN")
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

// PublicIPViaIface — đo public IP qua iface, KHÔNG phụ thuộc DNS.
//
// Trước đây probe dùng `curl --interface <iface> https://api.ipify.org`, nhưng
// --interface ép luôn truy vấn DNS đi qua ppp; resolver ISP thường timeout khi
// bị bind vào iface (đã gặp trên proxy7) → PublicIP rỗng → export rơi về IP LAN.
//
// Fix: hỏi endpoint dạng IP-literal (Cloudflare trace) nên không cần resolve gì.
// Trace trả về nhiều dòng key=value, lấy dòng "ip=". Nếu CF bị chặn, fallback
// sang ipify qua --resolve (tự ghim IP, vẫn không dùng DNS hệ thống).
func PublicIPViaIface(ctx context.Context, iface string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	maxt := fmt.Sprintf("%d", int(timeout.Seconds()))

	// 1) Cloudflare trace qua IP literal — không cần DNS.
	if ip := curlTraceIP(ctx, iface, maxt, timeout); ip != "" {
		return ip, nil
	}

	// 2) Fallback: ipify nhưng ghim sẵn IP (--resolve) để khỏi đụng DNS hệ thống.
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "curl", "-s", "--interface", iface,
		"--max-time", maxt,
		"--resolve", "api.ipify.org:443:104.26.12.205",
		"--resolve", "api.ipify.org:443:104.26.13.205",
		"https://api.ipify.org")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(out))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("không lấy được public IP qua %s", iface)
	}
	return ip, nil
}

// curlTraceIP — curl https://1.1.1.1/cdn-cgi/trace qua iface, parse dòng "ip=".
// Trả "" nếu lỗi/không tìm thấy (để caller fallback).
func curlTraceIP(ctx context.Context, iface, maxt string, timeout time.Duration) string {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "curl", "-s", "--interface", iface,
		"--max-time", maxt, "https://1.1.1.1/cdn-cgi/trace")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ip=") {
			ip := strings.TrimSpace(line[3:])
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	return ""
}
