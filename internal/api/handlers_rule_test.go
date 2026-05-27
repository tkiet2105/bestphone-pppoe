package api

import (
	"encoding/json"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func setupRuleRouter(t *testing.T) (*testutil.RouterKit) {
	t.Helper()
	r := setupRouter(t)
	hub := events.NewHub()
	SetEventHub(hub)
	proxysrv.Init(db.DB, hub, 55000, 55100)
	tok := testutil.SeedToken(t)

	r.GET("/api/v1/rules", BearerAuth(), ListRules)
	r.POST("/api/v1/rules", BearerAuth(), CreateRule)
	r.PUT("/api/v1/rules/:id", BearerAuth(), UpdateRule)
	r.DELETE("/api/v1/rules/:id", BearerAuth(), DeleteRule)

	return &testutil.RouterKit{R: r, Tok: tok}
}

func TestCreateRule_GlobalDomain(t *testing.T) {
	rk := setupRuleRouter(t)
	body := map[string]any{"scope": "global", "kind": "domain", "action": "deny", "value": "blocked.com"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/rules", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rule models.AccessRule
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &rule)
	if rule.Scope != "global" || rule.Kind != "domain" || rule.Action != "deny" {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestCreateRule_SessionIP(t *testing.T) {
	rk := setupRuleRouter(t)
	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)
	sid := sess.Id
	body := map[string]any{"scope": "session", "session_id": sid, "kind": "ip", "action": "allow", "value": "10.0.0.0/8"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/rules", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateRule_InvalidScope(t *testing.T) {
	rk := setupRuleRouter(t)
	body := map[string]any{"scope": "invalid", "kind": "domain", "action": "deny", "value": "x.com"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/rules", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateRule_SessionScopeWithoutSessionId(t *testing.T) {
	rk := setupRuleRouter(t)
	body := map[string]any{"scope": "session", "kind": "domain", "action": "deny", "value": "x.com"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/rules", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateRule_InvalidIP(t *testing.T) {
	rk := setupRuleRouter(t)
	body := map[string]any{"scope": "global", "kind": "ip", "action": "deny", "value": "not-an-ip"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/rules", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateRule_Success(t *testing.T) {
	rk := setupRuleRouter(t)
	rule := models.AccessRule{Scope: "global", Kind: "domain", Action: "deny", Value: "x.com"}
	db.DB.Create(&rule)

	body := map[string]any{"action": "allow"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/rules/"+uid(rule.Id), body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.AccessRule
	db.DB.First(&updated, rule.Id)
	if updated.Action != "allow" {
		t.Fatalf("expected allow, got %s", updated.Action)
	}
}

func TestDeleteRule_Success(t *testing.T) {
	rk := setupRuleRouter(t)
	rule := models.AccessRule{Scope: "global", Kind: "domain", Action: "deny", Value: "del.com"}
	db.DB.Create(&rule)

	w := testutil.Do(t, rk.R, "DELETE", "/api/v1/rules/"+uid(rule.Id), rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var count int64
	db.DB.Model(&models.AccessRule{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected 0 rules, got %d", count)
	}
}

func TestDeleteRule_NotFound(t *testing.T) {
	rk := setupRuleRouter(t)
	w := testutil.Do(t, rk.R, "DELETE", "/api/v1/rules/999", rk.Tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListRules_Filtering(t *testing.T) {
	rk := setupRuleRouter(t)
	db.DB.Create(&models.AccessRule{Scope: "global", Kind: "domain", Action: "deny", Value: "a.com"})
	db.DB.Create(&models.AccessRule{Scope: "global", Kind: "ip", Action: "allow", Value: "10.0.0.0/8"})

	w := testutil.Do(t, rk.R, "GET", "/api/v1/rules?kind=domain", rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var rules []models.AccessRule
	json.Unmarshal(resp.Data, &rules)
	if len(rules) != 1 {
		t.Fatalf("expected 1 domain rule, got %d", len(rules))
	}
}
