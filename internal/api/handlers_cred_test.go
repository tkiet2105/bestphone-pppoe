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

func setupCredRouter(t *testing.T) (*testutil.RouterKit, models.Proxy) {
	t.Helper()
	r := setupRouter(t)
	hub := events.NewHub()
	SetEventHub(hub)
	proxysrv.Init(db.DB, hub, 56000, 56100)
	tok := testutil.SeedToken(t)

	r.GET("/api/v1/proxies/:id/credentials", BearerAuth(), ListCreds)
	r.POST("/api/v1/proxies/:id/credentials", BearerAuth(), CreateCred)
	r.POST("/api/v1/proxies/:id/credentials/bulk", BearerAuth(), BulkCreateCreds)
	r.PUT("/api/v1/proxies/:id/credentials/:cid", BearerAuth(), UpdateCred)
	r.DELETE("/api/v1/proxies/:id/credentials/:cid", BearerAuth(), DeleteCred)

	line := testutil.SeedLine(t)
	sess := testutil.SeedSession(t, line.Id)
	proxy := testutil.SeedProxy(t, sess.Id)

	return &testutil.RouterKit{R: r, Tok: tok}, proxy
}

func TestCreateCred_Success(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"username": "user1", "password": "pass1"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cred models.ProxyCredential
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &cred)
	if cred.Username != "user1" {
		t.Fatalf("expected user1, got %s", cred.Username)
	}
	if !cred.Enabled {
		t.Fatal("new cred should be enabled")
	}
}

func TestCreateCred_MissingFields(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"username": "user1"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBulkCreateCreds_Success(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"count": 5, "prefix": "test"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/bulk", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var creds []models.ProxyCredential
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &creds)
	if len(creds) != 5 {
		t.Fatalf("expected 5 creds, got %d", len(creds))
	}
}

func TestBulkCreateCreds_InvalidCount(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"count": 201}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/bulk", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateCred_Success(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	cred := models.ProxyCredential{ProxyId: proxy.Id, Username: "u", Password: "p", Enabled: true}
	db.DB.Create(&cred)

	newUser := "updated-user"
	body := map[string]any{"username": newUser}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/"+uid(cred.Id), body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.ProxyCredential
	db.DB.First(&updated, cred.Id)
	if updated.Username != newUser {
		t.Fatalf("expected %s, got %s", newUser, updated.Username)
	}
}

func TestUpdateCred_NotFound(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"username": "x"}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/999", body, rk.Tok)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteCred_Success(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	cred := models.ProxyCredential{ProxyId: proxy.Id, Username: "del", Password: "me", Enabled: true}
	db.DB.Create(&cred)

	w := testutil.Do(t, rk.R, "DELETE", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/"+uid(cred.Id), rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var count int64
	db.DB.Model(&models.ProxyCredential{}).Where("id = ?", cred.Id).Count(&count)
	if count != 0 {
		t.Fatal("cred should be deleted")
	}
}

func TestCreateCred_WithTTL(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"username": "ttl-user", "password": "ttl-pass", "ttl": 3600}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cred models.ProxyCredential
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &cred)
	if cred.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set for ttl=3600")
	}
}

func TestCreateCred_ZeroTTL(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"username": "perm-user", "password": "perm-pass", "ttl": 0}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cred models.ProxyCredential
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &cred)
	if cred.ExpiresAt != nil {
		t.Fatal("ExpiresAt should be nil for ttl=0")
	}
}

func TestBulkCreateCreds_WithTTL(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	body := map[string]any{"count": 3, "prefix": "t", "ttl": 7200}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/bulk", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var creds []models.ProxyCredential
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &creds)
	for i, cr := range creds {
		if cr.ExpiresAt == nil {
			t.Fatalf("cred %d: ExpiresAt should be set for ttl=7200", i)
		}
	}
}

func TestUpdateCred_SetTTL(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	cred := models.ProxyCredential{ProxyId: proxy.Id, Username: "u", Password: "p", Enabled: true}
	db.DB.Create(&cred)

	ttl := 1800
	body := map[string]any{"ttl": ttl}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/"+uid(cred.Id), body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.ProxyCredential
	db.DB.First(&updated, cred.Id)
	if updated.ExpiresAt == nil {
		t.Fatal("ExpiresAt should be set after TTL update")
	}
}

func TestUpdateCred_ClearTTL(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	cred := models.ProxyCredential{ProxyId: proxy.Id, Username: "u2", Password: "p2", Enabled: true}
	db.DB.Create(&cred)

	ttl := 3600
	body := map[string]any{"ttl": ttl}
	testutil.DoJSON(t, rk.R, "PUT", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/"+uid(cred.Id), body, rk.Tok)

	ttlZero := 0
	body2 := map[string]any{"ttl": ttlZero}
	w := testutil.DoJSON(t, rk.R, "PUT", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials/"+uid(cred.Id), body2, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var updated models.ProxyCredential
	db.DB.First(&updated, cred.Id)
	if updated.ExpiresAt != nil {
		t.Fatal("ExpiresAt should be nil after TTL=0")
	}
}

func TestListCreds(t *testing.T) {
	rk, proxy := setupCredRouter(t)
	db.DB.Create(&models.ProxyCredential{ProxyId: proxy.Id, Username: "a", Password: "b", Enabled: true})
	db.DB.Create(&models.ProxyCredential{ProxyId: proxy.Id, Username: "c", Password: "d", Enabled: false})

	w := testutil.Do(t, rk.R, "GET", "/api/v1/proxies/"+uid(proxy.Id)+"/credentials", rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var creds []models.ProxyCredential
	json.Unmarshal(resp.Data, &creds)
	if len(creds) != 2 {
		t.Fatalf("expected 2 creds, got %d", len(creds))
	}
}
