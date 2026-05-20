package pppoe

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

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

	if sess.MAC != "" {
		if err := ensureMacvlan(line.Iface, MacvlanName(sess.Id), sess.MAC); err != nil {
			m.setStatus(&sess, models.StatusError, "macvlan: "+err.Error())
			return err
		}
	}

	if err := WritePeerFile(&line, &sess); err != nil {
		m.setStatus(&sess, models.StatusError, err.Error())
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
		m.setStatus(&sess, models.StatusError, "iface "+ifaceName+" did not come UP")
		return fmt.Errorf("iface %s did not come UP", ifaceName)
	}

	sess.Iface = ifaceName
	sess.IP = IfaceIPv4(ifaceName)
	sess.Status = models.StatusConnected
	sess.LastError = ""
	m.db.Save(&sess)
	m.publish("session.status", map[string]any{
		"session_id": sess.Id, "status": sess.Status, "iface": sess.Iface, "ip": sess.IP,
	})

	// Public IP async — không block dial result
	go m.probePublicIP(sess.Id, ifaceName)
	return nil
}

func (m *Manager) probePublicIP(sessionID uint, iface string) {
	ip, err := PublicIPViaIface(context.Background(), iface, 10*time.Second)
	if err != nil || ip == "" {
		return
	}
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Update("public_ip", ip)
	m.publish("session.public_ip", map[string]any{"session_id": sessionID, "public_ip": ip})
}

// Hangup — kill pppd process cho session này. Idempotent.
func (m *Manager) Hangup(sessionID uint) error {
	var sess models.Session
	if err := m.db.First(&sess, sessionID).Error; err != nil {
		return err
	}
	peerName := fmt.Sprintf("bp-sess-%d", sess.Id)
	m.hangupPeer(peerName)
	if sess.Iface != "" {
		_ = exec.Command("ip", "link", "set", sess.Iface, "down").Run()
	}
	sess.Status = models.StatusDisconnected
	sess.Iface = ""
	sess.IP = ""
	m.db.Save(&sess)
	m.publish("session.status", map[string]any{"session_id": sess.Id, "status": sess.Status})
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
	_ = m.Hangup(sessionID)
	time.Sleep(3 * time.Second) // BRAS settle
	// Clear error trước khi redial (override AuthNak lockout: caller chịu trách nhiệm)
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Updates(map[string]any{"status": models.StatusDisconnected, "last_error": ""})
	now := time.Now()
	m.db.Model(&models.Session{}).Where("id = ?", sessionID).Update("last_rotate_at", &now)
	if err := m.Dial(sessionID); err != nil {
		return oldIP, "", err
	}
	m.db.First(&sess, sessionID)
	newIP = sess.PublicIP
	m.publish("session.rotate", map[string]any{
		"session_id": sessionID, "old_ip": oldIP, "new_ip": newIP, "same_ip": oldIP == newIP,
	})
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
	m.db.Where("auto_rotate_seconds > 0 AND status = ?", models.StatusConnected).Find(&sessions)
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
			if _, _, err := m.Rotate(sid); err != nil {
				log.Printf("[auto-rotate] session %d failed: %v", sid, err)
			}
		}()
	}
	wg.Wait()
}

func (m *Manager) reconcileOnce() {
	var sessions []models.Session
	m.db.Where("status = ?", models.StatusConnected).Find(&sessions)
	for _, s := range sessions {
		if s.Iface == "" {
			continue
		}
		if IsIfaceUp(s.Iface) {
			continue
		}
		log.Printf("[watchdog] session %d iface %s DOWN — redial", s.Id, s.Iface)
		go func(sid uint) {
			if err := m.Dial(sid); err != nil {
				log.Printf("[watchdog] redial session %d failed: %v", sid, err)
			}
		}(s.Id)
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
