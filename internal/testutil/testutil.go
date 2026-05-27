package testutil

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func SetupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := g.AutoMigrate(
		&models.Line{}, &models.Session{}, &models.Proxy{},
		&models.ProxyCredential{}, &models.Token{}, &models.AccessRule{},
		&models.User{}, &models.AuditLog{}, &models.Setting{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	db.DB = g
	t.Cleanup(func() {
		sqlDB.Close()
	})
	return g
}

func SetupTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	SetupTestDB(t)
	r := gin.New()
	return r
}

func SeedLine(t *testing.T) models.Line {
	t.Helper()
	line := models.Line{Name: "test-line", Iface: "eth0", Username: "isp_user", Password: "isp_pass", UseMacvlan: false, MaxSessions: 8}
	if err := db.DB.Create(&line).Error; err != nil {
		t.Fatalf("seed line: %v", err)
	}
	return line
}

func SeedSession(t *testing.T, lineId uint) models.Session {
	t.Helper()
	sess := models.Session{LineId: lineId, PppUnit: 0, Username: "isp_user", Password: "isp_pass", Status: "connected"}
	if err := db.DB.Create(&sess).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
}

func SeedProxy(t *testing.T, sessionId uint) models.Proxy {
	t.Helper()
	proxy := models.Proxy{SessionId: sessionId, Port: 30001, Status: "running"}
	if err := db.DB.Create(&proxy).Error; err != nil {
		t.Fatalf("seed proxy: %v", err)
	}
	return proxy
}

func SeedUser(t *testing.T, username, passwordHash string) models.User {
	t.Helper()
	u := models.User{Username: username, PasswordHash: passwordHash, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.DB.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func SeedSessionToken(t *testing.T, userId uint) string {
	t.Helper()
	tok := "sess-token-" + time.Now().Format("150405.000000")
	tkn := models.Token{Token: tok, Label: "session:test", UserId: &userId, CreatedAt: time.Now()}
	if err := db.DB.Create(&tkn).Error; err != nil {
		t.Fatalf("seed session token: %v", err)
	}
	return tok
}

func SeedToken(t *testing.T) string {
	t.Helper()
	tok := "test-token-" + time.Now().Format("150405.000")
	tkn := models.Token{Token: tok, Label: "test", CreatedAt: time.Now()}
	if err := db.DB.Create(&tkn).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok
}

func DoJSON(t *testing.T, r *gin.Engine, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type APIResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func ParseResponse(t *testing.T, w *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

func ParseData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	resp := ParseResponse(t, w)
	var data T
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("parse data: %v (raw: %s)", err, string(resp.Data))
	}
	return data
}

func Do(t *testing.T, r *gin.Engine, method, path string, token string) *httptest.ResponseRecorder {
	t.Helper()
	return DoJSON(t, r, method, path, nil, token)
}

type RouterKit struct {
	R   *gin.Engine
	Tok string
}
