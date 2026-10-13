package api

import (
	"testing"
	"time"
)

func TestNextGap(t *testing.T) {
	base := 30 * time.Second
	if g := nextGap(base, base, true); g != 60*time.Second {
		t.Fatalf("bị chặn phải gấp đôi: %v", g)
	}
	if g := nextGap(480*time.Second, base, true); g != 600*time.Second {
		t.Fatalf("trần 600s: %v", g)
	}
	if g := nextGap(240*time.Second, base, false); g != base {
		t.Fatalf("thành công phải về mức gốc: %v", g)
	}
}

func TestBulkRetryable(t *testing.T) {
	yes := []string{
		"ISP cấp IP CGNAT/private (169.254.252.1) - không dùng được",
		"pppd: No PADO (BRAS không trả lời PADI)",
		"iface ppp12 did not come UP",
		"LCP terminated by peer (BRAS ngắt phiên)",
	}
	for _, m := range yes {
		if !bulkRetryable(m) {
			t.Errorf("phải thử lại: %q", m)
		}
	}
	for _, m := range []string{"pppd: AuthNak (cred không khớp BRAS)", "PAP authentication failed", "server hết ppp unit"} {
		if bulkRetryable(m) {
			t.Errorf("KHÔNG được thử lại: %q", m)
		}
	}
	if !bulkThrottled("ISP cấp IP CGNAT/private (169.254.1.1)") || bulkThrottled("iface ppp1 did not come UP") {
		t.Error("bulkThrottled phân loại sai")
	}
}
