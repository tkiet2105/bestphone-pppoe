package api

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

// ListActivity — query activity_logs với filter + pagination.
//
// Query params:
//
//	level=info|warn|error
//	category=dial|rotate|reconnect|watchdog|claim|cred|session|line|auth|proxy
//	session_id=NN
//	line_id=NN
//	iuser_id=NAME
//	since=2026-05-28T10:00:00Z   (RFC3339)
//	until=2026-05-28T11:00:00Z
//	limit=200  (max 1000)
//	offset=0
//	q=keyword   (LIKE trên summary)
//
// Response: {items: [...], total: NN}
func ListActivity(c *gin.Context) {
	q := db.DB.Model(&models.ActivityLog{})
	if v := c.Query("level"); v != "" {
		q = q.Where("level = ?", v)
	}
	if v := c.Query("category"); v != "" {
		q = q.Where("category = ?", v)
	}
	if v := c.Query("session_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("session_id = ?", id)
		}
	}
	if v := c.Query("line_id"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			q = q.Where("line_id = ?", id)
		}
	}
	if v := c.Query("iuser_id"); v != "" {
		q = q.Where("i_user_id = ?", v)
	}
	if v := c.Query("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if v := c.Query("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("created_at <= ?", t)
		}
	}
	if v := c.Query("q"); v != "" {
		q = q.Where("summary LIKE ?", "%"+v+"%")
	}

	var total int64
	q.Count(&total)

	limit := 200
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	offset := 0
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	var items []models.ActivityLog
	q.Order("id DESC").Limit(limit).Offset(offset).Find(&items)
	ok(c, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// GetSessionActivity — shortcut: lấy activity gần đây của 1 session (default 50).
func GetSessionActivity(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		fail(c, 400, "session id không hợp lệ")
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	var items []models.ActivityLog
	db.DB.Where("session_id = ?", id).Order("id DESC").Limit(limit).Find(&items)
	ok(c, gin.H{"session_id": id, "items": items})
}
