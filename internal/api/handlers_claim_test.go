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

// TestClaim_Private_IgnoresDefaultSeed — regression cho bug: private session (max=1)
// luôn có 1 default seed cred (iuser_id="") → trước v1.8.7, slot count = 1 → mọi
// claim đều fail "không đủ" dù chưa có iuser nào claim. Sau fix: default cred
// không count vào slot quota.
func TestClaim_Private_IgnoresDefaultSeed(t *testing.T) {
	rk := setupClaimRouter(t)
	line := testutil.SeedLine(t)
	// 3 private sessions, mỗi session 1 default seed cred (iuser_id="")
	for i := 0; i < 3; i++ {
		sess := models.Session{
			LineId: line.Id, PppUnit: 900 + i, Username: "u", Password: "p",
			Type: models.SessionTypePrivate, Status: models.StatusConnected,
		}
		db.DB.Create(&sess)
		p := models.Proxy{SessionId: sess.Id, Port: 58900 + i, Status: "running"}
		db.DB.Create(&p)
		db.DB.Create(&models.ProxyCredential{
			ProxyId: p.Id, Label: "default", Username: "uadmin", Password: "p", Enabled: true,
			// IUserId="" — đây là seed default, không phải claim
		})
	}

	// Claim 2 private cred — phải success dù 3 session đều đã có 1 default cred.
	body := map[string]any{"iuser_id": "customer-x", "type": "private", "count": 2, "ttl": 0}
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

	// Tiếp tục: claim thêm cho customer-y → phải success (vẫn còn 1 private trống)
	body2 := map[string]any{"iuser_id": "customer-y", "type": "private", "count": 1, "ttl": 0}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body2, rk.Tok)
	if w2.Code != 200 {
		t.Fatalf("y claim: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	// Claim thêm customer-z → phải fail (cả 3 private đã có iuser claim)
	body3 := map[string]any{"iuser_id": "customer-z", "type": "private", "count": 1, "ttl": 0}
	w3 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body3, rk.Tok)
	if w3.Code != 400 {
		t.Fatalf("z claim: expected 400 (exhausted), got %d: %s", w3.Code, w3.Body.String())
	}
}

// TestChange_NotPingPong — regression cho bug: change cred A → cấp B → change B → cấp A
// → lặp vô tận giữa 2 proxy. Sau fix shuffle, qua 10 lần change phải đi qua ít nhất 3
// session khác nhau (random rải đều trong pool 5 session).
func TestChange_NotPingPong(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 5)

	claimBody := map[string]any{"iuser_id": "user-pp", "count": 1, "ttl": 0}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", claimBody, rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	currentCred := cr.Credentials[0].CredId
	seenSessions := map[uint]bool{cr.Credentials[0].SessionId: true}

	for i := 0; i < 10; i++ {
		changeBody := map[string]any{
			"iuser_id": "user-pp",
			"cred_ids": []uint{currentCred},
		}
		w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/change", changeBody, rk.Tok)
		if w2.Code != 200 {
			t.Fatalf("change %d: status %d: %s", i, w2.Code, w2.Body.String())
		}
		resp2 := testutil.ParseResponse(t, w2)
		var cr2 claimResponse
		json.Unmarshal(resp2.Data, &cr2)
		if len(cr2.Credentials) != 1 {
			t.Fatalf("change %d: expected 1 cred, got %d", i, len(cr2.Credentials))
		}
		currentCred = cr2.Credentials[0].CredId
		seenSessions[cr2.Credentials[0].SessionId] = true
	}
	if len(seenSessions) < 3 {
		t.Errorf("ping-pong detected: 11 change calls chỉ qua %d session (kỳ vọng >=3 trong pool 5)", len(seenSessions))
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

// ---------- Type-based tests ----------

func seedSessionsWithType(t *testing.T, count int, sessType string, startUnit, startPort int) {
	t.Helper()
	line := testutil.SeedLine(t)
	for i := 0; i < count; i++ {
		sess := models.Session{
			LineId: line.Id, PppUnit: startUnit + i, Username: "isp", Password: "pass",
			Type:   sessType,
			Status: models.StatusConnected, IP: "10.0.0." + uid(uint(i+1)),
		}
		db.DB.Create(&sess)
		proxy := models.Proxy{SessionId: sess.Id, Port: startPort + i, Status: "running"}
		db.DB.Create(&proxy)
	}
}

func TestClaim_TypeFilter(t *testing.T) {
	rk := setupClaimRouter(t)
	seedSessionsWithType(t, 2, models.SessionTypeStatic, 100, 58000)
	seedSessionsWithType(t, 3, models.SessionTypeRotating, 200, 58100)

	body := map[string]any{"iuser_id": "u-type", "count": 2, "type": "static"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	for _, c := range cr.Credentials {
		if c.Type != "static" {
			t.Errorf("expected static, got %s", c.Type)
		}
	}
}

func TestClaim_InvalidType(t *testing.T) {
	rk := setupClaimRouter(t)
	seedConnectedSessions(t, 2)
	body := map[string]any{"iuser_id": "u-bad", "count": 1, "type": "invalid"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestClaim_PrivateMax1User(t *testing.T) {
	rk := setupClaimRouter(t)
	seedSessionsWithType(t, 2, models.SessionTypePrivate, 300, 58200)

	// User 1 claim 2 private sessions → ok
	body := map[string]any{"iuser_id": "u-priv-1", "count": 2, "type": "private"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 200 {
		t.Fatalf("user1 expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// User 2 cũng đòi 1 cred private → fail vì cả 2 session private đã hết slot (max=1).
	body2 := map[string]any{"iuser_id": "u-priv-2", "count": 1, "type": "private"}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body2, rk.Tok)
	if w2.Code != 400 {
		t.Fatalf("user2 expected 400 (private slots exhausted), got %d", w2.Code)
	}
}

func TestClaim_StaticMax5Users(t *testing.T) {
	rk := setupClaimRouter(t)
	seedSessionsWithType(t, 1, models.SessionTypeStatic, 400, 58300)

	// 5 user khác nhau, mỗi user claim 1 cred type=static → tất cả share cùng 1 session.
	for i := 1; i <= 5; i++ {
		body := map[string]any{"iuser_id": fmt.Sprintf("u-s-%d", i), "count": 1, "type": "static"}
		w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
		if w.Code != 200 {
			t.Fatalf("user %d expected 200, got %d: %s", i, w.Code, w.Body.String())
		}
	}

	// User 6 → fail
	body := map[string]any{"iuser_id": "u-s-6", "count": 1, "type": "static"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	if w.Code != 400 {
		t.Fatalf("user 6 expected 400 (static slots exhausted at 5), got %d", w.Code)
	}
}

func TestChange_PreservesType(t *testing.T) {
	rk := setupClaimRouter(t)
	seedSessionsWithType(t, 3, models.SessionTypeStatic, 500, 58400)

	// Claim 1 static
	body := map[string]any{"iuser_id": "u-chg-type", "count": 1, "type": "static"}
	w := testutil.DoJSON(t, rk.R, "POST", "/api/v1/claim", body, rk.Tok)
	resp := testutil.ParseResponse(t, w)
	var cr claimResponse
	json.Unmarshal(resp.Data, &cr)
	if len(cr.Credentials) != 1 {
		t.Fatalf("expected 1 claimed, got %d", len(cr.Credentials))
	}

	// Change → cred mới phải vẫn type=static
	chBody := map[string]any{
		"iuser_id": "u-chg-type",
		"cred_ids": []uint{cr.Credentials[0].CredId},
	}
	w2 := testutil.DoJSON(t, rk.R, "POST", "/api/v1/change", chBody, rk.Tok)
	if w2.Code != 200 {
		t.Fatalf("change expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	resp2 := testutil.ParseResponse(t, w2)
	var cr2 claimResponse
	json.Unmarshal(resp2.Data, &cr2)
	if cr2.Credentials[0].Type != "static" {
		t.Errorf("change should preserve type, got %s", cr2.Credentials[0].Type)
	}
}
