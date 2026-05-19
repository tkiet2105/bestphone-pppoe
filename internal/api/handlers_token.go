package api

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type tokenSummary struct {
	Id        uint      `json:"id"`
	Label     string    `json:"label"`
	Last4     string    `json:"last4"`
	CreatedAt time.Time `json:"created_at"`
}

func ListTokens(c *gin.Context) {
	var rows []models.Token
	db.DB.Order("id ASC").Find(&rows)
	out := make([]tokenSummary, 0, len(rows))
	for _, t := range rows {
		last4 := t.Token
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		out = append(out, tokenSummary{Id: t.Id, Label: t.Label, Last4: last4, CreatedAt: t.CreatedAt})
	}
	ok(c, out)
}

type createTokenReq struct {
	Label string `json:"label"`
}

func CreateToken(c *gin.Context) {
	var req createTokenReq
	_ = c.ShouldBindJSON(&req)
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	t := models.Token{Token: tok, Label: req.Label, CreatedAt: time.Now()}
	if err := db.DB.Create(&t).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	// Trả full token 1 lần (sau này không readable qua API)
	c.JSON(200, gin.H{"success": true, "data": gin.H{"id": t.Id, "label": t.Label, "token": tok}})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	// Bảo vệ: không cho xóa token cuối cùng (lock-out)
	var n int64
	db.DB.Model(&models.Token{}).Count(&n)
	if n <= 1 {
		fail(c, 400, "cannot delete last token")
		return
	}
	db.DB.Delete(&models.Token{}, id)
	ok(c, gin.H{"deleted": id})
}

// AuthLogin — POST /auth/login {token:"..."} → echo token (cho UI lưu localStorage sau khi verify).
type loginReq struct {
	Token string `json:"token" binding:"required"`
}

func AuthLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	var t models.Token
	if err := db.DB.Where("token = ?", req.Token).First(&t).Error; err != nil {
		fail(c, 401, "invalid token")
		return
	}
	ok(c, gin.H{"label": t.Label, "id": t.Id})
}
