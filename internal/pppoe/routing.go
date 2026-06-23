package pppoe

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Reply-path policy routing cho từng line PPPoE.
//
// Vấn đề: client ngoài vào THẲNG public_ip:port của line. Gói đến trên pppN,
// nhưng reply (source = public IP của line) lại tra bảng `main` → ra default
// route (vd enp1s0) thay vì quay lại pppN → bất đối xứng. Handshake/auth (gói
// nhỏ) lọt, data lớn rớt → "auth OK rồi treo".
//
// Fix: mỗi line khi UP tạo bảng route riêng + ip rule theo source IP:
//   ip route replace default dev pppN table T
//   ip rule add from <public_ip> table T
// để reply có source = public IP đi đúng pppN. Kèm MSS clamp vì pppN MTU=1492.
//
// Vòng đời: áp trong Dial() sau khi có wanIP; dọn trong Hangup()/rotate/demote.
// Table T = policyTableBase + ppp_unit (ổn định theo unit → cleanup idempotent).

// policyTableBase — base cao để tránh các table reserved (253 default, 254 main,
// 255 local). ppp_unit 0..999 → table 1000..1999.
const policyTableBase = 1000

func policyTable(pppUnit int) int { return policyTableBase + pppUnit }

func runIP(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// mssRule — args iptables cho MSS clamp trên OUTPUT (connection do listener cục
// bộ sinh ra, KHÔNG forward → chain OUTPUT). action = "-A" | "-D" | "-C".
func mssRule(action, iface string) []string {
	return []string{
		"-t", "mangle", action, "OUTPUT", "-o", iface,
		"-p", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--clamp-mss-to-pmtu",
	}
}

// ApplyReplyRouting — thiết lập policy routing + MSS clamp cho 1 line đã UP.
// Idempotent: dọn rule/table cũ của cùng ppp_unit trước khi thêm (chống tích tụ
// khi rotate đổi IP). Không fatal nếu lỗi — caller chỉ nên log cảnh báo.
func ApplyReplyRouting(iface, ip string, pppUnit int) error {
	if iface == "" || ip == "" {
		return fmt.Errorf("apply reply routing: iface/ip rỗng (iface=%q ip=%q)", iface, ip)
	}
	t := strconv.Itoa(policyTable(pppUnit))

	// 1) Dọn rule/table cũ (IP có thể đã đổi qua rotate → rule cũ thành rác)
	RemoveReplyRouting(iface, pppUnit)

	// 2) Default route trong table riêng → ra đúng pppN (point-to-point, không gw)
	if out, err := runIP("ip", "route", "replace", "default", "dev", iface, "table", t); err != nil {
		return fmt.Errorf("ip route default dev %s table %s: %v (%s)", iface, t, err, out)
	}
	// 3) Rule: gói có source = public IP của line → tra table riêng
	if out, err := runIP("ip", "rule", "add", "from", ip, "table", t); err != nil {
		return fmt.Errorf("ip rule add from %s table %s: %v (%s)", ip, t, err, out)
	}
	// 4) MSS clamp cho TCP ra pppN (MTU 1492) — tránh PMTU blackhole.
	//    delete-then-add để không nhân đôi rule.
	_, _ = runIP("iptables", mssRule("-D", iface)...)
	if out, err := runIP("iptables", mssRule("-A", iface)...); err != nil {
		return fmt.Errorf("iptables MSS clamp %s: %v (%s)", iface, err, out)
	}
	return nil
}

// RuleSourceIPs — đọc `ip rule show`, trả tập các source IP đang có rule
// `from <ip> ...`. Dùng để biết line nào đã có policy-rule egress (snapshot
// 1 lần/tick, tránh gọi mỗi session). Lỗi → map rỗng (caller coi như chưa có).
func RuleSourceIPs() map[string]bool {
	out, _ := runIP("ip", "rule", "show")
	return parseRuleSourceIPs(out)
}

func parseRuleSourceIPs(out string) map[string]bool {
	res := make(map[string]bool)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i, w := range f {
			if w == "from" && i+1 < len(f) {
				ip := f[i+1]
				if ip != "all" {
					res[ip] = true
				}
			}
		}
	}
	return res
}

// TablesWithDefaultDev — đọc `ip route show table all`, trả map table→iface cho
// các route `default dev pppX ... table T`. Dùng kiểm policy-table đã có default
// route ra đúng line chưa.
func TablesWithDefaultDev() map[int]string {
	out, _ := runIP("ip", "route", "show", "table", "all")
	return parseTablesWithDefaultDev(out)
}

func parseTablesWithDefaultDev(out string) map[int]string {
	res := make(map[int]string)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "default" {
			continue
		}
		var iface string
		var tbl int = -1
		for i, w := range f {
			if w == "dev" && i+1 < len(f) {
				iface = f[i+1]
			}
			if w == "table" && i+1 < len(f) {
				if n, err := strconv.Atoi(f[i+1]); err == nil {
					tbl = n
				}
			}
		}
		if iface != "" && tbl >= 0 {
			res[tbl] = iface
		}
	}
	return res
}

// RoutingHealthy — policy routing của line đã đầy đủ chưa: có rule `from ip` VÀ
// table `1000+unit` có `default dev iface`. rules/tbls là snapshot 1 lần/tick.
func RoutingHealthy(iface string, pppUnit int, ip string, rules map[string]bool, tbls map[int]string) bool {
	if iface == "" || ip == "" {
		return false
	}
	if !rules[ip] {
		return false
	}
	return tbls[policyTable(pppUnit)] == iface
}

// RemoveReplyRouting — gỡ rule + flush table + MSS clamp cho 1 line. Idempotent;
// nuốt lỗi (rule có thể đã không tồn tại). Cleanup theo table number (ổn định
// theo ppp_unit) nên không cần biết IP cũ — an toàn cả khi IP đã đổi.
func RemoveReplyRouting(iface string, pppUnit int) {
	t := strconv.Itoa(policyTable(pppUnit))
	// Xoá mọi rule trỏ tới table T (lặp tới khi hết)
	for i := 0; i < 16; i++ {
		if _, err := runIP("ip", "rule", "del", "table", t); err != nil {
			break
		}
	}
	_, _ = runIP("ip", "route", "flush", "table", t)
	if iface != "" {
		// Xoá hết bản sao MSS rule cho iface này (nếu có)
		for i := 0; i < 8; i++ {
			if _, err := runIP("iptables", mssRule("-D", iface)...); err != nil {
				break
			}
		}
	}
}
