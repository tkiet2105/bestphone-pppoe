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

	body := map[string]any{"name": "Line1", "iface": "eth0", "username": "isp", "password": "pass"}
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

func TestCreateLine_DefaultMaxSessions(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/lines", BearerAuth(), CreateLine)

	body := map[string]any{"name": "L2", "iface": "eth1"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/lines", body, tok)
	var line models.Line
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &line)
	if line.MaxSessions != 8 {
		t.Fatalf("expected default max_sessions=8, got %d", line.MaxSessions)
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
