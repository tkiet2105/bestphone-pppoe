// Package activity — diagnostic event logger persist vào DB.
//
// Cách dùng:
//
//	activity.Info("dial", "start", "Bắt đầu quay số session #5",
//	    activity.SessionId(5), activity.LineId(1))
//	activity.Error("dial", "auth_nak", "ISP từ chối tên đăng nhập",
//	    activity.SessionId(5), activity.F("isp_user", "fbr00chgt"))
//
// Helper KHÔNG block caller — DB insert chạy sync nhưng cheap (~1ms),
// nếu DB nil hoặc lỗi thì silently log ra stderr (không fail caller).
package activity

import (
	"encoding/json"
	"log"
	"time"

	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

const (
	CategoryDial      = "dial"
	CategoryRotate    = "rotate"
	CategoryReconnect = "reconnect"
	CategoryWatchdog  = "watchdog"
	CategoryClaim     = "claim"
	CategoryCred      = "cred"
	CategorySession   = "session"
	CategoryLine      = "line"
	CategoryAuth      = "auth"
	CategoryProxy     = "proxy"
)

var (
	dbRef *gorm.DB
	hub   *events.Hub
)

// Init — gọi 1 lần lúc bootstrap.
func Init(db *gorm.DB, h *events.Hub) {
	dbRef = db
	hub = h
}

// Field — key/value để gắn vào ActivityLog. Một số key đặc biệt
// ("session_id", "line_id", "proxy_id", "cred_id", "user_id", "iuser_id",
// "client_ip") được pull vào column riêng để index/query nhanh, phần còn lại
// gộp vào JSON Details.
type Field struct {
	Key string
	Val any
}

func F(key string, val any) Field { return Field{key, val} }

func SessionId(id uint) Field     { return Field{"session_id", id} }
func LineId(id uint) Field        { return Field{"line_id", id} }
func ProxyId(id uint) Field       { return Field{"proxy_id", id} }
func CredId(id uint) Field        { return Field{"cred_id", id} }
func UserId(id uint) Field        { return Field{"user_id", id} }
func IUserId(s string) Field      { return Field{"iuser_id", s} }
func ClientIP(s string) Field     { return Field{"client_ip", s} }

func Info(category, action, summary string, fields ...Field) {
	write(LevelInfo, category, action, summary, fields)
}
func Warn(category, action, summary string, fields ...Field) {
	write(LevelWarn, category, action, summary, fields)
}
func Error(category, action, summary string, fields ...Field) {
	write(LevelError, category, action, summary, fields)
}

func write(level, category, action, summary string, fields []Field) {
	entry := models.ActivityLog{
		CreatedAt: time.Now(),
		Level:     level,
		Category:  category,
		Action:    action,
		Summary:   summary,
	}
	extra := map[string]any{}
	for _, f := range fields {
		switch f.Key {
		case "session_id":
			if v, ok := f.Val.(uint); ok && v > 0 {
				entry.SessionId = &v
			}
		case "line_id":
			if v, ok := f.Val.(uint); ok && v > 0 {
				entry.LineId = &v
			}
		case "proxy_id":
			if v, ok := f.Val.(uint); ok && v > 0 {
				entry.ProxyId = &v
			}
		case "cred_id":
			if v, ok := f.Val.(uint); ok && v > 0 {
				entry.CredId = &v
			}
		case "user_id":
			if v, ok := f.Val.(uint); ok && v > 0 {
				entry.UserId = &v
			}
		case "iuser_id":
			if v, ok := f.Val.(string); ok {
				entry.IUserId = v
			}
		case "client_ip":
			if v, ok := f.Val.(string); ok {
				entry.ClientIP = v
			}
		default:
			extra[f.Key] = f.Val
		}
	}
	if len(extra) > 0 {
		if b, err := json.Marshal(extra); err == nil {
			entry.Details = string(b)
		}
	}
	if dbRef != nil {
		if err := dbRef.Create(&entry).Error; err != nil {
			log.Printf("[activity] DB insert failed: %v (entry=%+v)", err, entry)
			return
		}
	}
	if hub != nil {
		hub.Publish("activity.log", entry)
	}
}

// CleanupOld — chạy định kỳ, xóa entry > maxAge.
func CleanupOld(maxAge time.Duration) {
	if dbRef == nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	res := dbRef.Where("created_at < ?", cutoff).Delete(&models.ActivityLog{})
	if res.Error != nil {
		log.Printf("[activity] cleanup failed: %v", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("[activity] cleanup: deleted %d old entries (>%s)", res.RowsAffected, maxAge)
	}
}
