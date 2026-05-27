package api

import (
	"encoding/json"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func seedTestUser(t *testing.T, password string) models.User {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	return testutil.SeedUser(t, "admin", string(hash))
}

func TestAuthLogin_Success(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/login", AuthLogin)
	seedTestUser(t, "secret123")

	body := map[string]any{"username": "admin", "password": "secret123"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/login", body, "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var data map[string]any
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &data)
	if data["token"] == nil || data["token"] == "" {
		t.Fatal("expected token in response")
	}
	if data["username"] != "admin" {
		t.Fatalf("expected admin, got %v", data["username"])
	}
}

func TestAuthLogin_WrongPassword(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/login", AuthLogin)
	seedTestUser(t, "secret123")

	body := map[string]any{"username": "admin", "password": "wrong"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/login", body, "")
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthLogin_NonexistentUser(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/login", AuthLogin)
	seedTestUser(t, "secret123")

	body := map[string]any{"username": "nobody", "password": "secret123"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/login", body, "")
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthLogout_Success(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/logout", BearerAuth(), AuthLogout)
	u := seedTestUser(t, "pass")
	tok := testutil.SeedSessionToken(t, u.Id)

	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/logout", nil, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var count int64
	db.DB.Model(&models.Token{}).Where("token = ?", tok).Count(&count)
	if count != 0 {
		t.Fatal("session token should be deleted after logout")
	}
}

func TestAuthLogout_APIToken(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/logout", BearerAuth(), AuthLogout)
	tok := testutil.SeedToken(t) // API token (no user_id)

	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/logout", nil, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 for API token logout, got %d", w.Code)
	}
}

func TestChangePassword_Success(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/change-password", BearerAuth(), ChangePassword)
	u := seedTestUser(t, "oldpass")
	tok := testutil.SeedSessionToken(t, u.Id)

	body := map[string]any{"current_password": "oldpass", "new_password": "newpass123"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/change-password", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated models.User
	db.DB.First(&updated, u.Id)
	if err := bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("newpass123")); err != nil {
		t.Fatal("password should be updated")
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/change-password", BearerAuth(), ChangePassword)
	u := seedTestUser(t, "correct")
	tok := testutil.SeedSessionToken(t, u.Id)

	body := map[string]any{"current_password": "wrong", "new_password": "newpass123"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/change-password", body, tok)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestChangePassword_TooShort(t *testing.T) {
	r := setupRouter(t)
	r.POST("/api/v1/auth/change-password", BearerAuth(), ChangePassword)
	u := seedTestUser(t, "correct")
	tok := testutil.SeedSessionToken(t, u.Id)

	body := map[string]any{"current_password": "correct", "new_password": "ab"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/auth/change-password", body, tok)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateToken_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.POST("/api/v1/tokens", BearerAuth(), CreateToken)

	body := map[string]any{"label": "my-token"}
	w := testutil.DoJSON(t, r, "POST", "/api/v1/tokens", body, tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var data map[string]any
	resp := testutil.ParseResponse(t, w)
	json.Unmarshal(resp.Data, &data)
	if data["token"] == nil || data["token"] == "" {
		t.Fatal("expected token in response")
	}
}

func TestDeleteToken_Success(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.DELETE("/api/v1/tokens/:id", BearerAuth(), DeleteToken)

	// Create a second API token to delete
	t2 := models.Token{Token: "delete-me", Label: "to-delete"}
	db.DB.Create(&t2)

	w := testutil.Do(t, r, "DELETE", "/api/v1/tokens/"+uid(t2.Id), tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteToken_SessionToken(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.DELETE("/api/v1/tokens/:id", BearerAuth(), DeleteToken)

	u := seedTestUser(t, "pass")
	uid2 := u.Id
	st := models.Token{Token: "session-tok", Label: "session:admin", UserId: &uid2}
	db.DB.Create(&st)

	w := testutil.Do(t, r, "DELETE", "/api/v1/tokens/"+uid(st.Id), tok)
	if w.Code != 400 {
		t.Fatalf("expected 400 for session token deletion, got %d", w.Code)
	}
}

func TestListTokens_OnlyAPITokens(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/api/v1/tokens", BearerAuth(), ListTokens)

	u := seedTestUser(t, "pass")
	testutil.SeedSessionToken(t, u.Id)

	w := testutil.Do(t, r, "GET", "/api/v1/tokens", tok)
	resp := testutil.ParseResponse(t, w)
	var tokens []map[string]any
	json.Unmarshal(resp.Data, &tokens)
	for _, t2 := range tokens {
		if t2["user_id"] != nil {
			t.Fatal("session tokens should not appear in list")
		}
	}
}
