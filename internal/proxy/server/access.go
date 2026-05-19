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
//  2. Nếu có ÍT NHẤT 1 rule allow trong set → STRICT MODE: chỉ allow nếu host match allow nào.
//  3. Nếu không có allow rule nào → ALLOW (open default).
func (rs *ruleSet) allowed(host string) bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if len(rs.rules) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	// Strip port nếu có
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		if !strings.Contains(host[idx+1:], ".") {
			host = host[:idx]
		}
	}
	hostIP := net.ParseIP(host)

	hasAllow := false
	allowMatched := false
	for _, r := range rs.rules {
		if r.Action == models.RuleActionAllow {
			hasAllow = true
		}
		if !ruleMatch(&r, host, hostIP) {
			continue
		}
		if r.Action == models.RuleActionDeny {
			return false // deny-wins
		}
		if r.Action == models.RuleActionAllow {
			allowMatched = true
		}
	}
	if hasAllow && !allowMatched {
		return false // strict whitelist mode
	}
	return true
}

func ruleMatch(r *Rule, host string, hostIP net.IP) bool {
	switch r.Kind {
	case models.RuleKindDomain:
		if r.IsSuffix {
			return strings.HasSuffix(host, "."+r.Pattern) || host == r.Pattern
		}
		return host == r.Pattern
	case models.RuleKindIP:
		if hostIP == nil || r.IPNet == nil {
			return false
		}
		return r.IPNet.Contains(hostIP)
	}
	return false
}
