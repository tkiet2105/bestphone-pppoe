package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func TestCreateLine_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	body := map[string]any{"name": "Line1", "iface": "eth0", "username": "isp", "password": "pass", "max_sessions": 100}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	if !resp.Success {
		t.Fatal("expected success")
	}
	var line models.Line
	json.Unmarshal(resp.Data, &line)
	if line.Name != "Line1" {
		t.Fatalf("expected name Line1, got %s", line.Name)
	}
	if line.Id == 0 {
		t.Fatal("expected non-zero ID")
	}
}

func TestCreateLine_MissingName(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	body := map[string]any{"iface": "eth0"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// max_sessions = đúng số admin đặt: thiếu/≤0 → 400 (không tự ép mặc định).
func TestCreateLine_MaxSessionsRequired(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	for _, body := range []map[string]any{
		{"name": "L2", "iface": "eth1"},
		{"name": "L2", "iface": "eth1", "max_sessions": 0},
		{"name": "L2", "iface": "eth1", "max_sessions": -5},
	} {
		w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
		if w.Code != 400 {
			t.Fatalf("body %v: expected 400, got %d: %s", body, w.Code, w.Body.String())
		}
	}
	var n int64
	db.DB.Model(&models.Line{}).Count(&n)
	if n != 0 {
		t.Fatalf("expected no line created, got %d", n)
	}
}

// Không cắt trần: 100, 250 lưu nguyên.
func TestCreateLine_MaxSessionsNoClamp(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	for _, want := range []int{100, 250} {
		body := map[string]any{"name": fmt.Sprintf("L%d", want), "iface": "eth1", "max_sessions": want}
		w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var line models.Line
		json.Unmarshal(testutil.ParseResponse(t, w).Data, &line)
		var stored models.Line
		db.DB.First(&stored, line.Id)
		if line.MaxSessions != want || stored.MaxSessions != want {
			t.Fatalf("expected max_sessions=%d, got resp=%d db=%d", want, line.MaxSessions, stored.MaxSessions)
		}
	}
}

func TestUpdateLine_MaxSessions(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/lines/:id", BearerAuth(), UpdateLine)
	line := testutil.SeedLine(t)

	w := testutil.DoJSON(t, r, "PUT", "/api/v1/lines/"+uid(line.Id), map[string]any{"max_sessions": 0}, tok)
	if w.Code != 400 {
		t.Fatalf("max_sessions=0: expected 400, got %d", w.Code)
	}
	w = testutil.DoJSON(t, r, "PUT", "/api/v1/lines/"+uid(line.Id), map[string]any{"max_sessions": 100}, tok)
	if w.Code != 200 {
		t.Fatalf("max_sessions=100: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var stored models.Line
	db.DB.First(&stored, line.Id)
	if stored.MaxSessions != 100 {
		t.Fatalf("expected 100, got %d", stored.MaxSessions)
	}
}

func TestUpdateLine_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/lines/:id", BearerAuth(), UpdateLine)

	line := testutil.SeedLine(t)
	newName := "Updated"
	body := map[string]any{"name": newName}
	w := testutil.DoJSON(t, r, "PUT", "/api/v1/lines/"+uid(line.Id), body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.Line
	db.DB.First(&updated, line.Id)
	if updated.Name != "Updated" {
		t.Fatalf("expected Updated, got %s", updated.Name)
	}
}

func TestUpdateLine_NotFound(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/lines/:id", BearerAuth(), UpdateLine)

	w := testutil.DoJSON(t, r, "PUT", "/api/v1/lines/999", map[string]any{"name": "x"}, tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListLines_Empty(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/lines", BearerAuth(), ListLines)

	w := testutil.Do(t, r, "GET", "/api/v1/lines", tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	var lines []json.RawMessage
	json.Unmarshal(resp.Data, &lines)
	if len(lines) != 0 {
		t.Fatalf("expected empty, got %d", len(lines))
	}
}

func TestListLines_WithData(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/lines", BearerAuth(), ListLines)

	line := testutil.SeedLine(t)
	testutil.SeedSession(t, line.Id)

	w := testutil.Do(t, r, "GET", "/api/v1/lines", tok)
	resp := testutil.ParseResponse(t, w)
	var rows []map[string]any
	json.Unmarshal(resp.Data, &rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1, got %d", len(rows))
	}
	if int(rows[0]["session_count"].(float64)) != 1 {
		t.Fatalf("expected session_count=1, got %v", rows[0]["session_count"])
	}
}

func TestGetLine_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/lines/:id", BearerAuth(), GetLine)

	line := testutil.SeedLine(t)
	w := testutil.Do(t, r, "GET", "/api/v1/lines/"+uid(line.Id), tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetLine_NotFound(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/lines/:id", BearerAuth(), GetLine)

	w := testutil.Do(t, r, "GET", "/api/v1/lines/999", tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func uid(n uint) string {
	return fmt.Sprintf("%d", n)
}

// ---------- CustomMacs tests ----------

func TestCreateLine_WithCustomMacs(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	body := map[string]any{
		"name": "L-mac", "iface": "eth-mac", "max_sessions": 8,
		"custom_macs": "AA:BB:CC:DD:EE:01\naa:bb:cc:dd:ee:02\naa:bb:cc:dd:ee:01\n", // có duplicate + uppercase
	}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var line models.Line
	json.Unmarshal(resp.Data, &line)
	// Normalize: lowercase + dedup → 2 dòng
	wantLines := []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"}
	got := models.ParseMacs(line.CustomMacs)
	if len(got) != len(wantLines) {
		t.Fatalf("expected %d MACs, got %d (%v)", len(wantLines), len(got), got)
	}
	for i, w := range wantLines {
		if got[i] != w {
			t.Errorf("expected MAC[%d]=%s, got %s", i, w, got[i])
		}
	}
}

func TestCreateLine_InvalidMacFormat(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	body := map[string]any{
		"name": "L-bad", "iface": "eth-bad", "max_sessions": 8,
		"custom_macs": "not-a-mac",
	}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 (invalid MAC), got %d", w.Code)
	}
}

func TestParseMacs(t *testing.T) {
	got := models.ParseMacs("aa:bb:cc:dd:ee:01\nbb:cc:dd:ee:ff:02 , cc:dd:ee:ff:00:03")
	want := []string{"aa:bb:cc:dd:ee:01", "bb:cc:dd:ee:ff:02", "cc:dd:ee:ff:00:03"}
	if len(got) != len(want) {
		t.Fatalf("expected %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] want %s, got %s", i, want[i], got[i])
		}
	}
}

func TestIsValidMac(t *testing.T) {
	cases := map[string]bool{
		"aa:bb:cc:dd:ee:ff": true,
		"AA:BB:CC:DD:EE:FF": false, // require lowercase (ParseMacs normalizes first)
		"aabbccddeeff":      false,
		"aa:bb:cc:dd:ee":    false,
		"aa-bb-cc-dd-ee-ff": false,
		"":                  false,
	}
	for in, want := range cases {
		if got := models.IsValidMac(in); got != want {
			t.Errorf("IsValidMac(%q) = %v, want %v", in, got, want)
		}
	}
}
