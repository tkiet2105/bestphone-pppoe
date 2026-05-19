package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

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
}

func CreateCred(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	var req createCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
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
	if err := db.DB.Create(&cr).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	proxysrv.M.ReloadCreds(uint(pid))
	ok(c, cr)
}

type bulkCredReq struct {
	Count       int    `json:"count" binding:"required"`
	LabelPrefix string `json:"label_prefix"`
}

func BulkCreateCreds(c *gin.Context) {
	pid, _ := strconv.Atoi(c.Param("id"))
	var req bulkCredReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Count <= 0 || req.Count > 100 {
		fail(c, 400, "count must be 1..100")
		return
	}
	if req.LabelPrefix == "" {
		req.LabelPrefix = "auto"
	}
	out := make([]models.ProxyCredential, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		u := randHex(6)
		p := randHex(8)
		cr := models.ProxyCredential{
			ProxyId:  uint(pid),
			Label:    fmt.Sprintf("%s-%d", req.LabelPrefix, i+1),
			Username: u,
			Password: p,
			Enabled:  true,
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
