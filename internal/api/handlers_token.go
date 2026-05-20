package api

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type tokenSummary struct {
	Id        uint      `json:"id"`
	Label     string    `json:"label"`
	Last4     string    `json:"last4"`
	UserId    *uint     `json:"user_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTokens — chỉ liệt kê API token (user_id IS NULL). Session login không hiển thị.
func ListTokens(c *gin.Context) {
	var rows []models.Token
	db.DB.Where("user_id IS NULL").Order("id ASC").Find(&rows)
	out := make([]tokenSummary, 0, len(rows))
	for _, t := range rows {
		last4 := t.Token
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		out = append(out, tokenSummary{Id: t.Id, Label: t.Label, Last4: last4, UserId: t.UserId, CreatedAt: t.CreatedAt})
	}
	ok(c, out)
}

type createTokenReq struct {
	Label string `json:"label"`
}

func CreateToken(c *gin.Context) {
	var req createTokenReq
	_ = c.ShouldBindJSON(&req)
	tok := newRandomToken()
	t := models.Token{Token: tok, Label: req.Label, CreatedAt: time.Now()}
	if err := db.DB.Create(&t).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"id": t.Id, "label": t.Label, "token": tok}})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	// Cấm xóa session login qua endpoint này (dùng /auth/logout)
	var t models.Token
	if err := db.DB.First(&t, id).Error; err != nil {
		fail(c, 404, "token không tồn tại")
		return
	}
	if t.UserId != nil {
		fail(c, 400, "không thể xóa session đăng nhập qua endpoint này")
		return
	}
	// Bảo vệ: ít nhất phải còn 1 user — không chặn xóa API token rỗng
	db.DB.Delete(&models.Token{}, id)
	ok(c, gin.H{"deleted": id})
}

// ─── Authentication: user/password login ──────────────────────────────────

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AuthLogin — POST /auth/login {username, password} → issue session token.
// KHÔNG cần Authorization header (public endpoint).
func AuthLogin(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "thiếu tên đăng nhập hoặc mật khẩu")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	var u models.User
	if err := db.DB.Where("username = ?", req.Username).First(&u).Error; err != nil {
		fail(c, 401, "sai tên đăng nhập hoặc mật khẩu")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		fail(c, 401, "sai tên đăng nhập hoặc mật khẩu")
		return
	}
	// Phát session token
	tok := newRandomToken()
	uid := u.Id
	t := models.Token{
		Token:     tok,
		Label:     "session:" + u.Username,
		UserId:    &uid,
		CreatedAt: time.Now(),
	}
	if err := db.DB.Create(&t).Error; err != nil {
		fail(c, 500, "không thể tạo session: "+err.Error())
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{
		"token":    tok,
		"username": u.Username,
		"user_id":  u.Id,
	}})
}

// AuthMe — GET /auth/me → trả info user đang đăng nhập (lookup theo token_id middleware lưu).
func AuthMe(c *gin.Context) {
	tokenIdAny, _ := c.Get("token_id")
	tokenId, _ := tokenIdAny.(uint)
	var t models.Token
	if err := db.DB.First(&t, tokenId).Error; err != nil {
		fail(c, 401, "token không hợp lệ")
		return
	}
	if t.UserId == nil {
		// API token — không gắn user, trả label rỗng
		ok(c, gin.H{"username": "", "is_api_token": true, "label": t.Label})
		return
	}
	var u models.User
	if err := db.DB.First(&u, *t.UserId).Error; err != nil {
		fail(c, 401, "user không tồn tại")
		return
	}
	ok(c, gin.H{
		"username":     u.Username,
		"user_id":      u.Id,
		"is_api_token": false,
		"created_at":   u.CreatedAt,
		"updated_at":   u.UpdatedAt,
	})
}

// AuthLogout — POST /auth/logout → xóa session token hiện tại. Yêu cầu token thuộc 1 user (không cho phép logout API token).
func AuthLogout(c *gin.Context) {
	tokenIdAny, _ := c.Get("token_id")
	tokenId, _ := tokenIdAny.(uint)
	var t models.Token
	if err := db.DB.First(&t, tokenId).Error; err != nil {
		ok(c, gin.H{"logged_out": true})
		return
	}
	if t.UserId == nil {
		fail(c, 400, "đây là API token, không thể logout — xóa qua /tokens/:id")
		return
	}
	db.DB.Delete(&models.Token{}, tokenId)
	ok(c, gin.H{"logged_out": true})
}

type changePassReq struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

// ChangePassword — POST /auth/change-password. Xác minh current_password, hash new_password.
// Xóa toàn bộ session token khác của user (force re-login mọi nơi), giữ token hiện tại.
func ChangePassword(c *gin.Context) {
	var req changePassReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "thiếu mật khẩu hiện tại hoặc mật khẩu mới")
		return
	}
	if len(req.NewPassword) < 6 {
		fail(c, 400, "mật khẩu mới phải ≥ 6 ký tự")
		return
	}
	tokenIdAny, _ := c.Get("token_id")
	tokenId, _ := tokenIdAny.(uint)
	var t models.Token
	if err := db.DB.First(&t, tokenId).Error; err != nil || t.UserId == nil {
		fail(c, 403, "API token không thể đổi mật khẩu")
		return
	}
	var u models.User
	if err := db.DB.First(&u, *t.UserId).Error; err != nil {
		fail(c, 401, "user không tồn tại")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		fail(c, 401, "sai mật khẩu hiện tại")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		fail(c, 500, "không thể hash mật khẩu: "+err.Error())
		return
	}
	u.PasswordHash = string(hash)
	u.UpdatedAt = time.Now()
	if err := db.DB.Save(&u).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	// Force re-login các session khác của user — giữ token hiện tại
	db.DB.Where("user_id = ? AND id <> ?", u.Id, tokenId).Delete(&models.Token{})
	ok(c, gin.H{"changed": true})
}

func newRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
