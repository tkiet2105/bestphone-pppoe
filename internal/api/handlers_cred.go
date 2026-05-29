package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

// checkCredLimit — đảm bảo không vượt max iuser-claim slots theo session.Type.
// addN = số creds sắp tạo thêm. Áp dụng cho cả admin manual create + bulk +
// claim flow để có cùng định nghĩa "max" (số iuser slot).
//
// Không count default/seed creds (i_user_id = '') — chúng là infrastructure
// (auth bootstrap), không tính vào slot quota. Nhờ vậy private session
// (max=1) vẫn cho phép 1 claim dù đã có 1 default cred.
func checkCredLimit(proxyId uint, addN int) error {
	var p models.Proxy
	if err := db.DB.First(&p, proxyId).Error; err != nil {
		return fmt.Errorf("proxy không tồn tại")
	}
	var s models.Session
	if err := db.DB.First(&s, p.SessionId).Error; err != nil {
		return fmt.Errorf("session không tồn tại")
	}
	max := models.MaxCredsForType(s.Type)
	var cur int64
	now := time.Now()
	db.DB.Model(&models.ProxyCredential{}).
		Where("proxy_id = ? AND enabled = ? AND i_user_id != '' AND (expires_at IS NULL OR expires_at > ?)", proxyId, true, now).
		Count(&cur)
	if int(cur)+addN > max {
		return fmt.Errorf("vượt giới hạn iuser slots: session type=%s tối đa %d, đang có %d (claimed), thêm %d", s.Type, max, cur, addN)
	}
	return nil
}

func ListCreds(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	var rows []models.ProxyCredential
	db.DB.Where("proxy_id = ?", pid).Order("id ASC").Find(&rows)
	ok(c, rows)
}

type createCredReq struct {
	Label    string `json:"label"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Ttl      int    `json:"ttl"`
}

func CreateCred(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	var req createCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if err := checkCredLimit(uint(pid), 1); err != nil {
		fail(c, 400, err.Error())
		return
	}
	cr := models.ProxyCredential{
		ProxyId:  uint(pid),
		Label:    req.Label,
		Username: req.Username,
		Password: req.Password,
		Enabled:  true,
	}
	if req.Ttl > 0 {
		exp := time.Now().Add(time.Duration(req.Ttl) * time.Second)
		cr.ExpiresAt = &exp
	}
	if err := db.DB.Create(&cr).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	proxysrv.M.ReloadCreds(uint(pid))
	ok(c, cr)
}

type bulkCredReq struct {
	Count  int    `json:"count" binding:"required"`
	Prefix string `json:"prefix"` // username prefix, default "u"
	Ttl    int    `json:"ttl"`
}

func BulkCreateCreds(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	var req bulkCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Count <= 0 || req.Count > 200 {
		fail(c, 400, "count must be 1..200")
		return
	}
	if err := checkCredLimit(uint(pid), req.Count); err != nil {
		fail(c, 400, err.Error())
		return
	}
	prefix := req.Prefix
	if prefix == "" {
		prefix = "u"
	}
	var expPtr *time.Time
	if req.Ttl > 0 {
		exp := time.Now().Add(time.Duration(req.Ttl) * time.Second)
		expPtr = &exp
	}
	out := make([]models.ProxyCredential, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		cr := models.ProxyCredential{
			ProxyId:   uint(pid),
			Label:     "sub-cred",
			Username:  prefix + randHex(4),
			Password:  randHex(8),
			Enabled:   true,
			ExpiresAt: expPtr,
		}
		if err := db.DB.Create(&cr).Error; err != nil {
			fail(c, 500, err.Error())
			return
		}
		out = append(out, cr)
	}
	proxysrv.M.ReloadCreds(uint(pid))
	ok(c, out)
}

type updateCredReq struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
	Enabled  *bool   `json:"enabled"`
	Label    *string `json:"label"`
	Ttl      *int    `json:"ttl"`
}

func UpdateCred(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	cid, _ := strconv.Atoi(c.Param("cid"))
	var cr models.ProxyCredential
	if err := db.DB.Where("id = ? AND proxy_id = ?", cid, pid).First(&cr).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	var req updateCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Username != nil {
		cr.Username = *req.Username
	}
	if req.Password != nil {
		cr.Password = *req.Password
	}
	if req.Enabled != nil {
		cr.Enabled = *req.Enabled
	}
	if req.Label != nil {
		cr.Label = *req.Label
	}
	if req.Ttl != nil {
		if *req.Ttl > 0 {
			exp := time.Now().Add(time.Duration(*req.Ttl) * time.Second)
			cr.ExpiresAt = &exp
		} else {
			cr.ExpiresAt = nil
		}
	}
	db.DB.Save(&cr)
	proxysrv.M.ReloadCreds(uint(pid))
	ok(c, cr)
}

func DeleteCred(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	cid, _ := strconv.Atoi(c.Param("cid"))
	res := db.DB.Where("id = ? AND proxy_id = ?", cid, pid).Delete(&models.ProxyCredential{})
	if res.Error != nil {
		fail(c, 500, res.Error.Error())
		return
	}
	proxysrv.M.ReloadCreds(uint(pid))
	ok(c, gin.H{"deleted": cid})
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
