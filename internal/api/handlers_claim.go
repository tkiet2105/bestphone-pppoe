package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/activity"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

type credResponse struct {
	CredId    uint       `json:"cred_id"`
	ProxyId   uint       `json:"proxy_id"`
	SessionId uint       `json:"session_id"`
	Type      string     `json:"type"`
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

// activeCredsForUserByType — chỉ trả creds thuộc session.type = sessType.
func activeCredsForUserByType(iuserid, sessType string) []models.ProxyCredential {
	all := activeCredsForUser(iuserid)
	if sessType == "" {
		return all
	}
	out := make([]models.ProxyCredential, 0, len(all))
	for _, cr := range all {
		if credSessionType(cr.ProxyId) == sessType {
			out = append(out, cr)
		}
	}
	return out
}

// credSessionType — lookup session.Type qua proxy_id. Empty nếu không tìm thấy.
func credSessionType(proxyId uint) string {
	var p models.Proxy
	if db.DB.First(&p, proxyId).Error != nil {
		return ""
	}
	var s models.Session
	if db.DB.First(&s, p.SessionId).Error != nil {
		return ""
	}
	return s.Type
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
			Type:      s.Type,
			IP:        ip,
			Port:      p.Port,
			Username:  cr.Username,
			Password:  cr.Password,
			ExpiresAt: cr.ExpiresAt,
		})
	}
	return out
}

// availableProxiesForType — proxies running, session connected, type khớp, còn slot
// theo MaxCredsForType(session.type) và không nằm trong excludeProxyIds.
//
// Shuffle candidates trước khi filter slot để:
//   - Change cred KHÔNG ping-pong A↔B↔A (trước: order by id asc → đổi cred A
//     → exclude A → cấp B → đổi B → exclude B → cấp A. Lặp vô tận giữa 2-3 proxy)
//   - Claim đồng thời nhiều user thì proxy được phân bố đều, không dồn user
//     đầu vào cùng nhóm proxy id thấp
func availableProxiesForType(excludeProxyIds []uint, count int, sessType string) []models.Proxy {
	var candidates []models.Proxy
	q := db.DB.Joins("JOIN sessions ON sessions.id = proxies.session_id").
		Where("sessions.status = ? AND proxies.status = ?", models.StatusConnected, "running")
	if sessType != "" {
		q = q.Where("sessions.type = ?", sessType)
	}
	if len(excludeProxyIds) > 0 {
		q = q.Where("proxies.id NOT IN ?", excludeProxyIds)
	}
	q.Find(&candidates)

	// Shuffle in-place (Fisher-Yates) — math/rand tự seed từ Go 1.20+
	mathrand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	out := make([]models.Proxy, 0, count)
	now := time.Now()
	for _, p := range candidates {
		var s models.Session
		if db.DB.First(&s, p.SessionId).Error != nil {
			continue
		}
		max := models.MaxCredsForType(s.Type)
		// CHỈ count creds có i_user_id (claim cred), KHÔNG count default seed
		// cred (label="default", iuser=""). Trước: private session luôn có 1
		// default cred → cur=1 → max=1 → không bao giờ available, dù chưa có
		// khách nào claim. "Max" phản ánh số iuser slot, không phải tổng cred.
		var cur int64
		db.DB.Model(&models.ProxyCredential{}).
			Where("proxy_id = ? AND enabled = ? AND i_user_id != '' AND (expires_at IS NULL OR expires_at > ?)", p.Id, true, now).
			Count(&cur)
		if int(cur) < max {
			out = append(out, p)
			if len(out) >= count {
				break
			}
		}
	}
	return out
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
	Type    string `json:"type"` // static | private | rotating (default rotating)
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
	sessType := req.Type
	if sessType == "" {
		sessType = models.SessionTypeRotating
	}
	if !models.IsValidSessionType(sessType) {
		fail(c, 400, "type phải là static|private|rotating")
		return
	}

	claimMu.Lock()
	defer claimMu.Unlock()

	existingAll := activeCredsForUser(req.IUserId)
	existing := make([]models.ProxyCredential, 0, len(existingAll))
	for _, cr := range existingAll {
		if credSessionType(cr.ProxyId) == sessType {
			existing = append(existing, cr)
		}
	}

	if len(existing) >= req.Count {
		ok(c, gin.H{"iuser_id": req.IUserId, "type": sessType, "credentials": buildCredResponses(existing[:req.Count])})
		return
	}

	need := req.Count - len(existing)
	// Exclude proxies user đã có cred (mọi type) để không trùng session per-user.
	usedProxyIds := make([]uint, 0, len(existingAll))
	for _, cr := range existingAll {
		usedProxyIds = append(usedProxyIds, cr.ProxyId)
	}

	proxies := availableProxiesForType(usedProxyIds, need, sessType)
	if len(proxies) < need {
		activity.Warn(activity.CategoryClaim, "insufficient_slots",
			fmt.Sprintf("Claim type=%s cho iuser=%s thiếu slot: cần thêm %d, còn %d",
				sessType, req.IUserId, need, len(proxies)),
			activity.IUserId(req.IUserId), activity.ClientIP(c.ClientIP()),
			activity.F("type", sessType), activity.F("need", need),
			activity.F("available", len(proxies)))
		fail(c, 400, fmt.Sprintf("không đủ sessions type=%s: đã có %d creds, cần thêm %d, chỉ còn %d slot",
			sessType, len(existing), need, len(proxies)))
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
	activity.Info(activity.CategoryClaim, "ok",
		fmt.Sprintf("Claim thành công: iuser=%s, type=%s, cấp %d cred mới (tổng %d)",
			req.IUserId, sessType, len(newCreds), len(all)),
		activity.IUserId(req.IUserId), activity.ClientIP(c.ClientIP()),
		activity.F("type", sessType), activity.F("new_count", len(newCreds)),
		activity.F("total_count", len(all)))
	ok(c, gin.H{"iuser_id": req.IUserId, "type": sessType, "credentials": buildCredResponses(all)})
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

	now := time.Now()
	newCreds := make([]models.ProxyCredential, 0, len(oldCreds))

	// Per-cred: tìm thay thế cùng type. Sau mỗi lần allocate, thêm proxy mới vào
	// usedProxyIds để tránh các vòng sau pick lại.
	for _, old := range oldCreds {
		sessType := credSessionType(old.ProxyId)
		if sessType == "" {
			fail(c, 500, fmt.Sprintf("không xác định được type cho cred #%d", old.Id))
			return
		}
		fresh := availableProxiesForType(usedProxyIds, 1, sessType)
		if len(fresh) == 0 {
			fail(c, 400, fmt.Sprintf("không đủ sessions type=%s khả dụng để đổi cho cred #%d", sessType, old.Id))
			return
		}

		var expPtr *time.Time
		if old.ExpiresAt != nil {
			remaining := old.ExpiresAt.Sub(now)
			if remaining > 0 {
				exp := now.Add(remaining)
				expPtr = &exp
			}
		}

		cr := models.ProxyCredential{
			ProxyId:   fresh[0].Id,
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
		proxysrv.M.ReloadCreds(fresh[0].Id)

		oldProxyId := old.ProxyId
		db.DB.Delete(&old)
		proxysrv.M.ReloadCreds(oldProxyId)

		newCreds = append(newCreds, cr)
		usedProxyIds = append(usedProxyIds, fresh[0].Id)
	}

	activity.Info(activity.CategoryClaim, "change",
		fmt.Sprintf("Change credentials: iuser=%s, đổi %d cred", req.IUserId, len(newCreds)),
		activity.IUserId(req.IUserId), activity.ClientIP(c.ClientIP()),
		activity.F("changed_count", len(newCreds)),
		activity.F("old_cred_ids", req.CredIds))
	ok(c, gin.H{"iuser_id": req.IUserId, "credentials": buildCredResponses(newCreds)})
}

func ListUserCreds(c *gin.Context) {
	iuserid := c.Query("iuser_id")
	if iuserid == "" {
		fail(c, 400, "thiếu query param iuser_id")
		return
	}
	sessType := c.Query("type") // optional filter
	if sessType != "" && !models.IsValidSessionType(sessType) {
		fail(c, 400, "type phải là static|private|rotating")
		return
	}
	creds := activeCredsForUserByType(iuserid, sessType)
	ok(c, gin.H{"iuser_id": iuserid, "type": sessType, "credentials": buildCredResponses(creds)})
}

type releaseReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
	Type    string `json:"type"` // optional — chỉ release creds thuộc type này
}

func ReleaseCredentials(c *gin.Context) {
	var req releaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Type != "" && !models.IsValidSessionType(req.Type) {
		fail(c, 400, "type phải là static|private|rotating")
		return
	}

	var creds []models.ProxyCredential
	db.DB.Where("i_user_id = ?", req.IUserId).Find(&creds)

	// Nếu có type filter → chỉ giữ những cred thuộc type đó.
	if req.Type != "" {
		filtered := make([]models.ProxyCredential, 0, len(creds))
		for _, cr := range creds {
			if credSessionType(cr.ProxyId) == req.Type {
				filtered = append(filtered, cr)
			}
		}
		creds = filtered
	}

	proxyIds := make(map[uint]bool)
	credIds := make([]uint, 0, len(creds))
	for _, cr := range creds {
		proxyIds[cr.ProxyId] = true
		credIds = append(credIds, cr.Id)
	}

	var released int64
	if len(credIds) > 0 {
		res := db.DB.Where("id IN ?", credIds).Delete(&models.ProxyCredential{})
		released = res.RowsAffected
	}

	for pid := range proxyIds {
		proxysrv.M.ReloadCreds(pid)
	}

	if released > 0 {
		activity.Info(activity.CategoryClaim, "release",
			fmt.Sprintf("Release %d cred của iuser=%s (type=%s)", released, req.IUserId,
				ternaryStr(req.Type == "", "tất cả", req.Type)),
			activity.IUserId(req.IUserId), activity.ClientIP(c.ClientIP()),
			activity.F("type", req.Type), activity.F("released", released))
	}
	ok(c, gin.H{"iuser_id": req.IUserId, "type": req.Type, "released": released})
}

func ternaryStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

type extendReq struct {
	IUserId string `json:"iuser_id" binding:"required"`
	Ttl     int    `json:"ttl" binding:"required"`
	Type    string `json:"type"`     // optional — chỉ extend creds thuộc type này
	CredIds []uint `json:"cred_ids"` // optional — chỉ extend các cred cụ thể (subset của iuser)
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

	// breakdown by type
	type typeStats struct {
		Sessions       int64 `json:"sessions"`
		ClaimedCreds   int64 `json:"claimed_creds"`
		AvailableSlots int64 `json:"available_slots"`
	}
	byType := make(map[string]typeStats)
	for _, t := range []string{models.SessionTypeStatic, models.SessionTypePrivate, models.SessionTypeRotating} {
		var sessCount int64
		db.DB.Model(&models.Session{}).Where("status = ? AND type = ?", models.StatusConnected, t).Count(&sessCount)

		var claimedInType int64
		db.DB.Model(&models.ProxyCredential{}).
			Joins("JOIN proxies ON proxies.id = proxy_credentials.proxy_id").
			Joins("JOIN sessions ON sessions.id = proxies.session_id").
			Where("proxy_credentials.i_user_id != '' AND proxy_credentials.enabled = ? AND (proxy_credentials.expires_at IS NULL OR proxy_credentials.expires_at > ?) AND sessions.type = ?",
				true, now, t).
			Count(&claimedInType)

		maxPerSess := int64(models.MaxCredsForType(t))
		available := sessCount*maxPerSess - claimedInType
		if available < 0 {
			available = 0
		}
		byType[t] = typeStats{Sessions: sessCount, ClaimedCreds: claimedInType, AvailableSlots: available}
	}

	ok(c, gin.H{
		"total_connected_sessions": totalSessions,
		"active_users":             activeUsers,
		"total_claimed_creds":      totalClaimed,
		"by_type":                  byType,
	})
}

func ClaimUserStatus(c *gin.Context) {
	iuserid := c.Query("iuser_id")
	if iuserid == "" {
		fail(c, 400, "thiếu query param iuser_id")
		return
	}
	creds := activeCredsForUser(iuserid)
	resps := buildCredResponses(creds)

	byType := map[string]int{
		models.SessionTypeStatic:   0,
		models.SessionTypePrivate:  0,
		models.SessionTypeRotating: 0,
	}
	for _, r := range resps {
		if _, ok := byType[r.Type]; ok {
			byType[r.Type]++
		}
	}

	ok(c, gin.H{
		"iuser_id":     iuserid,
		"active_creds": len(resps),
		"by_type":      byType,
		"credentials":  resps,
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

	// Đếm breakdown by type cho từng user.
	type userOut struct {
		IUserId        string         `json:"iuser_id"`
		CredCount      int64          `json:"cred_count"`
		EarliestExpiry *time.Time     `json:"earliest_expiry"`
		ByType         map[string]int `json:"by_type"`
	}
	out := make([]userOut, 0, len(rows))
	for _, r := range rows {
		creds := activeCredsForUser(r.IUserId)
		bt := map[string]int{
			models.SessionTypeStatic:   0,
			models.SessionTypePrivate:  0,
			models.SessionTypeRotating: 0,
		}
		for _, cr := range creds {
			t := credSessionType(cr.ProxyId)
			if _, ok := bt[t]; ok {
				bt[t]++
			}
		}
		out = append(out, userOut{
			IUserId:        r.IUserId,
			CredCount:      r.CredCount,
			EarliestExpiry: r.EarliestExpiry,
			ByType:         bt,
		})
	}
	ok(c, out)
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
	if req.Type != "" && !models.IsValidSessionType(req.Type) {
		fail(c, 400, "type phải là static|private|rotating")
		return
	}

	// Scope: nếu có cred_ids → chỉ extend các cred được liệt kê (sau khi
	// kiểm tra ownership = iuser_id). Ngược lại → extend toàn bộ iuser (tùy
	// chọn lọc theo type).
	var targetCreds []models.ProxyCredential
	if len(req.CredIds) > 0 {
		db.DB.Where("id IN ? AND i_user_id = ?", req.CredIds, req.IUserId).Find(&targetCreds)
		if len(targetCreds) != len(req.CredIds) {
			fail(c, 400, "một số cred_ids không tồn tại hoặc không thuộc iuser_id này")
			return
		}
	} else {
		targetCreds = activeCredsForUserByType(req.IUserId, req.Type)
		if len(targetCreds) == 0 {
			fail(c, 404, "không tìm thấy credentials active cho iuser_id này")
			return
		}
	}

	// Cộng dồn: new_expires_at = max(now, current_expires_at) + ttl giây.
	// - Cred còn hạn → cộng vào hạn hiện tại
	// - Cred quá hạn → tính lại từ now (không cộng quá khứ âm)
	// - Cred NULL (vô thời hạn) → set mới = now + ttl (như claim mới)
	now := time.Now()
	add := time.Duration(req.Ttl) * time.Second
	ids := make([]uint, 0, len(targetCreds))
	for _, cr := range targetCreds {
		var base time.Time
		if cr.ExpiresAt == nil || cr.ExpiresAt.Before(now) {
			base = now
		} else {
			base = *cr.ExpiresAt
		}
		newExp := base.Add(add)
		db.DB.Model(&models.ProxyCredential{}).Where("id = ?", cr.Id).Update("expires_at", newExp)
		ids = append(ids, cr.Id)
	}

	// Trả về cred đã extend (refresh từ DB)
	var updated []models.ProxyCredential
	if len(ids) > 0 {
		db.DB.Where("id IN ?", ids).Find(&updated)
	}

	scope := ternaryStr(req.Type == "", "tất cả", req.Type)
	if len(req.CredIds) > 0 {
		scope = fmt.Sprintf("%d cred chỉ định", len(req.CredIds))
	}
	activity.Info(activity.CategoryClaim, "extend",
		fmt.Sprintf("Cộng dồn %ds cho %d cred của iuser=%s (scope: %s)",
			req.Ttl, len(updated), req.IUserId, scope),
		activity.IUserId(req.IUserId), activity.ClientIP(c.ClientIP()),
		activity.F("type", req.Type), activity.F("ttl_seconds_added", req.Ttl),
		activity.F("cred_ids", req.CredIds),
		activity.F("extended_count", len(updated)))
	ok(c, gin.H{
		"iuser_id":    req.IUserId,
		"type":        req.Type,
		"credentials": buildCredResponses(updated),
	})
}
