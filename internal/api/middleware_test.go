package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

func setupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	return testutil.SetupTestRouter(t)
}

func TestBearerAuth_ValidToken(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/test", BearerAuth(), func(c *gin.Context) {
		id, _ := c.Get("token_id")
		ok(c, gin.H{"token_id": id})
	})
	w := testutil.Do(t, r, "GET", "/test", tok)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := testutil.ParseResponse(t, w)
	if !resp.Success {
		t.Fatal("expected success")
	}
}

func TestBearerAuth_MissingHeader(t *testing.T) {
	r := setupRouter(t)
	r.GET("/test", BearerAuth(), func(c *gin.Context) { ok(c, nil) })
	w := testutil.Do(t, r, "GET", "/test", "")
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBearerAuth_InvalidToken(t *testing.T) {
	r := setupRouter(t)
	r.GET("/test", BearerAuth(), func(c *gin.Context) { ok(c, nil) })
	w := testutil.Do(t, r, "GET", "/test", "nonexistent-token")
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestBearerAuth_QueryParam(t *testing.T) {
	r := setupRouter(t)
	tok := testutil.SeedToken(t)
	r.GET("/test", BearerAuth(), func(c *gin.Context) { ok(c, gin.H{"ok": true}) })
	req := httptest.NewRequest("GET", "/test?token="+tok, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with query param, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOkHelper(t *testing.T) {
	r := setupRouter(t)
	r.GET("/test", func(c *gin.Context) { ok(c, gin.H{"msg": "hello"}) })
	w := testutil.Do(t, r, "GET", "/test", "")
	resp := testutil.ParseResponse(t, w)
	if !resp.Success {
		t.Fatal("ok() should set success=true")
	}
}

func TestFailHelper(t *testing.T) {
	r := setupRouter(t)
	r.GET("/test", func(c *gin.Context) { fail(c, 400, "bad") })
	w := testutil.Do(t, r, "GET", "/test", "")
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := testutil.ParseResponse(t, w)
	if resp.Success {
		t.Fatal("fail() should set success=false")
	}
	if resp.Error != "bad" {
		t.Fatalf("expected error 'bad', got %q", resp.Error)
	}
}

func TestCORS_Options(t *testing.T) {
	r := setupRouter(t)
	r.Use(CORS())
	r.GET("/test", func(c *gin.Context) { ok(c, nil) })
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("CORS origin header not set correctly")
	}
}
