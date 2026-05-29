package pppoe

import "testing"

func TestIsBlockedISPIp(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		// user-listed prefixes
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"169.254.1.2", true},
		// other private ranges
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"127.0.0.1", true},
		// public IPs — must NOT be blocked
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"100.63.255.255", false}, // ngay dưới CGNAT range
		{"100.128.0.0", false},    // ngay trên CGNAT range
		{"169.253.1.2", false},    // không phải link-local
		{"172.15.0.1", false},     // ngay dưới 172.16/12
		{"172.32.0.1", false},     // ngay trên 172.16/12
		{"203.0.113.1", false},
		{"14.224.245.142", false}, // production server
		// edge cases
		{"", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		got := IsBlockedISPIp(c.ip)
		if got != c.blocked {
			t.Errorf("IsBlockedISPIp(%q) = %v, want %v", c.ip, got, c.blocked)
		}
	}
}
