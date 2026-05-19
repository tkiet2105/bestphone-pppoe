package api

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

// allocMu — serialize allocation cua ppp_unit + proxy port trong bulk create.
// SQLite WAL với maxOpenConns=1 đã serialize TX, nhưng (SELECT free → INSERT)
// là 2 step rời → race nếu N goroutines song song. Mutex này thắt eo lại.
var allocMu sync.Mutex

type createSessionReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
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
	Count int                `json:"count" binding:"required"`
	Creds []createSessionReq `json:"creds"`
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
	if len(req.Creds) < req.Count {
		fail(c, 400, fmt.Sprintf("need %d creds, got %d", req.Count, len(req.Creds)))
		return
	}
	type result struct {
		SessionId uint   `json:"session_id"`
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
			if err != nil {
				out[idx] = result{Status: "error", Error: err.Error()}
				return
			}
			out[idx] = result{SessionId: sess.Id, Status: sess.Status}
		}(i)
	}
	wg.Wait()
	ok(c, out)
}

func createSessionAndDial(line models.Line, req createSessionReq) (*models.Session, *models.Proxy, error) {
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
		Username: req.Username,
		Password: req.Password,
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

	// Seed cred mặc định (mode multi-cred từ đầu — user có thể thêm/xóa sau)
	cred := models.ProxyCredential{
		ProxyId:  p.Id,
		Label:    "default",
		Username: req.Username,
		Password: req.Password,
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
