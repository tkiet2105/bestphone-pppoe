package audit

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

var hub *events.Hub

func SetHub(h *events.Hub) { hub = h }

func LogFromContext(c *gin.Context, action, resourceType string, resourceId uint, oldVal, newVal any, summary string) {
	tokenId, userId := fromContext(c)
	entry := models.AuditLog{
		TokenId:      tokenId,
		UserId:       userId,
		ClientIP:     c.ClientIP(),
		Action:       action,
		ResourceType: resourceType,
		ResourceId:   resourceId,
		Summary:      summary,
		CreatedAt:    time.Now(),
	}
	if oldVal != nil {
		if b, err := json.Marshal(oldVal); err == nil {
			s := string(b)
			entry.OldValue = &s
		}
	}
	if newVal != nil {
		if b, err := json.Marshal(newVal); err == nil {
			s := string(b)
			entry.NewValue = &s
		}
	}
	go write(entry)
}

func fromContext(c *gin.Context) (uint, *uint) {
	var tokenId uint
	if v, ok := c.Get("token_id"); ok {
		tokenId, _ = v.(uint)
	}
	var t models.Token
	if err := db.DB.Select("user_id").First(&t, tokenId).Error; err == nil {
		return tokenId, t.UserId
	}
	return tokenId, nil
}

func write(entry models.AuditLog) {
	if err := db.DB.Create(&entry).Error; err != nil {
		log.Printf("[audit] write failed: %v", err)
	}
	if hub != nil {
		hub.Publish("audit.log", entry)
	}
}
