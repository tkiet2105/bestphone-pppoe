package api

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func setupClaimRouter(t *testing.T) *testutil.RouterKit {
	t.Helper()
	r := setupRouter(t)
	hub := events.NewHub()
	SetEventHub(hub)
	proxysrv.Init(db.DB, hub, 57000, 57100)
	tok := testutil.SeedToken(t)

	r.POST("/api/v1/claim", BearerAuth(), ClaimCredentials)
	r.POST("/api/v1/change", BearerAuth(), ChangeCredentials)
	r.GET("/api/v1/user-creds", BearerAuth(), ListUserCreds)
	r.POST("/api/v1/release", BearerAuth(), ReleaseCredentials)
	r.POST("/api/v1/extend", BearerAuth(), ExtendCredentials)
	r.GET("/api/v1/claim/status", BearerAuth(), ClaimStatus)
	r.GET("/api/v1/claim/user-status", BearerAuth(), ClaimUserStatus)

	return &testutil.RouterKit{R: r, Tok: tok}
}

func seedConnectedSessions(t *testing.T, count int) {
	t.Helper()
	line := testutil.SeedLine(t)
	for i := 0; i < count; i++ {
		sess := models.Session{
			LineId: line.Id, PppUnit: i, Username: "isp", Password: "pass",
			Status: models.StatusConnected, IP: "10.0.0." + uid(uint(i+1)),
		}
		db.DB.Create(&sess)
		proxy := models.Proxy{SessionId: sess.Id, Port: 57000 + i, Status: "running"}
		db.DB.Create(&proxy)
	}
}

type claimResponse struct {
	IUserId     string         `json:"iuser_id"`
	Credentials []credResponse `json:"credentials"`
}

func parseClaim(t *testing.T, w *testutil.RouterKit, resp testutil.APIResponse) claimResponse {
	t.Helper()
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	return cr
}

func TestClaim_Success(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	body := map[string]any{"iuser_id": "user-1", "count": 2, "ttl": 3600}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	if len(cr.Credentials) != 2 {
		t.Fatalf("expected 2 creds, got %d", len(cr.Credentials))
	}
	if cr.Credentials[0].SessionId == cr.Credentials[1].SessionId {
		t.Fatal("creds should be on different sessions")
	}
	if cr.Credentials[0].ExpiresAt == nil {
		t.Fatal("expires_at should be set for ttl=3600")
	}
}

func TestClaim_DuplicateReturnsExisting(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	body := map[string]any{"iuser_id": "user-dup", "count": 2, "ttl": 0}
	w1 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	resp1 := testutil.ParseResponse(t, w1)
	var cr1 claimResponse
	json.Unmarshal(resp1.Data, &cr1)

	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	resp2 := testutil.ParseResponse(t, w2)
	var cr2 claimResponse
	json.Unmarshal(resp2.Data, &cr2)

	if cr1.Credentials[0].CredId != cr2.Credentials[0].CredId {
		t.Fatal("second claim should return same creds")
	}
}

func TestClaim_ExceedsSessions(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 2)

	body := map[string]any{"iuser_id": "user-exceed", "count": 5, "ttl": 0}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestClaim_MissingParams(t *testing.T) {
	rk := setupClaimRouter(t)
	body := map[string]any{"count": 2}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChange_Success(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	claimBody := map[string]any{"iuser_id": "user-chg", "count": 2, "ttl": 7200}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)

	changeBody := map[string]any{
		"iuser_id": "user-chg",
		"cred_ids": []uint{cr.Credentials[0].CredId},
	}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/change", changeBody, rk.Tok)
	if w2.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	resp2 := testutil.ParseResponse(t, w2)
	var cr2 claimResponse
	json.Unmarshal(resp2.Data, &cr2)
	if len(cr2.Credentials) != 1 {
		t.Fatalf("expected 1 new cred, got %d", len(cr2.Credentials))
	}
	if cr2.Credentials[0].SessionId == cr.Credentials[0].SessionId {
		t.Fatal("new cred should be on a different session")
	}
	var oldCount int64
	db.DB.Model(&models.ProxyCredential{}).Where("id = ?", cr.Credentials[0].CredId).Count(&oldCount)
	if oldCount != 0 {
		t.Fatal("old cred should be deleted")
	}
}

func TestChange_WrongOwner(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	claimBody := map[string]any{"iuser_id": "owner-a", "count": 1, "ttl": 0}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)

	changeBody := map[string]any{
		"iuser_id": "owner-b",
		"cred_ids": []uint{cr.Credentials[0].CredId},
	}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/change", changeBody, rk.Tok)
	if w2.Code != 400 {
		t.Fatalf("expected 400, got %d", w2.Code)
	}
}

func TestListUserCreds(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 3)

	claimBody := map[string]any{"iuser_id": "user-list", "count": 2, "ttl": 0}
	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)

	w := testutil.Do(t, rk.R, "GET", "/api/v1/user-creds?iuser_id=user-list", rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	if len(cr.Credentials) != 2 {
		t.Fatalf("expected 2, got %d", len(cr.Credentials))
	}
}

func TestRelease_Success(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 3)

	claimBody := map[string]any{"iuser_id": "user-rel", "count": 2, "ttl": 0}
	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)

	releaseBody := map[string]any{"iuser_id": "user-rel"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/release", releaseBody, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var result map[string]any
	json.Unmarshal(resp.Data, &result)
	if result["released"].(float64) != 2 {
		t.Fatalf("expected 2 released, got %v", result["released"])
	}

	var count int64
	db.DB.Model(&models.ProxyCredential{}).Where("i_user_id = ?", "user-rel").Count(&count)
	if count != 0 {
		t.Fatal("all creds should be deleted")
	}
}

func TestExtend_Success(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 3)

	claimBody := map[string]any{"iuser_id": "user-ext", "count": 1, "ttl": 60}
	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)

	extBody := map[string]any{"iuser_id": "user-ext", "ttl": 7200}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/extend", extBody, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	if cr.Credentials[0].ExpiresAt == nil {
		t.Fatal("expires_at should be set")
	}
	remaining := time.Until(*cr.Credentials[0].ExpiresAt)
	if remaining < 7000*time.Second || remaining > 7200*time.Second {
		t.Fatalf("expected ~7200s remaining, got %v", remaining)
	}
}

func TestClaim_Concurrent(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 20)

	var wg sync.WaitGroup
	results := make([]claimResponse, 10)
	errors := make([]int, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := map[string]any{"iuser_id": fmt.Sprintf("conc-user-%d", idx), "count": 2, "ttl": 0}
			w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
			errors[idx] = w.Code
			if w.Code == 200 {
				resp := testutil.ParseResponse(t, w)
				json.Unmarshal(resp.Data, &results[idx])
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 10; i++ {
		if errors[i] != 200 {
			t.Fatalf("user %d got status %d", i, errors[i])
		}
		if len(results[i].Credentials) != 2 {
			t.Fatalf("user %d expected 2 creds, got %d", i, len(results[i].Credentials))
		}
		s1 := results[i].Credentials[0].SessionId
		s2 := results[i].Credentials[1].SessionId
		if s1 == s2 {
			t.Fatalf("user %d has 2 creds on same session %d", i, s1)
		}
	}
}

func TestClaim_SameSessionMultiUser(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 1)

	body1 := map[string]any{"iuser_id": "multi-a", "count": 1, "ttl": 0}
	w1 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body1, rk.Tok)
	if w1.Code != 200 {
		t.Fatalf("user-a: expected 200, got %d", w1.Code)
	}

	body2 := map[string]any{"iuser_id": "multi-b", "count": 1, "ttl": 0}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body2, rk.Tok)
	if w2.Code != 200 {
		t.Fatalf("user-b: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var cr1, cr2 claimResponse
	resp1 := testutil.ParseResponse(t, w1)
	json.Unmarshal(resp1.Data, &cr1)
	resp2 := testutil.ParseResponse(t, w2)
	json.Unmarshal(resp2.Data, &cr2)

	if cr1.Credentials[0].SessionId != cr2.Credentials[0].SessionId {
		t.Fatal("both users should be on same session (only 1 session available)")
	}
	if cr1.Credentials[0].CredId == cr2.Credentials[0].CredId {
		t.Fatal("different users should have different creds")
	}
}

func TestClaimStatus(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim",
		map[string]any{"iuser_id": "stat-a", "count": 2, "ttl": 0}, rk.Tok)
	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim",
		map[string]any{"iuser_id": "stat-b", "count": 1, "ttl": 0}, rk.Tok)

	w := testutil.Do(t, rk.R, "GET", "/api/v1/claim/status", rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	var status map[string]float64
	json.Unmarshal(resp.Data, &status)
	if status["total_connected_sessions"] != 5 {
		t.Errorf("expected 5 sessions, got %v", status["total_connected_sessions"])
	}
	if status["active_users"] != 2 {
		t.Errorf("expected 2 users, got %v", status["active_users"])
	}
	if status["total_claimed_creds"] != 3 {
		t.Errorf("expected 3 creds, got %v", status["total_claimed_creds"])
	}
}

func TestClaimUserStatus(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim",
		map[string]any{"iuser_id": "ustat-x", "count": 3, "ttl": 0}, rk.Tok)

	w := testutil.Do(t, rk.R, "GET", "/api/v1/claim/user-status?iuser_id=ustat-x", rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	var result struct {
		IUserId     string         `json:"iuser_id"`
		ActiveCreds int            `json:"active_creds"`
		Credentials []credResponse `json:"credentials"`
	}
	json.Unmarshal(resp.Data, &result)
	if result.ActiveCreds != 3 {
		t.Errorf("expected 3, got %d", result.ActiveCreds)
	}
	if len(result.Credentials) != 3 {
		t.Errorf("expected 3 creds, got %d", len(result.Credentials))
	}
}
