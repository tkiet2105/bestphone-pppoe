package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/activity"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

type createLineReq struct {
	Name        string `json:"name" binding:"required"`
	Iface       string `json:"iface" binding:"required"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	UseMacvlan  bool   `json:"use_macvlan"`
	MaxSessions int    `json:"max_sessions"`
	CustomMacs  string `json:"custom_macs"` // 1 MAC/dòng, format aa:bb:cc:dd:ee:ff
}

// normalizeAndValidateMacs — parse pool, validate từng entry, trả về chuỗi
// đã chuẩn hóa (1 MAC/dòng, lowercase, no dup) hoặc lỗi đầu tiên gặp phải.
func normalizeAndValidateMacs(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	macs := models.ParseMacs(raw)
	seen := make(map[string]bool, len(macs))
	out := make([]string, 0, len(macs))
	for _, m := range macs {
		if !models.IsValidMac(m) {
			return "", fmt.Errorf("MAC không hợp lệ: %q (cần format aa:bb:cc:dd:ee:ff)", m)
		}
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return strings.Join(out, "\n"), nil
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
	normalizedMacs, err := normalizeAndValidateMacs(req.CustomMacs)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	line := models.Line{
		Name:        req.Name,
		Iface:       req.Iface,
		Username:    req.Username,
		Password:    req.Password,
		UseMacvlan:  req.UseMacvlan,
		MaxSessions: req.MaxSessions,
		CustomMacs:  normalizedMacs,
	}
	if err := db.DB.Create(&line).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	activity.Info(activity.CategoryLine, "create",
		fmt.Sprintf("Tạo line #%d: %s (iface=%s, max=%d)", line.Id, line.Name, line.Iface, line.MaxSessions),
		activity.LineId(line.Id), activity.ClientIP(c.ClientIP()),
		activity.F("name", line.Name), activity.F("iface", line.Iface),
		activity.F("max_sessions", line.MaxSessions),
		activity.F("mac_pool_size", len(models.ParseMacs(line.CustomMacs))))
	ok(c, line)
}

type updateLineReq struct {
	Name        *string `json:"name"`
	Username    *string `json:"username"`
	Password    *string `json:"password"`
	UseMacvlan  *bool   `json:"use_macvlan"`
	MaxSessions *int    `json:"max_sessions"`
	CustomMacs  *string `json:"custom_macs"`
}

func UpdateLine(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var line models.Line
	if err := db.DB.First(&line, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	var req updateLineReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Name != nil {
		line.Name = *req.Name
	}
	if req.Username != nil {
		line.Username = *req.Username
	}
	if req.Password != nil {
		line.Password = *req.Password
	}
	if req.UseMacvlan != nil {
		line.UseMacvlan = *req.UseMacvlan
	}
	if req.MaxSessions != nil {
		line.MaxSessions = *req.MaxSessions
	}
	if req.CustomMacs != nil {
		normalized, err := normalizeAndValidateMacs(*req.CustomMacs)
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		line.CustomMacs = normalized
	}
	db.DB.Save(&line)
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
		pppoe.RemoveMacvlan(s.Id)
		pppoe.RemoveSessionSecrets(s.Username)
		db.DB.Delete(&s)
	}
	db.DB.Delete(&models.Line{}, id)
	activity.Warn(activity.CategoryLine, "delete",
		fmt.Sprintf("Xóa line #%d và %d session liên quan", id, len(sessions)),
		activity.LineId(uint(id)), activity.ClientIP(c.ClientIP()),
		activity.F("cascaded_sessions", len(sessions)))
	ok(c, gin.H{"deleted": id, "sessions": len(sessions)})
}
