package server

import (
	"net"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func TestCompile_DomainExact(t *testing.T) {
	rules := compile([]models.AccessRule{
		{Kind: "domain", Action: "deny", Value: "example.com"},
	})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Pattern != "example.com" || rules[0].IsSuffix {
		t.Fatalf("expected exact domain, got pattern=%q suffix=%v", rules[0].Pattern, rules[0].IsSuffix)
	}
}

func TestCompile_DomainWildcard(t *testing.T) {
	rules := compile([]models.AccessRule{
		{Kind: "domain", Action: "deny", Value: "*.foo.com"},
	})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Pattern != "foo.com" || !rules[0].IsSuffix {
		t.Fatalf("expected suffix foo.com, got pattern=%q suffix=%v", rules[0].Pattern, rules[0].IsSuffix)
	}
}

func TestCompile_IPSingle(t *testing.T) {
	rules := compile([]models.AccessRule{
		{Kind: "ip", Action: "deny", Value: "1.2.3.4"},
	})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].IPNet.String() != "1.2.3.4/32" {
		t.Fatalf("expected 1.2.3.4/32, got %s", rules[0].IPNet.String())
	}
}

func TestCompile_IPCidr(t *testing.T) {
	rules := compile([]models.AccessRule{
		{Kind: "ip", Action: "deny", Value: "10.0.0.0/8"},
	})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].IPNet.String() != "10.0.0.0/8" {
		t.Fatalf("expected 10.0.0.0/8, got %s", rules[0].IPNet.String())
	}
}

func TestCompile_InvalidIP(t *testing.T) {
	rules := compile([]models.AccessRule{
		{Kind: "ip", Action: "deny", Value: "not-an-ip"},
	})
	if len(rules) != 0 {
		t.Fatalf("invalid IP should be skipped, got %d rules", len(rules))
	}
}

func TestAllowed_EmptyRules(t *testing.T) {
	rs := &ruleSet{}
	if !rs.allowed("example.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("empty rules should allow")
	}
}

func TestAllowed_DenyDomain(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "deny", Value: "blocked.com"},
	}))
	if rs.allowed("blocked.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("deny domain should block")
	}
	if !rs.allowed("other.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("non-matching domain should allow")
	}
}

func TestAllowed_AllowDomainStrictMode(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "allow", Value: "allowed.com"},
	}))
	if !rs.allowed("allowed.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("allowed domain should pass")
	}
	if rs.allowed("other.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("strict mode should block non-matching")
	}
}

func TestAllowed_DenyWins(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "allow", Value: "example.com"},
		{Kind: "domain", Action: "deny", Value: "example.com"},
	}))
	if rs.allowed("example.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("deny should win over allow")
	}
}

func TestAllowed_DenyIP(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "ip", Action: "deny", Value: "192.168.1.0/24"},
	}))
	if rs.allowed("example.com", net.ParseIP("192.168.1.50")) {
		t.Fatal("deny IP should block client")
	}
	if !rs.allowed("example.com", net.ParseIP("10.0.0.1")) {
		t.Fatal("non-matching IP should allow")
	}
}

func TestAllowed_AllowIPStrictMode(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "ip", Action: "allow", Value: "10.0.0.1"},
	}))
	if !rs.allowed("example.com", net.ParseIP("10.0.0.1")) {
		t.Fatal("allowed IP should pass")
	}
	if rs.allowed("example.com", net.ParseIP("10.0.0.2")) {
		t.Fatal("strict mode should block non-allowed IP")
	}
}

func TestAllowed_MixedDomainIP(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "allow", Value: "good.com"},
		{Kind: "ip", Action: "deny", Value: "192.168.1.100"},
	}))
	if rs.allowed("good.com", net.ParseIP("192.168.1.100")) {
		t.Fatal("deny IP should block even if domain allowed")
	}
	if !rs.allowed("good.com", net.ParseIP("10.0.0.1")) {
		t.Fatal("allowed domain + non-denied IP should pass")
	}
	if rs.allowed("other.com", net.ParseIP("10.0.0.1")) {
		t.Fatal("strict domain mode should block non-matching domain")
	}
}

func TestAllowed_WildcardSuffix(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "deny", Value: "*.example.com"},
	}))
	if rs.allowed("sub.example.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("wildcard should match subdomain")
	}
	if rs.allowed("example.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("wildcard *.example.com should also match example.com")
	}
	if !rs.allowed("notexample.com", net.ParseIP("1.2.3.4")) {
		t.Fatal("should not match different domain")
	}
}

func TestAllowed_PortStripping(t *testing.T) {
	rs := &ruleSet{}
	rs.set(compile([]models.AccessRule{
		{Kind: "domain", Action: "deny", Value: "blocked.com"},
	}))
	if rs.allowed("blocked.com:443", net.ParseIP("1.2.3.4")) {
		t.Fatal("port should be stripped before matching")
	}
}

func TestRuleMatchDomain_ExactAndSuffix(t *testing.T) {
	exact := Rule{Kind: "domain", Pattern: "foo.com"}
	suffix := Rule{Kind: "domain", Pattern: "foo.com", IsSuffix: true}

	if !ruleMatchDomain(&exact, "foo.com") {
		t.Fatal("exact should match")
	}
	if ruleMatchDomain(&exact, "sub.foo.com") {
		t.Fatal("exact should not match subdomain")
	}
	if !ruleMatchDomain(&suffix, "sub.foo.com") {
		t.Fatal("suffix should match subdomain")
	}
	if !ruleMatchDomain(&suffix, "foo.com") {
		t.Fatal("suffix should match base domain")
	}
}

func TestRuleMatchIP_Contains(t *testing.T) {
	_, ipnet, _ := net.ParseCIDR("10.0.0.0/8")
	r := Rule{Kind: "ip", IPNet: ipnet}

	if !ruleMatchIP(&r, net.ParseIP("10.1.2.3")) {
		t.Fatal("10.1.2.3 should be in 10.0.0.0/8")
	}
	if ruleMatchIP(&r, net.ParseIP("192.168.1.1")) {
		t.Fatal("192.168.1.1 should not be in 10.0.0.0/8")
	}
}

func TestRuleMatchIP_NilIP(t *testing.T) {
	_, ipnet, _ := net.ParseCIDR("10.0.0.0/8")
	r := Rule{Kind: "ip", IPNet: ipnet}
	if ruleMatchIP(&r, nil) {
		t.Fatal("nil IP should not match")
	}
}
