// Package db — SQLite (modernc — pure Go, CGO=0) + gorm wrap.
package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
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
	if err := DB.AutoMigrate(&models.Line{}, &models.Session{}, &models.Proxy{}, &models.ProxyCredential{}, &models.Token{}, &models.AccessRule{}, &models.User{}, &models.AuditLog{}, &models.Setting{}); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}
	// Backfill 1-shot: nếu có column legacy isp_username/isp_password (từ v1.2.x),
	// copy giá trị sang username/password. Idempotent — chỉ chạy khi username rỗng.
	hasLegacy := DB.Migrator().HasColumn(&models.Line{}, "isp_username")
	if hasLegacy {
		DB.Exec("UPDATE lines SET username = COALESCE(NULLIF(username,''), isp_username), password = COALESCE(NULLIF(password,''), isp_password) WHERE (isp_username <> '' OR isp_password <> '')")
		log.Printf("[db] backfilled lines.username/password from legacy isp_* columns")
		_ = DB.Migrator().DropColumn(&models.Line{}, "isp_username")
		_ = DB.Migrator().DropColumn(&models.Line{}, "isp_password")
	}
	return nil
}

func SeedDefaultSettings() {
	defaults := map[string]string{
		"reconnect_enabled":       "true",
		"reconnect_max_retries":   "1",
		"reconnect_pause_minutes": "60",
	}
	for k, v := range defaults {
		var count int64
		DB.Model(&models.Setting{}).Where("key = ?", k).Count(&count)
		if count == 0 {
			DB.Create(&models.Setting{Key: k, Value: v})
		}
	}
}

// SeedAdminToken — nếu DB chưa có API token nào, insert token từ env ADMIN_TOKEN.
// Chỉ đếm token user_id IS NULL (API token) để không nhầm với session đăng nhập.
func SeedAdminToken(adminToken string) error {
	if adminToken == "" {
		return nil
	}
	var count int64
	DB.Model(&models.Token{}).Where("user_id IS NULL").Count(&count)
	if count > 0 {
		return nil
	}
	t := models.Token{Token: adminToken, Label: "admin-default", CreatedAt: time.Now()}
	if err := DB.Create(&t).Error; err != nil {
		return err
	}
	log.Printf("[db] seeded admin-default API token (id=%d)", t.Id)
	return nil
}

// SeedAdminUser — nếu DB chưa có user nào, tạo admin mặc định.
// Username/password lấy từ env ADMIN_USERNAME / ADMIN_PASSWORD; nếu env trống dùng
// "admin" / "bestphone" — log cảnh báo và khuyến khích đổi pass ngay.
func SeedAdminUser(envUser, envPass string) error {
	var count int64
	DB.Model(&models.User{}).Count(&count)
	if count > 0 {
		return nil
	}
	username := envUser
	if username == "" {
		username = "admin"
	}
	password := envPass
	usedDefault := false
	if password == "" {
		password = "bestphone"
		usedDefault = true
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("bcrypt: %w", err)
	}
	u := models.User{
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := DB.Create(&u).Error; err != nil {
		return err
	}
	if usedDefault {
		log.Printf("[db] ⚠ seeded admin user %q / %q — ĐỔI MẬT KHẨU NGAY tại trang Cài đặt", username, password)
	} else {
		log.Printf("[db] seeded admin user %q from env (id=%d)", username, u.Id)
	}
	return nil
}
