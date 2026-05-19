package api

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

type createLineReq struct {
	Name        string `json:"name" binding:"required"`
	Iface       string `json:"iface" binding:"required"`
	UseMacvlan  bool   `json:"use_macvlan"`
	MaxSessions int    `json:"max_sessions"`
}

func CreateLine(c *gin.Context) {
	var req createLineReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.MaxSessions <= 0 {
		req.MaxSessions = 8
	}
	line := models.Line{
		Name:        req.Name,
		Iface:       req.Iface,
		UseMacvlan:  req.UseMacvlan,
		MaxSessions: req.MaxSessions,
	}
	if err := db.DB.Create(&line).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, line)
}

func ListLines(c *gin.Context) {
	var lines []models.Line
	db.DB.Order("id ASC").Find(&lines)
	type row struct {
		models.Line
		SessionCount int64 `json:"session_count"`
	}
	out := make([]row, 0, len(lines))
	for _, l := range lines {
		var n int64
		db.DB.Model(&models.Session{}).Where("line_id = ?", l.Id).Count(&n)
		out = append(out, row{Line: l, SessionCount: n})
	}
	ok(c, out)
}

func GetLine(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var line models.Line
	if err := db.DB.First(&line, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	ok(c, line)
}

// DeleteLine — cascade: hangup + remove peer + remove proxy listener + DB.
func DeleteLine(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var sessions []models.Session
	db.DB.Where("line_id = ?", id).Find(&sessions)
	for _, s := range sessions {
		// stop proxy + hangup pppd
		var p models.Proxy
		if err := db.DB.Where("session_id = ?", s.Id).First(&p).Error; err == nil {
			_ = proxysrv.M.Stop(p.Id)
			db.DB.Where("proxy_id = ?", p.Id).Delete(&models.ProxyCredential{})
			db.DB.Delete(&p)
		}
		_ = pppoe.M.Hangup(s.Id)
		pppoe.RemovePeerFile(s.Id)
		pppoe.RemoveSessionSecrets(s.Username)
		db.DB.Delete(&s)
	}
	db.DB.Delete(&models.Line{}, id)
	ok(c, gin.H{"deleted": id, "sessions": len(sessions)})
}
