package activity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := g.AutoMigrate(&models.ActivityLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return g
}

func TestLog_Levels(t *testing.T) {
	g := setupTestDB(t)
	Init(g, nil)
	t.Cleanup(func() { Init(nil, nil) })

	Info("dial", "start", "session 1 dialing", SessionId(1), LineId(2), F("iface", "eth0"))
	Warn("rotate", "same_ip", "IP unchanged", SessionId(1), F("old_ip", "1.2.3.4"))
	Error("dial", "pppd_fail", "pppd dies", SessionId(1), F("reason", "timeout"))

	var entries []models.ActivityLog
	g.Order("id ASC").Find(&entries)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Level != LevelInfo || entries[0].Category != "dial" || entries[0].Action != "start" {
		t.Errorf("entry 0 mismatch: %+v", entries[0])
	}
	if entries[0].SessionId == nil || *entries[0].SessionId != 1 {
		t.Errorf("entry 0 SessionId expected 1, got %v", entries[0].SessionId)
	}
	if entries[0].LineId == nil || *entries[0].LineId != 2 {
		t.Errorf("entry 0 LineId expected 2, got %v", entries[0].LineId)
	}
	if entries[1].Level != LevelWarn || entries[2].Level != LevelError {
		t.Errorf("levels mismatch")
	}

	// Check details JSON
	var detail map[string]any
	if err := json.Unmarshal([]byte(entries[0].Details), &detail); err != nil {
		t.Fatalf("parse details: %v", err)
	}
	if detail["iface"] != "eth0" {
		t.Errorf("details.iface expected eth0, got %v", detail["iface"])
	}
}

func TestLog_NilDB_Safe(t *testing.T) {
	Init(nil, nil)
	// Phải không panic dù DB nil
	Info("dial", "start", "no db")
}

func TestSpecialFields(t *testing.T) {
	g := setupTestDB(t)
	Init(g, nil)
	t.Cleanup(func() { Init(nil, nil) })

	Info("claim", "ok", "claimed 5", IUserId("user_abc"), ClientIP("10.0.0.1"),
		CredId(7), ProxyId(3), UserId(99))
	var e models.ActivityLog
	if err := g.First(&e).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if e.IUserId != "user_abc" {
		t.Errorf("IUserId expected user_abc, got %q", e.IUserId)
	}
	if e.ClientIP != "10.0.0.1" {
		t.Errorf("ClientIP expected 10.0.0.1, got %q", e.ClientIP)
	}
	if e.CredId == nil || *e.CredId != 7 {
		t.Errorf("CredId expected 7, got %v", e.CredId)
	}
	// "session_id" not provided → nil
	if e.SessionId != nil {
		t.Errorf("SessionId expected nil, got %v", *e.SessionId)
	}
}

func TestCleanupOld(t *testing.T) {
	g := setupTestDB(t)
	Init(g, nil)
	t.Cleanup(func() { Init(nil, nil) })

	now := time.Now()
	// Insert 1 old + 1 fresh
	g.Create(&models.ActivityLog{CreatedAt: now.Add(-40 * 24 * time.Hour), Level: "info", Category: "dial", Summary: "old"})
	g.Create(&models.ActivityLog{CreatedAt: now.Add(-1 * time.Hour), Level: "info", Category: "dial", Summary: "fresh"})

	CleanupOld(30 * 24 * time.Hour)

	var entries []models.ActivityLog
	g.Find(&entries)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after cleanup, got %d", len(entries))
	}
	if entries[0].Summary != "fresh" {
		t.Errorf("expected fresh entry to survive, got %q", entries[0].Summary)
	}
}
