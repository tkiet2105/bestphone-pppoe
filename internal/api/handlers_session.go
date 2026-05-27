package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

// allocMu — serialize ppp_unit + proxy port allocation cho bulk parallel.
var allocMu sync.Mutex

// randHexInternal — N bytes hex string. Mirror Mode 2 helper.
func randHexInternal(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// randMacGo — sinh MAC random LAA `02:xx:xx:xx:xx:xx`.
func randMacGo() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4])
}

// proxyAuthReq — mirror Mode 2 createSessionReq.ProxyAuth.
type proxyAuthReq struct {
	Mode     string `json:"mode"`     // "random" | "manual" | "none"
	Username string `json:"username"` // required khi mode=manual
	Password string `json:"password"` // required khi mode=manual
}

type createSessionReq struct {
	Username  string        `json:"username"`   // optional — override line cred
	Password  string        `json:"password"`   // optional — override line cred
	ProxyAuth *proxyAuthReq `json:"proxy_auth"` // optional — client proxy auth
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

// bulkSessReq — mirror Mode 2: chỉ count + proxy_auth (shared cho mọi session).
type bulkSessReq struct {
	Count     int           `json:"count" binding:"required"`
	ProxyAuth *proxyAuthReq `json:"proxy_auth"`
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
	if req.Count <= 0 || req.Count > 50 {
		fail(c, 400, "count must be 1..50")
		return
	}
	if line.Username == "" || line.Password == "" {
		fail(c, 400, "line chưa có username/password — cần PUT /lines/:id trước")
		return
	}

	type result struct {
		SessionId uint   `json:"session_id"`
		Status    string `json:"status"`
		Error     string `json:"error,omitempty"`
	}
	out := make([]result, req.Count)
	const maxRetries = 3
	for i := 0; i < req.Count; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		sub := createSessionReq{ProxyAuth: req.ProxyAuth}
		sess, _, err := createSessionAndDial(line, sub)
		for retry := 1; retry <= maxRetries && err != nil && isPADITimeout(err.Error()) && sess != nil; retry++ {
			log.Printf("[bulk-create] session %d PADI timeout, retry %d/%d", sess.Id, retry, maxRetries)
			time.Sleep(time.Duration(3+retry*2) * time.Second)
			if retryErr := pppoe.M.Dial(sess.Id); retryErr == nil {
				err = nil
				db.DB.First(&sess, sess.Id)
			}
		}
		r := result{}
		if sess != nil {
			r.SessionId = sess.Id
			r.Status = sess.Status
		}
		if err != nil {
			r.Status = "error"
			r.Error = truncateErr(err.Error(), 240)
		}
		out[i] = r
	}
	ok(c, out)
}

func isPADITimeout(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "no pado") || strings.Contains(low, "padi") || strings.Contains(low, "timeout")
}

// truncateErr — gọn error message dài (pppd verbose) + extract AuthNak hint.
func truncateErr(s string, n int) string {
	low := strings.ToLower(s)
	if strings.Contains(low, "authnak") || strings.Contains(low, "authentication failed") {
		return "PAP AuthNak — cred không khớp BRAS"
	}
	if strings.Contains(low, "no pado") || strings.Contains(low, "timeout") {
		return "No PADO — NIC không nhận PPPoE upstream"
	}
	if strings.Contains(low, "iface") && strings.Contains(low, "did not come up") {
		return "iface không lên UP"
	}
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// createSessionAndDial — flow Mode 2:
//  1. Resolve ISP cred: req → fallback line.Username/Password.
//  2. Sinh MAC random nếu line.UseMacvlan && req không có cred override.
//  3. Alloc ppp_unit + port (serialized).
//  4. Insert Session + Proxy + (PppoeProxyCredential theo proxy_auth.mode).
//  5. Dial pppd.
//  6. Start proxy listener.
func createSessionAndDial(line models.Line, req createSessionReq) (*models.Session, *models.Proxy, error) {
	username := req.Username
	password := req.Password
	if username == "" {
		username = line.Username
	}
	if password == "" {
		password = line.Password
	}
	if username == "" || password == "" {
		return nil, nil, fmt.Errorf("cần ISP cred: nhập trong line.username/password hoặc override per-session")
	}

	mac := ""
	if line.UseMacvlan {
		mac = randMacGo()
	}

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
		MAC:      mac,
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

	// Apply proxy_auth mode → seed ProxyCredential (CLIENT auth — KHÔNG phải ISP).
	mode := "random"
	if req.ProxyAuth != nil && req.ProxyAuth.Mode != "" {
		mode = req.ProxyAuth.Mode
	}
	switch mode {
	case "random":
		cred := models.ProxyCredential{
			ProxyId:  p.Id,
			Label:    "default",
			Username: "u" + randHexInternal(4),
			Password: randHexInternal(8),
			Enabled:  true,
		}
		db.DB.Create(&cred)
	case "manual":
		if req.ProxyAuth == nil || req.ProxyAuth.Username == "" || req.ProxyAuth.Password == "" {
			db.DB.Delete(&p)
			db.DB.Delete(&sess)
			return nil, nil, fmt.Errorf("proxy_auth.mode=manual cần username + password")
		}
		cred := models.ProxyCredential{
			ProxyId:  p.Id,
			Label:    "default",
			Username: req.ProxyAuth.Username,
			Password: req.ProxyAuth.Password,
			Enabled:  true,
		}
		db.DB.Create(&cred)
	case "none":
		// KHÔNG seed cred → listener mode no-auth (SOCKS5 method 0x00).
	default:
		db.DB.Delete(&p)
		db.DB.Delete(&sess)
		return nil, nil, fmt.Errorf("proxy_auth.mode phải là random|manual|none")
	}

	if err := pppoe.M.Dial(sess.Id); err != nil {
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
		ProxyPort   int    `json:"proxy_port"`
		ProxyId     uint   `json:"proxy_id"`
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

// SetSessionAutoRotate — cấu hình tự đổi IP chu kỳ cho phiên.
// body: { "seconds": N }  N=0 tắt, N>=60 = chu kỳ (giây). Tối thiểu 60s để tránh
// DDoS BRAS / rotate storm.
type setAutoRotateReq struct {
	Seconds int `json:"seconds"`
}

func SetSessionAutoRotate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req setAutoRotateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Seconds < 0 {
		fail(c, 400, "seconds phải >= 0")
		return
	}
	if req.Seconds > 0 && req.Seconds < 60 {
		fail(c, 400, "chu kỳ tối thiểu 60 giây (tránh rotate storm)")
		return
	}
	var s models.Session
	if err := db.DB.First(&s, id).Error; err != nil {
		fail(c, 404, "session không tồn tại")
		return
	}
	if err := db.DB.Model(&s).Update("auto_rotate_seconds", req.Seconds).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"session_id": id, "auto_rotate_seconds": req.Seconds})
}

// SetSessionAutoRotateBatch — bulk set cho nhiều phiên cùng lúc.
type setAutoRotateBatchReq struct {
	SessionIds []uint `json:"session_ids" binding:"required"`
	Seconds    int    `json:"seconds"`
}

func SetSessionAutoRotateBatch(c *gin.Context) {
	var req setAutoRotateBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Seconds < 0 {
		fail(c, 400, "seconds phải >= 0")
		return
	}
	if req.Seconds > 0 && req.Seconds < 60 {
		fail(c, 400, "chu kỳ tối thiểu 60 giây")
		return
	}
	if len(req.SessionIds) == 0 {
		fail(c, 400, "thiếu session_ids")
		return
	}
	res := db.DB.Model(&models.Session{}).Where("id IN ?", req.SessionIds).Update("auto_rotate_seconds", req.Seconds)
	if res.Error != nil {
		fail(c, 500, res.Error.Error())
		return
	}
	ok(c, gin.H{"updated": res.RowsAffected, "seconds": req.Seconds})
}

// ResumeAutoRotate — bỏ tạm dừng auto-rotate sau khi xoay fail, reset fail count.
func ResumeAutoRotate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var s models.Session
	if err := db.DB.First(&s, id).Error; err != nil {
		fail(c, 404, "session không tồn tại")
		return
	}
	if err := db.DB.Model(&s).Updates(map[string]any{
		"auto_rotate_paused": false,
		"rotate_fail_count":  0,
	}).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"session_id": id, "auto_rotate_paused": false})
}

type resumeAutoRotateBatchReq struct {
	SessionIds []uint `json:"session_ids" binding:"required"`
}

func ResumeAutoRotateBatch(c *gin.Context) {
	var req resumeAutoRotateBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if len(req.SessionIds) == 0 {
		fail(c, 400, "thiếu session_ids")
		return
	}
	res := db.DB.Model(&models.Session{}).Where("id IN ?", req.SessionIds).Updates(map[string]any{
		"auto_rotate_paused": false,
		"rotate_fail_count":  0,
	})
	if res.Error != nil {
		fail(c, 500, res.Error.Error())
		return
	}
	ok(c, gin.H{"updated": res.RowsAffected})
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
