package api

import (
	"encoding/json"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func TestGetSettings_Empty(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/settings", BearerAuth(), GetSettings)

	w := testutil.Do(t, r, "GET", "/api/v1/settings", tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	if !resp.Success {
		t.Fatal("expected success")
	}
}

func TestGetSettings_WithData(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/settings", BearerAuth(), GetSettings)

	db.SeedDefaultSettings()
	w := testutil.Do(t, r, "GET", "/api/v1/settings", tok)
	data := testutil.ParseData[map[string]string](t, w)
	if data["reconnect_enabled"] != "true" {
		t.Errorf("expected reconnect_enabled=true, got %q", data["reconnect_enabled"])
	}
	if data["reconnect_max_retries"] != "1" {
		t.Errorf("expected reconnect_max_retries=1, got %q", data["reconnect_max_retries"])
	}
}

func TestUpdateSettings_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/settings", BearerAuth(), GetSettings)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	db.SeedDefaultSettings()
	body := map[string]string{"reconnect_max_retries": "10", "reconnect_pause_minutes": "30"}
	w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	data := testutil.ParseData[map[string]string](t, w)
	if data["reconnect_max_retries"] != "10" {
		t.Errorf("expected 10, got %q", data["reconnect_max_retries"])
	}
	if data["reconnect_pause_minutes"] != "30" {
		t.Errorf("expected 30, got %q", data["reconnect_pause_minutes"])
	}
	if data["reconnect_enabled"] != "true" {
		t.Errorf("reconnect_enabled should remain true, got %q", data["reconnect_enabled"])
	}
}

func TestUpdateSettings_InvalidKey(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	body := map[string]string{"unknown_key": "value"}
	w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateSettings_InvalidRetriesValue(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	tests := []struct {
		name string
		val  string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"too_high", "101"},
		{"not_number", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]string{"reconnect_max_retries": tt.val}
			w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
			if w.Code != 400 {
				t.Errorf("expected 400 for %q, got %d", tt.val, w.Code)
			}
		})
	}
}

func TestUpdateSettings_InvalidPauseValue(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	tests := []struct {
		name string
		val  string
	}{
		{"zero", "0"},
		{"too_high", "1441"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]string{"reconnect_pause_minutes": tt.val}
			w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
			if w.Code != 400 {
				t.Errorf("expected 400 for %q, got %d", tt.val, w.Code)
			}
		})
	}
}

func TestUpdateSettings_InvalidEnabledValue(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	body := map[string]string{"reconnect_enabled": "maybe"}
	w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateSettings_Persists(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.PUT("/api/v1/settings", BearerAuth(), UpdateSettings)

	db.SeedDefaultSettings()
	body := map[string]string{"reconnect_enabled": "false"}
	w := testutil.DoJSON(t, r, "PUT", "/api/v1/settings", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var raw json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &struct {
		Data *json.RawMessage `json:"data"`
	}{&raw})

	if v := db.GetSettingBool("reconnect_enabled", true); v {
		t.Error("expected reconnect_enabled=false after update")
	}
}
