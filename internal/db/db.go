// Package db — SQLite (modernc — pure Go, CGO=0) + gorm wrap.
package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

var DB *gorm.DB

func Init(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("mkdir db dir: %w", err)
	}
	g, err := gorm.Open(sqlite.Open(dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB, _ := g.DB()
	sqlDB.SetMaxOpenConns(1) // SQLite không thích nhiều writer
	DB = g
	if err := DB.AutoMigrate(&models.Line{}, &models.Session{}, &models.Proxy{}, &models.ProxyCredential{}, &models.Token{}, &models.AccessRule{}); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}
	return nil
}

// SeedAdminToken — nếu DB chưa có token nào, insert token từ env ADMIN_TOKEN.
func SeedAdminToken(adminToken string) error {
	if adminToken == "" {
		return nil
	}
	var count int64
	DB.Model(&models.Token{}).Count(&count)
	if count > 0 {
		return nil
	}
	t := models.Token{Token: adminToken, Label: "admin-default", CreatedAt: time.Now()}
	if err := DB.Create(&t).Error; err != nil {
		return err
	}
	log.Printf("[db] seeded admin-default token (id=%d)", t.Id)
	return nil
}
