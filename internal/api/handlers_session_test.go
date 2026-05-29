package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func setupSessionRouter(t *testing.T) *testutil.RouterKit {
	t.Helper()
	r := setupRouter(t)
	hub := events.NewHub()
	SetEventHub(hub)
	proxysrv.Init(db.DB, hub, 57000, 57100)

	tok := testutil.SeedToken(t)

	r.GET("/api/v1/sessions", BearerAuth(), ListSessions)
	r.GET("/api/v1/sessions/:id", BearerAuth(), GetSession)
	r.PUT("/api/v1/sessions/:id/auto-rotate", BearerAuth(), SetSessionAutoRotate)
	r.POST("/api/v1/sessions/auto-rotate/batch", BearerAuth(), SetSessionAutoRotateBatch)

	return &testutil.RouterKit{R: r, Tok: tok}
}

func TestListSessions_Empty(t *testing.T) {
	rk := setupSessionRouter(t)
	w := testutil.Do(t, rk.R, "GET", "/api/v1/sessions", rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	var sessions []json.RawMessage
	json.Unmarshal(resp.Data, &sessions)
	if len(sessions) != 0 {
		t.Fatalf("expected empty, got %d", len(sessions))
	}
}

func TestListSessions_WithFilters(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	s1 := models.Session{LineId: line.Id, PppUnit: 0, Username: "u1", Password: "p1", Status: "connected"}
	s2 := models.Session{LineId: line.Id, PppUnit: 1, Username: "u2", Password: "p2", Status: "disconnected"}
	db.DB.Create(&s1)
	db.DB.Create(&s2)

	w := testutil.Do(t, rk.R, "GET", "/api/v1/sessions?status=connected", rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var sessions []json.RawMessage
	json.Unmarshal(resp.Data, &sessions)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 connected session, got %d", len(sessions))
	}
}

func TestGetSession_Success(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)

	w := testutil.Do(t, rk.R, "GET", "/api/v1/sessions/"+uid(sess.Id), rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetSession_NotFound(t *testing.T) {
	rk := setupSessionRouter(t)
	w := testutil.Do(t, rk.R, "GET", "/api/v1/sessions/999", rk.Tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSetSessionAutoRotate_Success(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)

	body := map[string]any{"seconds": 120}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/auto-rotate", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.Session
	db.DB.First(&updated, sess.Id)
	if updated.AutoRotateSeconds != 120 {
		t.Fatalf("expected 120, got %d", updated.AutoRotateSeconds)
	}
}

func TestSetSessionAutoRotate_Disable(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)
	db.DB.Model(&sess).Update("auto_rotate_seconds", 120)

	body := map[string]any{"seconds": 0}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/auto-rotate", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSetSessionAutoRotate_TooLow(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)

	body := map[string]any{"seconds": 30}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/auto-rotate", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 for <60s, got %d", w.Code)
	}
}

func TestSetSessionAutoRotate_Negative(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)

	body := map[string]any{"seconds": -1}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/auto-rotate", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 for negative, got %d", w.Code)
	}
}

func TestSetSessionAutoRotateBatch_Success(t *testing.T) {
	rk := setupSessionRouter(t)
	line := testutil.SeedLine(t)
	s1 := models.Session{LineId: line.Id, PppUnit: 10, Username: "u1", Password: "p1", Status: "connected"}
	s2 := models.Session{LineId: line.Id, PppUnit: 11, Username: "u2", Password: "p2", Status: "connected"}
	db.DB.Create(&s1)
	db.DB.Create(&s2)

	body := map[string]any{"session_ids": []uint{s1.Id, s2.Id}, "seconds": 300}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/sessions/auto-rotate/batch", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var data map[string]any
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &data)
	if int(data["updated"].(float64)) != 2 {
		t.Fatalf("expected 2 updated, got %v", data["updated"])
	}
}

func TestTruncateErr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"PAP AuthNak received from BRAS", "PAP AuthNak — cred không khớp BRAS"},
		{"authentication failed", "PAP AuthNak — cred không khớp BRAS"},
		{"no PADO response", "No PADO — NIC không nhận PPPoE upstream"},
		{"timeout waiting for PADO", "No PADO — NIC không nhận PPPoE upstream"},
		{"iface ppp0 did not come up", "iface không lên UP"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := truncateErr(tt.input, 240)
		if got != tt.expected {
			t.Errorf("truncateErr(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}

	long := ""
	for i := 0; i < 300; i++ {
		long += "x"
	}
	got := truncateErr(long, 240)
	if len(got) > 244 {
		t.Errorf("expected truncated to ~243, got len=%d", len(got))
	}
}

// uid is defined in handlers_line_test.go; redeclare here for standalone use
func init() {
	_ = fmt.Sprintf // ensure fmt imported
}

// ---------- SetSessionType tests ----------

func setupSessionTypeRouter(t *testing.T) *testutil.RouterKit {
	t.Helper()
	rk := setupSessionRouter(t)
	rk.R.PUT("/api/v1/sessions/:id/type", BearerAuth(), SetSessionType)
	rk.R.POST("/api/v1/sessions/type/batch", BearerAuth(), SetSessionTypeBatch)
	return rk
}

func TestSetSessionType_Success(t *testing.T) {
	rk := setupSessionTypeRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)

	body := map[string]any{"type": "static"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/type", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var s models.Session
	db.DB.First(&s, sess.Id)
	if s.Type != "static" {
		t.Errorf("expected type=static, got %s", s.Type)
	}
}

func TestSetSessionType_InvalidType(t *testing.T) {
	rk := setupSessionTypeRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)
	body := map[string]any{"type": "bogus"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/type", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSetSessionType_NotFound(t *testing.T) {
	rk := setupSessionTypeRouter(t)
	body := map[string]any{"type": "static"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/9999/type", body, rk.Tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSetSessionType_ExceedsMax(t *testing.T) {
	rk := setupSessionTypeRouter(t)
	line := testutil.SeedLine(t)
	// Tạo session với type=rotating + 3 creds. Đổi sang private (max=1) → fail.
	sess := models.Session{
		LineId: line.Id, PppUnit: 700, Username: "u", Password: "p",
		Type: models.SessionTypeRotating, Status: models.StatusConnected,
	}
	db.DB.Create(&sess)
	proxy := models.Proxy{SessionId: sess.Id, Port: 58600, Status: "running"}
	db.DB.Create(&proxy)
	for i := 0; i < 3; i++ {
		// IUserId set → simulate claimed creds (default creds với IUserId="" sẽ
		// KHÔNG count vào quota — đó là design mới sau v1.8.7)
		db.DB.Create(&models.ProxyCredential{
			ProxyId: proxy.Id, Username: fmt.Sprintf("u%d", i), Password: "p", Enabled: true,
			IUserId: fmt.Sprintf("iuser%d", i),
		})
	}

	body := map[string]any{"type": "private"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/sessions/"+uid(sess.Id)+"/type", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 (exceeds max), got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetSessionTypeBatch_MixedResults(t *testing.T) {
	rk := setupSessionTypeRouter(t)
	line := testutil.SeedLine(t)

	// Session A: 0 creds → đổi sang private OK.
	sa := models.Session{
		LineId: line.Id, PppUnit: 800, Username: "u", Password: "p",
		Type: models.SessionTypeRotating, Status: models.StatusConnected,
	}
	db.DB.Create(&sa)
	pa := models.Proxy{SessionId: sa.Id, Port: 58700, Status: "running"}
	db.DB.Create(&pa)

	// Session B: 2 creds → đổi sang private (max=1) fail.
	sb := models.Session{
		LineId: line.Id, PppUnit: 801, Username: "u", Password: "p",
		Type: models.SessionTypeRotating, Status: models.StatusConnected,
	}
	db.DB.Create(&sb)
	pb := models.Proxy{SessionId: sb.Id, Port: 58701, Status: "running"}
	db.DB.Create(&pb)
	for i := 0; i < 2; i++ {
		// Set IUserId → simulate claimed creds (count vào quota)
		db.DB.Create(&models.ProxyCredential{
			ProxyId: pb.Id, Username: fmt.Sprintf("ub%d", i), Password: "p", Enabled: true,
			IUserId: fmt.Sprintf("iuser-b%d", i),
		})
	}

	body := map[string]any{"session_ids": []uint{sa.Id, sb.Id, 9999}, "type": "private"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/sessions/type/batch", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var results []struct {
		SessionId uint   `json:"session_id"`
		Updated   bool   `json:"updated"`
		Error     string `json:"error,omitempty"`
	}
	json.Unmarshal(resp.Data, &results)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !results[0].Updated {
		t.Errorf("session A should update, got error: %s", results[0].Error)
	}
	if results[1].Updated {
		t.Errorf("session B should fail (over max)")
	}
	if results[2].Updated || results[2].Error == "" {
		t.Errorf("session 9999 should fail not-found")
	}
}
