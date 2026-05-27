package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1)
	g.AutoMigrate(&models.Setting{})
	DB = g
	t.Cleanup(func() { sqlDB.Close() })
}

func TestGetSetting_NotFound(t *testing.T) {
	setupTestDB(t)
	if v := GetSetting("nonexistent"); v != "" {
		t.Errorf("expected empty, got %q", v)
	}
}

func TestSetAndGetSetting(t *testing.T) {
	setupTestDB(t)
	if err := SetSetting("foo", "bar"); err != nil {
		t.Fatal(err)
	}
	if v := GetSetting("foo"); v != "bar" {
		t.Errorf("expected bar, got %q", v)
	}
}

func TestSetSetting_Overwrite(t *testing.T) {
	setupTestDB(t)
	SetSetting("key", "v1")
	SetSetting("key", "v2")
	if v := GetSetting("key"); v != "v2" {
		t.Errorf("expected v2, got %q", v)
	}
}

func TestGetSettingInt(t *testing.T) {
	setupTestDB(t)
	if v := GetSettingInt("missing", 42); v != 42 {
		t.Errorf("expected default 42, got %d", v)
	}
	SetSetting("num", "10")
	if v := GetSettingInt("num", 0); v != 10 {
		t.Errorf("expected 10, got %d", v)
	}
	SetSetting("bad", "abc")
	if v := GetSettingInt("bad", 99); v != 99 {
		t.Errorf("expected default 99 for non-numeric, got %d", v)
	}
}

func TestGetSettingBool(t *testing.T) {
	setupTestDB(t)
	if v := GetSettingBool("missing", true); !v {
		t.Error("expected default true")
	}
	SetSetting("on", "true")
	if v := GetSettingBool("on", false); !v {
		t.Error("expected true")
	}
	SetSetting("off", "false")
	if v := GetSettingBool("off", true); v {
		t.Error("expected false")
	}
	SetSetting("one", "1")
	if v := GetSettingBool("one", false); !v {
		t.Error("expected true for '1'")
	}
}

func TestAllSettings(t *testing.T) {
	setupTestDB(t)
	SetSetting("a", "1")
	SetSetting("b", "2")
	m := AllSettings()
	if len(m) != 2 {
		t.Errorf("expected 2, got %d", len(m))
	}
	if m["a"] != "1" || m["b"] != "2" {
		t.Errorf("unexpected values: %v", m)
	}
}

func TestSeedDefaultSettings(t *testing.T) {
	setupTestDB(t)
	SeedDefaultSettings()
	if v := GetSetting("reconnect_enabled"); v != "true" {
		t.Errorf("expected true, got %q", v)
	}
	if v := GetSetting("reconnect_max_retries"); v != "1" {
		t.Errorf("expected 1, got %q", v)
	}
	if v := GetSetting("reconnect_pause_minutes"); v != "60" {
		t.Errorf("expected 60, got %q", v)
	}
	SetSetting("reconnect_max_retries", "10")
	SeedDefaultSettings()
	if v := GetSetting("reconnect_max_retries"); v != "10" {
		t.Errorf("seed should not overwrite, expected 10 got %q", v)
	}
}
