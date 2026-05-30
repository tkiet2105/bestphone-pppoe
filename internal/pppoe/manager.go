package pppoe

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/activity"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type Manager struct {
	db          *gorm.DB
	hub         *events.Hub
	dialSem     chan struct{}
	lineMu      sync.Map // lineId → *sync.Mutex
	rotateMu    sync.Map // sessionId → *sync.Mutex (chống concurrent rotate cùng 1 session)
	dialTimeout time.Duration
	rotateNewIP bool
}

var M *Manager

func Init(db *gorm.DB, hub *events.Hub, dialConcurrent int, rotateNewIP bool) {
	if dialConcurrent <= 0 {
		dialConcurrent = 5
	}
	M = &Manager{
		db:          db,
		hub:         hub,
		dialSem:     make(chan struct{}, dialConcurrent),
		dialTimeout: 30 * time.Second,
		rotateNewIP: rotateNewIP,
	}
}

func (m *Manager) lineLock(lineId uint) *sync.Mutex {
	mu, _ := m.lineMu.LoadOrStore(lineId, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// Dial — blocking. Returns error nếu dial fail. Cập nhật session.Status / .Iface / .IP.
func (m *Manager) Dial(sessionID uint) error {
	var sess models.Session
	if err := m.db.First(&sess, sessionID).Error; err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	var line models.Line
	if err := m.db.First(&line, sess.LineId).Error; err != nil {
		return fmt.Errorf("load line: %w", err)
	}

	// Anti-lockout: nếu session đang error vì AuthNak → KHÔNG retry tự động
	// (chỉ caller manual rotate mới override). Caller phải reset status trước khi Dial lại.
	if sess.Status == models.StatusError && strings.Contains(strings.ToLower(sess.LastError), "authnak") {
		return fmt.Errorf("session %d locked (last error: %s) — manual rotate required", sessionID, sess.LastError)
	}

	mu := m.lineLock(line.Id)
	mu.Lock()
	defer mu.Unlock()

	m.dialSem <- struct{}{}
	defer func() { <-m.dialSem }()

	m.setStatus(&sess, models.StatusDialing, "")

	activity.Info(activity.CategoryDial, "start",
		fmt.Sprintf("Bắt đầu quay số session #%d (line: %s, iface: %s)", sess.Id, line.Name, line.Iface),
		activity.SessionId(sess.Id), activity.LineId(line.Id),
		activity.F("iface", line.Iface), activity.F("isp_user", sess.Username), activity.F("mac", sess.MAC))

	if sess.MAC != "" {
		if err := ensureMacvlan(line.Iface, MacvlanName(sess.Id), sess.MAC); err != nil {
			m.setStatus(&sess, models.StatusError, "macvlan: "+err.Error())
			activity.Error(activity.CategoryDial, "macvlan_fail",
				fmt.Sprintf("Tạo macvlan thất bại cho session #%d: %s", sess.Id, err.Error()),
				activity.SessionId(sess.Id), activity.LineId(line.Id), activity.F("error", err.Error()))
			return err
		}
	}

	if err := WritePeerFile(&line, &sess); err != nil {
		m.setStatus(&sess, models.StatusError, err.Error())
		activity.Error(activity.CategoryDial, "peer_file_fail",
			fmt.Sprintf("Ghi peer file thất bại cho session #%d: %s", sess.Id, err.Error()),
			activity.SessionId(sess.Id), activity.LineId(line.Id), activity.F("error", err.Error()))
		return err
	}

	peerName := fmt.Sprintf("bp-sess-%d", sess.Id)
	// pppd updetach → process tự detach sau khi link UP. Timeout chặt bởi exec.
	ctx, cancel := context.WithTimeout(context.Background(), m.dialTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/pppd", "call", peerName, "updetach").CombinedOutput()
	if err != nil {
		errMsg := strings.TrimSpace(string(out))
		if errMsg == "" {
			errMsg = err.Error()
		}
		shortMsg := classifyPppdError(errMsg)
		m.setStatus(&sess, models.StatusError, shortMsg)
		activity.Error(activity.CategoryDial, "pppd_fail",
			fmt.Sprintf("pppd thất bại session #%d: %s", sess.Id, shortMsg),
			activity.SessionId(sess.Id), activity.LineId(line.Id),
			activity.F("reason", shortMsg), activity.F("pppd_output", truncateStr(errMsg, 500)))
		return fmt.Errorf("pppd: %s", shortMsg)
	}

	// Poll iface ppp<unit> UP
	ifaceName := fmt.Sprintf("ppp%d", sess.PppUnit)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if IsIfaceUp(ifaceName) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !IsIfaceUp(ifaceName) {
		m.hangupPeer(peerName)
		errMsg := "iface " + ifaceName + " did not come UP"
		m.setStatus(&sess, models.StatusError, errMsg)
		activity.Error(activity.CategoryDial, "iface_not_up",
			fmt.Sprintf("Session #%d: interface %s không UP sau 15s", sess.Id, ifaceName),
			activity.SessionId(sess.Id), activity.LineId(line.Id), activity.F("iface", ifaceName))
		return fmt.Errorf("%s", errMsg)
	}

	wanIP := IfaceIPv4(ifaceName)
	if IsBlockedISPIp(wanIP) {
		// ISP cấp IP CGNAT/private → proxy không có IP công cộng riêng.
		// Hangup + đánh dấu error để watchdog redial (có thể lần sau BRAS cho IP khác).
		m.hangupPeer(peerName)
		errMsg := fmt.Sprintf("ISP cấp IP CGNAT/private (%s) - không dùng được", wanIP)
		m.setStatus(&sess, models.StatusError, errMsg)
		activity.Error(activity.CategoryDial, "blocked_ip",
			fmt.Sprintf("Session #%d bị từ chối vì ISP cấp IP CGNAT/private: %s", sess.Id, wanIP),
			activity.SessionId(sess.Id), activity.LineId(line.Id), activity.F("blocked_ip", wanIP))
		return fmt.Errorf("%s", errMsg)
	}

	// Reply-path policy routing + MSS clamp: client vào THẲNG public IP trên
	// pppN → reply phải quay lại pppN (không ra default route). Không fatal nếu
	// lỗi: egress qua line vẫn chạy, chỉ inbound trực tiếp bị ảnh hưởng.
	if err := ApplyReplyRouting(ifaceName, wanIP, sess.PppUnit); err != nil {
		log.Printf("[routing] session %d apply reply routing failed: %v", sess.Id, err)
		activity.Warn(activity.CategoryDial, "routing_fail",
			fmt.Sprintf("Session #%d: thiết lập policy-routing thất bại: %s", sess.Id, err.Error()),
			activity.SessionId(sess.Id), activity.LineId(line.Id),
			activity.F("iface", ifaceName), activity.F("ip", wanIP), activity.F("error", err.Error()))
	}

	now := time.Now()
	sess.Iface = ifaceName
	sess.IP = wanIP
	sess.Status = models.StatusConnected
	sess.LastError = ""
	sess.ConnectedAt = &now
	sess.RotateFailCount = 0
	sess.ReconnectAttempts = 0
	sess.NextReconnectAt = nil
	m.db.Save(&sess)
	m.publish("session.status", map[string]any{
		"session_id": sess.Id, "status": sess.Status, "iface": sess.Iface, "ip": sess.IP,
	})
	activity.Info(activity.CategoryDial, "connected",
		fmt.Sprintf("Session #%d kết nối thành công: iface=%s, IP=%s", sess.Id, ifaceName, wanIP),
		activity.SessionId(sess.Id), activity.LineId(line.Id),
		activity.F("iface", ifaceName), activity.F("ip", wanIP))

	// Public IP async — không block dial result
	go m.probePublicIP(sess.Id, ifaceName)
	return nil
}


func (m *Manager) probePublicIP(sessionID uint, iface string) {
	ip, err := PublicIPViaIface(context.Background(), iface, 10*time.Second)
	if err != nil || ip == "" {
		activity.Warn(activity.CategoryDial, "public_ip_probe_fail",
			fmt.Sprintf("Session #%d: không kiểm tra được public IP qua %s", sessionID, iface),
			activity.SessionId(sessionID), activity.F("iface", iface),
			activity.F("error", fmt.Sprintf("%v", err)))
		return
	}
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Update("public_ip", ip)
	m.publish("session.public_ip", map[string]any{"session_id": sessionID, "public_ip": ip})
	activity.Info(activity.CategoryDial, "public_ip_ok",
		fmt.Sprintf("Session #%d có public IP: %s", sessionID, ip),
		activity.SessionId(sessionID), activity.F("public_ip", ip))
}

// Hangup — kill pppd process cho session này. Idempotent.
func (m *Manager) Hangup(sessionID uint) error {
	var sess models.Session
	if err := m.db.First(&sess, sessionID).Error; err != nil {
		return err
	}
	oldIP := sess.IP
	peerName := fmt.Sprintf("bp-sess-%d", sess.Id)
	m.hangupPeer(peerName)
	// Dọn policy routing + MSS clamp (IP sắp mất → rule thành rác)
	RemoveReplyRouting(sess.Iface, sess.PppUnit)
	if sess.Iface != "" {
		_ = exec.Command("ip", "link", "set", sess.Iface, "down").Run()
	}
	sess.Status = models.StatusDisconnected
	sess.Iface = ""
	sess.IP = ""
	m.db.Save(&sess)
	m.publish("session.status", map[string]any{"session_id": sess.Id, "status": sess.Status})
	activity.Info(activity.CategoryDial, "hangup",
		fmt.Sprintf("Session #%d đã ngắt (IP cũ: %s)", sess.Id, oldIP),
		activity.SessionId(sess.Id), activity.LineId(sess.LineId), activity.F("old_ip", oldIP))
	return nil
}

func (m *Manager) hangupPeer(peerName string) {
	_ = exec.Command("pkill", "-TERM", "-f", "pppd call "+peerName).Run()
	time.Sleep(800 * time.Millisecond)
	_ = exec.Command("pkill", "-KILL", "-f", "pppd call "+peerName).Run()
}

// Rotate — hangup → sleep BRAS settle → dial. Trả old_ip / new_ip.
func (m *Manager) Rotate(sessionID uint) (oldIP, newIP string, err error) {
	muIf, _ := m.rotateMu.LoadOrStore(sessionID, &sync.Mutex{})
	mu := muIf.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	var sess models.Session
	if err := m.db.First(&sess, sessionID).Error; err != nil {
		return "", "", err
	}
	oldIP = sess.PublicIP
	activity.Info(activity.CategoryRotate, "start",
		fmt.Sprintf("Bắt đầu đổi IP session #%d (IP cũ: %s)", sessionID, oldIP),
		activity.SessionId(sessionID), activity.LineId(sess.LineId), activity.F("old_ip", oldIP))
	_ = m.Hangup(sessionID)
	time.Sleep(3 * time.Second) // BRAS settle
	// Clear error trước khi redial (override AuthNak lockout: caller chịu trách nhiệm)
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Updates(map[string]any{"status": models.StatusDisconnected, "last_error": ""})
	now := time.Now()
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Update("last_rotate_at", &now)
	if err := m.Dial(sessionID); err != nil {
		m.db.Model(&models.Session{}).Where("id = ?", sessionID).Update("rotate_fail_count", gorm.Expr("rotate_fail_count + 1"))
		activity.Error(activity.CategoryRotate, "fail",
			fmt.Sprintf("Đổi IP session #%d thất bại: %s", sessionID, err.Error()),
			activity.SessionId(sessionID), activity.LineId(sess.LineId),
			activity.F("old_ip", oldIP), activity.F("error", err.Error()))
		return oldIP, "", err
	}
	m.db.First(&sess, sessionID)
	newIP = sess.PublicIP
	m.publish("session.rotate", map[string]any{
		"session_id": sessionID, "old_ip": oldIP, "new_ip": newIP, "same_ip": oldIP == newIP,
	})
	if oldIP != "" && oldIP == newIP {
		activity.Warn(activity.CategoryRotate, "same_ip",
			fmt.Sprintf("Session #%d đổi IP nhưng nhận lại IP cũ: %s", sessionID, newIP),
			activity.SessionId(sessionID), activity.LineId(sess.LineId),
			activity.F("old_ip", oldIP), activity.F("new_ip", newIP))
	} else {
		activity.Info(activity.CategoryRotate, "ok",
			fmt.Sprintf("Session #%d đã đổi IP: %s → %s", sessionID, oldIP, newIP),
			activity.SessionId(sessionID), activity.LineId(sess.LineId),
			activity.F("old_ip", oldIP), activity.F("new_ip", newIP))
	}
	return oldIP, newIP, nil
}

// StartWatchdog — 20s tick: redial nếu connected nhưng iface không UP.
func (m *Manager) StartWatchdog(ctx context.Context) {
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.reconcileOnce()
			}
		}
	}()
}

// StartAutoRotate — 30s tick: tìm các session có AutoRotateSeconds>0 đã đến hạn,
// rotate song song với concurrency 3 để tránh BRAS storm. Mốc tính chu kỳ là
// LastRotateAt (fallback CreatedAt nếu chưa rotate lần nào).
func (m *Manager) StartAutoRotate(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.autoRotateOnce()
			}
		}
	}()
}

func (m *Manager) autoRotateOnce() {
	now := time.Now()
	var sessions []models.Session
	m.db.Where("auto_rotate_seconds > 0 AND auto_rotate_paused = ? AND status = ?", false, models.StatusConnected).Find(&sessions)
	if len(sessions) == 0 {
		return
	}
	sem := make(chan struct{}, 3) // tối đa 3 rotate đồng thời
	var wg sync.WaitGroup
	for _, s := range sessions {
		baseline := s.CreatedAt
		if s.LastRotateAt != nil {
			baseline = *s.LastRotateAt
		}
		dueAt := baseline.Add(time.Duration(s.AutoRotateSeconds) * time.Second)
		if dueAt.After(now) {
			continue
		}
		sid := s.Id
		interval := s.AutoRotateSeconds
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			log.Printf("[auto-rotate] session %d due (interval=%ds, last=%v)", sid, interval, baseline.Format(time.RFC3339))
			activity.Info(activity.CategoryRotate, "auto_due",
				fmt.Sprintf("Session #%d đến hạn auto-rotate (chu kỳ %ds)", sid, interval),
				activity.SessionId(sid), activity.F("interval_seconds", interval))
			if _, _, err := m.Rotate(sid); err != nil {
				log.Printf("[auto-rotate] session %d failed, pausing auto-rotate: %v", sid, err)
				m.db.Model(&models.Session{}).Where("id = ?", sid).Updates(map[string]any{
					"auto_rotate_paused": true,
					"rotate_fail_count":  gorm.Expr("rotate_fail_count + 1"),
				})
				m.publish("session.auto_rotate_paused", map[string]any{
					"session_id": sid, "reason": err.Error(),
				})
				activity.Warn(activity.CategoryRotate, "auto_paused",
					fmt.Sprintf("Session #%d auto-rotate bị tạm dừng do lỗi: %s", sid, err.Error()),
					activity.SessionId(sid), activity.F("error", err.Error()))
			}
		}()
	}
	wg.Wait()
}

func (m *Manager) reconcileOnce() {
	var sessions []models.Session
	m.db.Where("status = ?", models.StatusConnected).Find(&sessions)
	for _, s := range sessions {
		// 1) Demote session "connected" nhưng dùng IP CGNAT/private — không có ích cho proxy
		if IsBlockedISPIp(s.IP) {
			log.Printf("[watchdog] session %d IP %s nằm trong dải bị chặn — demote", s.Id, s.IP)
			m.hangupPeer(fmt.Sprintf("bp-sess-%d", s.Id))
			errMsg := fmt.Sprintf("ISP cấp IP CGNAT/private (%s) - không dùng được", s.IP)
			m.db.Model(&models.Session{}).Where("id = ?", s.Id).Updates(map[string]any{
				"status":     models.StatusError,
				"last_error": errMsg,
			})
			m.publish("session.status", map[string]any{
				"session_id": s.Id, "status": models.StatusError, "error": errMsg,
			})
			activity.Warn(activity.CategoryWatchdog, "demote_blocked_ip",
				fmt.Sprintf("Session #%d bị watchdog demote vì IP %s nằm trong dải bị chặn", s.Id, s.IP),
				activity.SessionId(s.Id), activity.F("blocked_ip", s.IP))
			continue
		}
		if s.Iface == "" {
			continue
		}
		if IsIfaceUp(s.Iface) {
			continue
		}
		log.Printf("[watchdog] session %d iface %s DOWN — redial", s.Id, s.Iface)
		activity.Warn(activity.CategoryWatchdog, "iface_down_redial",
			fmt.Sprintf("Session #%d: interface %s đã DOWN, watchdog quay số lại", s.Id, s.Iface),
			activity.SessionId(s.Id), activity.F("iface", s.Iface))
		go func(sid uint) {
			if err := m.Dial(sid); err != nil {
				log.Printf("[watchdog] redial session %d failed: %v", sid, err)
			}
		}(s.Id)
	}

	m.reconnectErrorSessions()
}

func (m *Manager) reconnectErrorSessions() {
	if !db.GetSettingBool("reconnect_enabled", true) {
		return
	}
	maxRetries := db.GetSettingInt("reconnect_max_retries", 5)
	pauseMinutes := db.GetSettingInt("reconnect_pause_minutes", 60)

	now := time.Now()
	var errSessions []models.Session
	m.db.Where(
		"status IN (?, ?) AND reconnect_attempts < ? AND (next_reconnect_at IS NULL OR next_reconnect_at <= ?)",
		models.StatusError, models.StatusDisconnected, maxRetries, now,
	).Find(&errSessions)

	for _, s := range errSessions {
		sid := s.Id
		attempt := s.ReconnectAttempts + 1
		log.Printf("[reconnect] session %d attempt %d/%d", sid, attempt, maxRetries)
		activity.Info(activity.CategoryReconnect, "attempt",
			fmt.Sprintf("Session #%d: reconnect lần %d/%d", sid, attempt, maxRetries),
			activity.SessionId(sid), activity.F("attempt", attempt), activity.F("max", maxRetries))

		if err := m.Dial(sid); err != nil {
			nextAt := now.Add(time.Duration(pauseMinutes) * time.Minute)
			m.db.Model(&models.Session{}).Where("id = ?", sid).Updates(map[string]any{
				"reconnect_attempts": attempt,
				"next_reconnect_at":  nextAt,
			})
			log.Printf("[reconnect] session %d fail %d/%d, next try at %s: %v",
				sid, attempt, maxRetries, nextAt.Format("15:04:05"), err)
			m.publish("session.reconnect_failed", map[string]any{
				"session_id": sid, "attempt": attempt, "max": maxRetries,
				"next_at": nextAt.Format(time.RFC3339), "error": err.Error(),
			})
			lvl := activity.Warn
			if attempt >= maxRetries {
				lvl = activity.Error
			}
			lvl(activity.CategoryReconnect, "fail",
				fmt.Sprintf("Session #%d reconnect lần %d/%d thất bại, thử lại lúc %s: %s",
					sid, attempt, maxRetries, nextAt.Format("15:04:05"), err.Error()),
				activity.SessionId(sid), activity.F("attempt", attempt),
				activity.F("max", maxRetries), activity.F("next_at", nextAt.Format(time.RFC3339)),
				activity.F("error", err.Error()))
		} else {
			var updated models.Session
			m.db.First(&updated, sid)
			log.Printf("[reconnect] session %d reconnected OK (IP=%s)", sid, updated.IP)
			m.publish("session.reconnect_ok", map[string]any{
				"session_id": sid, "ip": updated.IP,
			})
			activity.Info(activity.CategoryReconnect, "ok",
				fmt.Sprintf("Session #%d reconnect thành công (IP=%s)", sid, updated.IP),
				activity.SessionId(sid), activity.F("ip", updated.IP),
				activity.F("attempt", attempt))
		}
	}
}

func isPhysicalNIC(name string) bool {
	for _, prefix := range []string{"lo", "ppp", "mvbp", "mv-", "docker", "veth", "br-", "tun", "tap", "wg", "virbr"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	// Sysfs check: NIC physical phải có /sys/class/net/<name>/device (PCI).
	// Loại bỏ VLAN, macvlan, bridge derivatives (đều ở /sys/devices/virtual/net/).
	if _, err := os.Stat("/sys/class/net/" + name + "/device"); err != nil {
		return false
	}
	return true
}

func (m *Manager) EnsurePhysicalNICsUp() {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("[nic-up] failed to list interfaces: %v", err)
		return
	}
	for _, i := range ifaces {
		if !isPhysicalNIC(i.Name) {
			continue
		}
		if i.Flags&net.FlagUp != 0 {
			continue
		}
		if err := exec.Command("ip", "link", "set", i.Name, "up").Run(); err != nil {
			log.Printf("[nic-up] failed to bring up %s: %v", i.Name, err)
		} else {
			log.Printf("[nic-up] brought up %s", i.Name)
		}
	}
}

// RestoreState — boot: dial pending sessions (status=connected nhưng iface không UP).
func (m *Manager) RestoreState() {
	var sessions []models.Session
	m.db.Where("status IN (?, ?)", models.StatusConnected, models.StatusDialing).Find(&sessions)
	for _, s := range sessions {
		if s.Iface != "" && IsIfaceUp(s.Iface) {
			continue // adopt iface đang UP
		}
		log.Printf("[restore] re-dial session %d (was %s)", s.Id, s.Status)
		go func(sid uint) {
			_ = m.Dial(sid)
		}(s.Id)
	}
}

func (m *Manager) setStatus(sess *models.Session, status, errMsg string) {
	sess.Status = status
	sess.LastError = errMsg
	m.db.Save(sess)
	m.publish("session.status", map[string]any{
		"session_id": sess.Id, "status": status, "error": errMsg,
	})
}

func (m *Manager) publish(t string, payload map[string]any) {
	if m.hub != nil {
		m.hub.Publish(t, payload)
	}
}

func ensureMacvlan(parent, name, mac string) error {
	// Idempotent: nếu link đã có thì del + tạo lại với MAC mới
	_ = exec.Command("ip", "link", "del", name).Run()
	if err := exec.Command("ip", "link", "add", "link", parent, "name", name, "type", "macvlan", "mode", "bridge").Run(); err != nil {
		return fmt.Errorf("macvlan add: %w", err)
	}
	if mac != "" {
		_ = exec.Command("ip", "link", "set", name, "address", mac).Run()
	}
	if err := exec.Command("ip", "link", "set", name, "up").Run(); err != nil {
		return fmt.Errorf("macvlan up: %w", err)
	}
	return nil
}

// AllocPppUnit — tìm ppp_unit chưa dùng (0..999).
func AllocPppUnit(db *gorm.DB) (int, error) {
	for u := 0; u < 1000; u++ {
		var n int64
		db.Model(&models.Session{}).Where("ppp_unit = ?", u).Count(&n)
		if n == 0 {
			return u, nil
		}
	}
	return 0, fmt.Errorf("no free ppp_unit")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// classifyPppdError — extract short hint từ pppd verbose log.
// Helper để DB last_error ngắn gọn + user dễ debug.
func classifyPppdError(raw string) string {
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(low, "authnak"), strings.Contains(low, "authentication failed"):
		return "AuthNak (cred không khớp BRAS)"
	case strings.Contains(low, "service-name") && strings.Contains(low, "padt"):
		return "PADT (BRAS terminate session)"
	case strings.Contains(low, "no service") || strings.Contains(low, "service-name unmatched"):
		return "Service-Name không khớp BRAS"
	case strings.Contains(low, "signal: killed"):
		// Trường hợp pppd bị context cancel — thường là PADI/PADO/PAP không xong trong 30s
		if strings.Contains(low, "recv pppoe discovery v1t1 pado") {
			return "PADO received but PAP timeout (cred bất hợp lệ hoặc BRAS không respond auth)"
		}
		if strings.Contains(low, "send pppoe discovery v1t1 padi") {
			return "PADI sent, no PADO response (NIC không nối BRAS hoặc cáp lỏng)"
		}
		return "pppd timeout (context killed)"
	case strings.Contains(low, "no buffer space"):
		return "kernel buffer full"
	default:
		// Fallback: cắt 200 ký tự
		return truncate(strings.ReplaceAll(raw, "\n", " | "), 200)
	}
}
