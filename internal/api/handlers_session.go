package api

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

// randMacGo — sinh MAC random LAA `02:xx:xx:xx:xx:xx`.
func randMacGo() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4])
}

// allocMu — serialize allocation cua ppp_unit + proxy port trong bulk create.
// SQLite WAL với maxOpenConns=1 đã serialize TX, nhưng (SELECT free → INSERT)
// là 2 step rời → race nếu N goroutines song song. Mutex này thắt eo lại.
var allocMu sync.Mutex

type createSessionReq struct {
	Username string `json:"username"` // optional — fallback line.IspUsername
	Password string `json:"password"` // optional — fallback line.IspPassword
	MAC      string `json:"mac"`
}

func CreateLineSession(c *gin.Context) {
	lineID, _ := strconv.Atoi(c.Param("id"))
	var line models.Line
	if err := db.DB.First(&line, lineID).Error; err != nil {
		fail(c, 404, "line not found")
		return
	}
	var req createSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	sess, p, err := createSessionAndDial(line, req)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"session": sess, "proxy": p})
}

type bulkSessReq struct {
	Count       int                `json:"count" binding:"required"`
	Creds       []createSessionReq `json:"creds"`
	AutoMac     bool               `json:"auto_mac"` // mode N-only: backend tự sinh MAC random, dùng line cred
}

func CreateLineSessionsBulk(c *gin.Context) {
	lineID, _ := strconv.Atoi(c.Param("id"))
	var line models.Line
	if err := db.DB.First(&line, lineID).Error; err != nil {
		fail(c, 404, "line not found")
		return
	}
	var req bulkSessReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Count <= 0 || req.Count > 100 {
		fail(c, 400, "count must be 1..100")
		return
	}
	// Mode N-only: nếu auto_mac=true HOẶC không có creds → sinh N entries dùng line cred + MAC random.
	if req.AutoMac || len(req.Creds) == 0 {
		if line.IspUsername == "" || line.IspPassword == "" {
			fail(c, 400, "line chưa có isp_username/isp_password — cần PUT /lines/:id trước hoặc gửi creds explicit")
			return
		}
		req.Creds = make([]createSessionReq, req.Count)
		for i := 0; i < req.Count; i++ {
			req.Creds[i] = createSessionReq{MAC: randMacGo()}
		}
	}
	if len(req.Creds) < req.Count {
		fail(c, 400, fmt.Sprintf("need %d creds, got %d", req.Count, len(req.Creds)))
		return
	}
	type result struct {
		SessionId uint   `json:"session_id"`
		Username  string `json:"username"`
		Status    string `json:"status"`
		Error     string `json:"error,omitempty"`
	}
	out := make([]result, req.Count)
	var wg sync.WaitGroup
	for i := 0; i < req.Count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess, _, err := createSessionAndDial(line, req.Creds[idx])
			r := result{Username: req.Creds[idx].Username}
			if sess != nil {
				r.SessionId = sess.Id
				r.Status = sess.Status
			}
			if err != nil {
				r.Status = "error"
				r.Error = truncateErr(err.Error(), 240)
			}
			out[idx] = r
		}(i)
	}
	wg.Wait()
	ok(c, out)
}

// truncateErr — cắt error message dài (pppd verbose log) + extract AuthNak hint.
func truncateErr(s string, n int) string {
	low := strings.ToLower(s)
	if strings.Contains(low, "authnak") || strings.Contains(low, "authentication failed") {
		return "PAP AuthNak — cred không khớp BRAS (cred fake hoặc sai)"
	}
	if strings.Contains(low, "no pado") || strings.Contains(low, "timeout") {
		return "No PADO — NIC không nhận PPPoE upstream (cáp chưa cắm hoặc sai port)"
	}
	if strings.Contains(low, "iface") && strings.Contains(low, "did not come up") {
		return "iface không lên UP — pppd dial fail (xem last_error chi tiết)"
	}
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func createSessionAndDial(line models.Line, req createSessionReq) (*models.Session, *models.Proxy, error) {
	// Resolve cred: req override → fallback line.IspUsername/IspPassword.
	// Lý do: 1 PPPoE account ISP cấp ⇒ N session same cred, khác MAC (BRAS treat
	// như N gateway độc lập cùng account). User KHÔNG cần nhập cred mỗi session.
	username := req.Username
	password := req.Password
	if username == "" {
		username = line.IspUsername
	}
	if password == "" {
		password = line.IspPassword
	}
	if username == "" || password == "" {
		return nil, nil, fmt.Errorf("cần cred PPPoE: nhập trong line.isp_username/password hoặc override per-session")
	}

	// === Alloc phase (serialized) — đảm bảo ppp_unit + port không trùng khi N goroutines song song ===
	allocMu.Lock()
	pppUnit, err := pppoe.AllocPppUnit(db.DB)
	if err != nil {
		allocMu.Unlock()
		return nil, nil, err
	}
	sess := models.Session{
		LineId:   line.Id,
		PppUnit:  pppUnit,
		Username: username,
		Password: password,
		MAC:      req.MAC,
		Status:   models.StatusDisconnected,
	}
	if err := db.DB.Create(&sess).Error; err != nil {
		allocMu.Unlock()
		return nil, nil, err
	}
	port, err := proxysrv.M.AllocPort()
	if err != nil {
		db.DB.Delete(&sess)
		allocMu.Unlock()
		return nil, nil, err
	}
	p := models.Proxy{SessionId: sess.Id, Port: port, Status: "stopped"}
	if err := db.DB.Create(&p).Error; err != nil {
		db.DB.Delete(&sess)
		allocMu.Unlock()
		return nil, nil, err
	}
	allocMu.Unlock()
	// === End alloc ===

	// Seed cred mặc định cho proxy listener (CLIENT auth — KHÔNG phải PPPoE auth).
	// Mặc định lấy cùng cred ISP để user copy-paste 1 chỗ, có thể đổi thành random
	// sau qua /proxies/:id/credentials.
	cred := models.ProxyCredential{
		ProxyId:  p.Id,
		Label:    "default",
		Username: username,
		Password: password,
		Enabled:  true,
	}
	db.DB.Create(&cred)

	if err := pppoe.M.Dial(sess.Id); err != nil {
		// Dial fail — giữ session + proxy ở DB cho user retry. Không cascade delete.
		db.DB.First(&sess, sess.Id)
		return &sess, &p, fmt.Errorf("dial: %w", err)
	}
	if err := proxysrv.M.Start(p.Id); err != nil {
		return nil, nil, fmt.Errorf("proxy start: %w", err)
	}
	db.DB.First(&sess, sess.Id)
	db.DB.First(&p, p.Id)
	return &sess, &p, nil
}

func ListSessions(c *gin.Context) {
	var sessions []models.Session
	q := db.DB.Order("id ASC")
	if lineID := c.Query("line_id"); lineID != "" {
		q = q.Where("line_id = ?", lineID)
	}
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	q.Find(&sessions)

	type row struct {
		models.Session
		ProxyPort   int `json:"proxy_port"`
		ProxyId     uint `json:"proxy_id"`
		ProxyStatus string `json:"proxy_status"`
		CredsCount  int64  `json:"creds_count"`
	}
	out := make([]row, 0, len(sessions))
	for _, s := range sessions {
		r := row{Session: s}
		var p models.Proxy
		if err := db.DB.Where("session_id = ?", s.Id).First(&p).Error; err == nil {
			r.ProxyPort = p.Port
			r.ProxyId = p.Id
			r.ProxyStatus = p.Status
			db.DB.Model(&models.ProxyCredential{}).Where("proxy_id = ? AND enabled = ?", p.Id, true).Count(&r.CredsCount)
		}
		out = append(out, r)
	}
	ok(c, out)
}

func GetSession(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var s models.Session
	if err := db.DB.First(&s, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	var p models.Proxy
	db.DB.Where("session_id = ?", s.Id).First(&p)
	ok(c, gin.H{"session": s, "proxy": p})
}

func DeleteSession(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var s models.Session
	if err := db.DB.First(&s, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
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
	ok(c, gin.H{"deleted": id})
}

func RotateSession(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	oldIP, newIP, err := pppoe.M.Rotate(uint(id))
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"session_id": id, "old_ip": oldIP, "new_ip": newIP, "same_ip": oldIP == newIP})
}

type setEnabledReq struct {
	Enabled bool `json:"enabled"`
}

func SetSessionEnabled(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req setEnabledReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	var p models.Proxy
	if err := db.DB.Where("session_id = ?", id).First(&p).Error; err != nil {
		fail(c, 404, "proxy not found for session")
		return
	}
	if req.Enabled {
		if err := proxysrv.M.Start(p.Id); err != nil {
			fail(c, 500, err.Error())
			return
		}
	} else {
		if err := proxysrv.M.Stop(p.Id); err != nil {
			fail(c, 500, err.Error())
			return
		}
	}
	ok(c, gin.H{"session_id": id, "enabled": req.Enabled})
}

type rotateBatchReq struct {
	SessionIds  []uint `json:"session_ids" binding:"required"`
	Concurrency int    `json:"concurrency"`
}

func RotateBatch(c *gin.Context) {
	var req rotateBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 5
	}
	type result struct {
		SessionId uint   `json:"session_id"`
		OldIP     string `json:"old_ip"`
		NewIP     string `json:"new_ip"`
		Error     string `json:"error,omitempty"`
	}
	out := make([]result, len(req.SessionIds))
	sem := make(chan struct{}, req.Concurrency)
	var wg sync.WaitGroup
	for i, sid := range req.SessionIds {
		wg.Add(1)
		go func(idx int, id uint) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			oldIP, newIP, err := pppoe.M.Rotate(id)
			r := result{SessionId: id, OldIP: oldIP, NewIP: newIP}
			if err != nil {
				r.Error = err.Error()
			}
			out[idx] = r
		}(i, sid)
	}
	wg.Wait()
	ok(c, out)
}
