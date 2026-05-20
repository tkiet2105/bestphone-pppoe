package server

import (
	"net"
	"strings"
	"sync"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

// Rule — compiled access rule (1 cho hiệu năng match).
type Rule struct {
	Action   string // allow | deny
	Kind     string // domain | ip
	Pattern  string // raw value
	IsSuffix bool   // *.foo.com → suffix match foo.com
	IPNet    *net.IPNet
}

// ruleSet — bộ rule cho 1 listener: global + session-scope đã merge.
type ruleSet struct {
	mu    sync.RWMutex
	rules []Rule
}

func compile(rules []models.AccessRule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		cr := Rule{Action: r.Action, Kind: r.Kind, Pattern: r.Value}
		switch r.Kind {
		case models.RuleKindDomain:
			if strings.HasPrefix(r.Value, "*.") {
				cr.IsSuffix = true
				cr.Pattern = strings.ToLower(r.Value[2:])
			} else {
				cr.Pattern = strings.ToLower(r.Value)
			}
		case models.RuleKindIP:
			val := r.Value
			if !strings.Contains(val, "/") {
				// /32 cho IPv4, /128 cho IPv6
				if strings.Contains(val, ":") {
					val += "/128"
				} else {
					val += "/32"
				}
			}
			if _, ipnet, err := net.ParseCIDR(val); err == nil {
				cr.IPNet = ipnet
			} else {
				continue // skip rule invalid
			}
		default:
			continue
		}
		out = append(out, cr)
	}
	return out
}

func (rs *ruleSet) set(rules []Rule) {
	rs.mu.Lock()
	rs.rules = rules
	rs.mu.Unlock()
}

// allowed — áp dụng deny-wins:
//  1. Nếu bất kỳ rule deny match → BLOCK (false).
//  2. Nếu có ÍT NHẤT 1 rule allow trong set → STRICT MODE: chỉ allow nếu match.
//  3. Nếu không có allow rule nào → ALLOW (open default).
//
// Tham số:
//   - dest: host đích client muốn kết nối (vd "facebook.com"). Match với rule
//     kind=domain.
//   - clientIP: IP của client đang gọi proxy (lấy từ conn.RemoteAddr). Match
//     với rule kind=ip. Đây là cách hiểu trực quan cho proxy: "chặn IP X" =
//     không cho X dùng proxy. Lọc theo destination IP gần như không hữu dụng
//     trong proxy use-case → kind=ip giờ chỉ áp dụng cho clientIP.
func (rs *ruleSet) allowed(dest string, clientIP net.IP) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if len(rs.rules) == 0 {
		return true
	}
	dest = strings.ToLower(strings.TrimSpace(dest))
	if idx := strings.LastIndex(dest, ":"); idx > 0 {
		if !strings.Contains(dest[idx+1:], ".") {
			dest = dest[:idx]
		}
	}

	// Tách allow/deny TÁCH BIỆT theo kind để strict-mode hoạt động đúng:
	// allow chỉ thuộc về phạm vi (domain hoặc client_ip) tương ứng — không lấn
	// sang phạm vi khác. Vd có "allow ip 1.2.3.4" thì không ép buộc strict mode
	// trên các domain request từ IP khác.
	hasAllowDomain := false
	hasAllowClientIP := false
	allowMatchedDomain := false
	allowMatchedClientIP := false

	for _, r := range rs.rules {
		switch r.Kind {
		case models.RuleKindDomain:
			if r.Action == models.RuleActionAllow {
				hasAllowDomain = true
			}
			if !ruleMatchDomain(&r, dest) {
				continue
			}
			if r.Action == models.RuleActionDeny {
				return false // deny-wins
			}
			allowMatchedDomain = true
		case models.RuleKindIP:
			if r.Action == models.RuleActionAllow {
				hasAllowClientIP = true
			}
			if !ruleMatchIP(&r, clientIP) {
				continue
			}
			if r.Action == models.RuleActionDeny {
				return false // deny-wins
			}
			allowMatchedClientIP = true
		}
	}
	if hasAllowDomain && !allowMatchedDomain {
		return false
	}
	if hasAllowClientIP && !allowMatchedClientIP {
		return false
	}
	return true
}

func ruleMatchDomain(r *Rule, host string) bool {
	if r.Kind != models.RuleKindDomain {
		return false
	}
	if r.IsSuffix {
		return strings.HasSuffix(host, "."+r.Pattern) || host == r.Pattern
	}
	return host == r.Pattern
}

func ruleMatchIP(r *Rule, clientIP net.IP) bool {
	if r.Kind != models.RuleKindIP || clientIP == nil || r.IPNet == nil {
		return false
	}
	return r.IPNet.Contains(clientIP)
}

// remoteIP — extract client IP từ conn.RemoteAddr. Trả nil nếu parse fail.
func remoteIP(conn net.Conn) net.IP {
	if conn == nil {
		return nil
	}
	addr := conn.RemoteAddr()
	if addr == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}
