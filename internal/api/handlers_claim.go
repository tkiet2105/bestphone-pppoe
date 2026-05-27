package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

type credResponse struct {
	CredId    uint       `json:"cred_id"`
	ProxyId   uint       `json:"proxy_id"`
	SessionId uint       `json:"session_id"`
	IP        string     `json:"ip"`
	Port      int        `json:"port"`
	Username  string     `json:"username"`
	Password  string     `json:"password"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func activeCredsForUser(iuserid string) []models.ProxyCredential {
	var creds []models.ProxyCredential
	now := time.Now()
	db.DB.Where("i_user_id = ? AND enabled = ? AND (expires_at IS NULL OR expires_at > ?)",
		iuserid, true, now).Find(&creds)
	return creds
}

func buildCredResponses(creds []models.ProxyCredential) []credResponse {
	out := make([]credResponse, 0, len(creds))
	for _, cr := range creds {
		var p models.Proxy
		if db.DB.First(&p, cr.ProxyId).Error != nil {
			continue
		}
		var s models.Session
		if db.DB.First(&s, p.SessionId).Error != nil {
			continue
		}
		ip := s.PublicIP
		if ip == "" {
			ip = s.IP
		}
		out = append(out, credResponse{
			CredId:    cr.Id,
			ProxyId:   p.Id,
			SessionId: s.Id,
			IP:        ip,
			Port:      p.Port,
			Username:  cr.Username,
			Password:  cr.Password,
			ExpiresAt: cr.ExpiresAt,
		})
	}
	return out
}

func availableProxies(excludeProxyIds []uint, count int) []models.Proxy {
	var proxies []models.Proxy
	q := db.DB.Joins("JOIN sessions ON sessions.id = proxies.session_id").
		Where("sessions.status = ? AND proxies.status = ?", models.StatusConnected, "running")
	if len(excludeProxyIds) > 0 {
		q = q.Where("proxies.id NOT IN ?", excludeProxyIds)
	}
	q.Limit(count).Find(&proxies)
	return proxies
}

func claimRandHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var claimMu sync.Mutex

type claimReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
	Count   int    `json:"count" binding:"required"`
	Ttl     int    `json:"ttl"`
}

func ClaimCredentials(c *gin.Context) {
	var req claimReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Count < 1 {
		fail(c, 400, "count phải >= 1")
		return
	}

	claimMu.Lock()
	defer claimMu.Unlock()

	existing := activeCredsForUser(req.IUserId)
	if len(existing) >= req.Count {
		ok(c, gin.H{"iuser_id": req.IUserId, "credentials": buildCredResponses(existing[:req.Count])})
		return
	}

	need := req.Count - len(existing)
	usedProxyIds := make([]uint, 0, len(existing))
	for _, cr := range existing {
		usedProxyIds = append(usedProxyIds, cr.ProxyId)
	}

	proxies := availableProxies(usedProxyIds, need)
	if len(proxies) < need {
		fail(c, 400, fmt.Sprintf("không đủ sessions: đã có %d creds, cần thêm %d, chỉ còn %d sessions trống",
			len(existing), need, len(proxies)))
		return
	}

	var expPtr *time.Time
	if req.Ttl > 0 {
		exp := time.Now().Add(time.Duration(req.Ttl) * time.Second)
		expPtr = &exp
	}

	newCreds := make([]models.ProxyCredential, 0, need)
	for _, p := range proxies[:need] {
		cr := models.ProxyCredential{
			ProxyId:   p.Id,
			Label:     "claim",
			Username:  "u" + claimRandHex(4),
			Password:  claimRandHex(8),
			Enabled:   true,
			IUserId:   req.IUserId,
			ExpiresAt: expPtr,
		}
		if err := db.DB.Create(&cr).Error; err != nil {
			fail(c, 500, err.Error())
			return
		}
		proxysrv.M.ReloadCreds(p.Id)
		newCreds = append(newCreds, cr)
	}

	all := append(existing, newCreds...)
	ok(c, gin.H{"iuser_id": req.IUserId, "credentials": buildCredResponses(all)})
}

type changeReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
	CredIds []uint `json:"cred_ids" binding:"required"`
}

func ChangeCredentials(c *gin.Context) {
	var req changeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if len(req.CredIds) == 0 {
		fail(c, 400, "cred_ids không được rỗng")
		return
	}

	claimMu.Lock()
	defer claimMu.Unlock()

	var oldCreds []models.ProxyCredential
	db.DB.Where("id IN ? AND i_user_id = ?", req.CredIds, req.IUserId).Find(&oldCreds)
	if len(oldCreds) != len(req.CredIds) {
		fail(c, 400, "một số cred_ids không tồn tại hoặc không thuộc iuser_id này")
		return
	}

	allUserCreds := activeCredsForUser(req.IUserId)
	usedProxyIds := make([]uint, 0, len(allUserCreds))
	for _, cr := range allUserCreds {
		usedProxyIds = append(usedProxyIds, cr.ProxyId)
	}

	proxies := availableProxies(usedProxyIds, len(oldCreds))
	if len(proxies) < len(oldCreds) {
		fail(c, 400, "không đủ sessions khả dụng để đổi")
		return
	}

	now := time.Now()
	newCreds := make([]models.ProxyCredential, 0, len(oldCreds))
	for i, old := range oldCreds {
		var expPtr *time.Time
		if old.ExpiresAt != nil {
			remaining := old.ExpiresAt.Sub(now)
			if remaining > 0 {
				exp := now.Add(remaining)
				expPtr = &exp
			}
		}

		cr := models.ProxyCredential{
			ProxyId:   proxies[i].Id,
			Label:     "claim",
			Username:  "u" + claimRandHex(4),
			Password:  claimRandHex(8),
			Enabled:   true,
			IUserId:   req.IUserId,
			ExpiresAt: expPtr,
		}
		if err := db.DB.Create(&cr).Error; err != nil {
			fail(c, 500, err.Error())
			return
		}
		proxysrv.M.ReloadCreds(proxies[i].Id)

		oldProxyId := old.ProxyId
		db.DB.Delete(&old)
		proxysrv.M.ReloadCreds(oldProxyId)

		newCreds = append(newCreds, cr)
	}

	ok(c, gin.H{"iuser_id": req.IUserId, "credentials": buildCredResponses(newCreds)})
}

func ListUserCreds(c *gin.Context) {
	iuserid := c.Query("iuser_id")
	if iuserid == "" {
		fail(c, 400, "thiếu query param iuser_id")
		return
	}
	creds := activeCredsForUser(iuserid)
	ok(c, gin.H{"iuser_id": iuserid, "credentials": buildCredResponses(creds)})
}

type releaseReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
}

func ReleaseCredentials(c *gin.Context) {
	var req releaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}

	var creds []models.ProxyCredential
	db.DB.Where("i_user_id = ?", req.IUserId).Find(&creds)

	proxyIds := make(map[uint]bool)
	for _, cr := range creds {
		proxyIds[cr.ProxyId] = true
	}

	res := db.DB.Where("i_user_id = ?", req.IUserId).Delete(&models.ProxyCredential{})

	for pid := range proxyIds {
		proxysrv.M.ReloadCreds(pid)
	}

	ok(c, gin.H{"iuser_id": req.IUserId, "released": res.RowsAffected})
}

type extendReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
	Ttl     int    `json:"ttl" binding:"required"`
}

func ClaimStatus(c *gin.Context) {
	var totalSessions int64
	db.DB.Model(&models.Session{}).Where("status = ?", models.StatusConnected).Count(&totalSessions)

	now := time.Now()
	var totalClaimed int64
	db.DB.Model(&models.ProxyCredential{}).
		Where("i_user_id != '' AND enabled = ? AND (expires_at IS NULL OR expires_at > ?)", true, now).
		Count(&totalClaimed)

	var activeUsers int64
	db.DB.Model(&models.ProxyCredential{}).
		Where("i_user_id != '' AND enabled = ? AND (expires_at IS NULL OR expires_at > ?)", true, now).
		Distinct("i_user_id").Count(&activeUsers)

	ok(c, gin.H{
		"total_connected_sessions": totalSessions,
		"active_users":             activeUsers,
		"total_claimed_creds":      totalClaimed,
	})
}

func ClaimUserStatus(c *gin.Context) {
	iuserid := c.Query("iuser_id")
	if iuserid == "" {
		fail(c, 400, "thiếu query param iuser_id")
		return
	}
	creds := activeCredsForUser(iuserid)
	ok(c, gin.H{
		"iuser_id":     iuserid,
		"active_creds": len(creds),
		"credentials":  buildCredResponses(creds),
	})
}

func ClaimUsers(c *gin.Context) {
	now := time.Now()
	type userRow struct {
		IUserId        string     `json:"iuser_id"`
		CredCount      int64      `json:"cred_count"`
		EarliestExpiry *time.Time `json:"earliest_expiry"`
	}
	var rows []userRow
	db.DB.Model(&models.ProxyCredential{}).
		Select("i_user_id, COUNT(*) as cred_count, MIN(expires_at) as earliest_expiry").
		Where("i_user_id != '' AND enabled = ? AND (expires_at IS NULL OR expires_at > ?)", true, now).
		Group("i_user_id").
		Scan(&rows)
	ok(c, rows)
}

func ExtendCredentials(c *gin.Context) {
	var req extendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Ttl <= 0 {
		fail(c, 400, "ttl phải > 0")
		return
	}

	creds := activeCredsForUser(req.IUserId)
	if len(creds) == 0 {
		fail(c, 404, "không tìm thấy credentials active cho iuser_id này")
		return
	}

	exp := time.Now().Add(time.Duration(req.Ttl) * time.Second)
	ids := make([]uint, 0, len(creds))
	for _, cr := range creds {
		ids = append(ids, cr.Id)
	}
	db.DB.Model(&models.ProxyCredential{}).Where("id IN ?", ids).Update("expires_at", exp)

	updated := activeCredsForUser(req.IUserId)
	ok(c, gin.H{"iuser_id": req.IUserId, "credentials": buildCredResponses(updated)})
}
