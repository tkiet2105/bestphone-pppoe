package pppoe

import "testing"

func TestReconcileDecision(t *testing.T) {
	cases := []struct {
		name    string
		ifaceUp bool
		liveIP  string
		dbIP    string
		blocked bool
		healthy bool
		want    reconcileAction
	}{
		{"iface down → redial", false, "", "1.2.3.4", false, false, reconRedial},
		{"up no ip yet → noop", true, "", "1.2.3.4", false, false, reconNoop},
		{"link-local blocked → demote", true, "169.254.1.2", "1.2.3.4", true, false, reconDemote},
		{"cgnat blocked → demote", true, "100.96.0.1", "1.2.3.4", true, false, reconDemote},
		{"ip drift → update+reapply", true, "5.6.7.8", "1.2.3.4", false, false, reconUpdateAndReapply},
		{"ip drift even if healthy-flag → update", true, "5.6.7.8", "1.2.3.4", false, true, reconUpdateAndReapply},
		{"same ip routing missing → reapply", true, "1.2.3.4", "1.2.3.4", false, false, reconReapply},
		{"same ip healthy → noop", true, "1.2.3.4", "1.2.3.4", false, true, reconNoop},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reconcileDecision(c.ifaceUp, c.liveIP, c.dbIP, c.blocked, c.healthy)
			if got != c.want {
				t.Fatalf("reconcileDecision = %d, want %d", got, c.want)
			}
		})
	}
}

func TestParseRuleSourceIPs(t *testing.T) {
	out := `0:	from all lookup local
32296:	from 118.71.67.194 lookup 1144
32297:	from 42.116.227.236 lookup 1000
32766:	from all lookup main
32767:	from all lookup default`
	got := parseRuleSourceIPs(out)
	if !got["118.71.67.194"] || !got["42.116.227.236"] {
		t.Fatalf("missing expected source IPs: %v", got)
	}
	if got["all"] {
		t.Fatal("'all' must not be treated as a source IP")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 source IPs, got %d: %v", len(got), got)
	}
}

func TestParseTablesWithDefaultDev(t *testing.T) {
	out := `default dev ppp144 table 1144 scope link
default dev ppp0 table 1000 scope link
183.81.85.229 dev ppp0 proto kernel scope link src 42.116.227.236
default via 192.168.1.1 dev enp1s0 `
	got := parseTablesWithDefaultDev(out)
	if got[1144] != "ppp144" {
		t.Fatalf("table 1144 → %q, want ppp144", got[1144])
	}
	if got[1000] != "ppp0" {
		t.Fatalf("table 1000 → %q, want ppp0", got[1000])
	}
	// 'default via ... dev enp1s0' (main, không có table) → bỏ qua, không map.
	for tbl, ifc := range got {
		if ifc == "enp1s0" {
			t.Fatalf("main default should be ignored, got table %d → enp1s0", tbl)
		}
	}
}

func TestIfaceUpFromLinkShow(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"ppp up shows UNKNOWN", `12: ppp0: <POINTOPOINT,MULTICAST,NOARP,UP,LOWER_UP> mtu 1492 ... state UNKNOWN mode DEFAULT`, true},
		{"ethernet UP", `2: enp1s0: <BROADCAST,MULTICAST,UP,LOWER_UP> ... state UP mode DEFAULT`, true},
		{"ppp zombie DOWN keeps POINTOPOINT", `2048: ppp124: <POINTOPOINT,MULTICAST,NOARP> mtu 1492 qdisc pfifo_fast state DOWN mode DEFAULT`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ifaceUpFromLinkShow(c.out); got != c.want {
				t.Fatalf("ifaceUpFromLinkShow=%v want %v", got, c.want)
			}
		})
	}
}

func TestRoutingHealthy(t *testing.T) {
	rules := map[string]bool{"42.116.227.236": true}
	tbls := map[int]string{1000: "ppp0", 1124: "ppp124"}

	// ppp0 unit 0 → table 1000 default dev ppp0, có rule from .236 → healthy
	if !RoutingHealthy("ppp0", 0, "42.116.227.236", rules, tbls) {
		t.Fatal("ppp0 should be healthy")
	}
	// thiếu rule from current IP → không healthy
	if RoutingHealthy("ppp124", 124, "42.118.130.158", rules, tbls) {
		t.Fatal("ppp124 without rule should be unhealthy")
	}
	// có rule nhưng table trỏ sai iface → không healthy
	if RoutingHealthy("ppp9", 9, "42.116.227.236", rules, tbls) {
		t.Fatal("table 1009 missing → unhealthy")
	}
	// iface/ip rỗng → không healthy
	if RoutingHealthy("", 0, "", rules, tbls) {
		t.Fatal("empty iface/ip → unhealthy")
	}
}
