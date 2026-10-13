package server

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type Manager struct {
	db          *gorm.DB
	hub         *events.Hub
	mu          sync.Mutex
	listeners   map[uint]*listener // proxyID → listener
	globalRules []Rule             // compiled global rules cache
	globalMu    sync.RWMutex
	portMin     int
	portMax     int
	rootCtx     context.Context
	rootCancel  context.CancelFunc
}

var M *Manager

func Init(db *gorm.DB, hub *events.Hub, portMin, portMax int) {
	ctx, cancel := context.WithCancel(context.Background())
	M = &Manager{
		db:         db,
		hub:        hub,
		listeners:  make(map[uint]*listener),
		portMin:    portMin,
		portMax:    portMax,
		rootCtx:    ctx,
		rootCancel: cancel,
	}
}

// AllocPort — chọn random 1 port chưa dùng trong [portMin, portMax].
//
// Lý do random thay vì sequential: ports liên tiếp (30000, 30001, ...) dễ bị
// scan từ ngoài. Random rải đều trong range khiến attacker phải scan rộng hơn
// nhiều mới tìm thấy listener.
//
// Dùng crypto/rand để không predict được (math/rand seed mặc định cố định).
func (m *Manager) AllocPort() (int, error) {
	used := make(map[int]bool)
	var ports []int
	m.db.Model(&models.Proxy{}).Pluck("port", &ports)
	for _, p := range ports {
		used[p] = true
	}
	free := make([]int, 0, m.portMax-m.portMin+1)
	for p := m.portMin; p <= m.portMax; p++ {
		if !used[p] {
			free = append(free, p)
		}
	}
	// Port trống trong DB vẫn có thể đang bị socket khác giữ (listener cũ chưa
	// đóng, hoặc cổng outbound ephemeral nằm trong dải) → thử bind trước khi chọn,
	// tránh session connected mà proxy "bind: address already in use".
	for tries := 0; len(free) > 0 && tries < 64; tries++ {
		idx, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(free))))
		if err != nil {
			return 0, fmt.Errorf("rand pick: %w", err)
		}
		i := int(idx.Int64())
		port := free[i]
		if ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port))); err == nil {
			_ = ln.Close()
			return port, nil
		}
		free[i] = free[len(free)-1]
		free = free[:len(free)-1]
	}
	return 0, fmt.Errorf("server hết cổng proxy trống trong [%d,%d] — nới PROXY_PORT_MIN/MAX", m.portMin, m.portMax)
}

// Start — load proxy + session, bind net.Listen, spawn acceptLoop. Idempotent.
func (m *Manager) Start(proxyID uint) error {
	m.mu.Lock()
	if _, ok := m.listeners[proxyID]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	var p models.Proxy
	if err := m.db.First(&p, proxyID).Error; err != nil {
		return fmt.Errorf("load proxy: %w", err)
	}
	if p.Port == 0 {
		port, err := m.AllocPort()
		if err != nil {
			return err
		}
		p.Port = port
		m.db.Save(&p)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(p.Port)))
	if err != nil && errors.Is(err, syscall.EADDRINUSE) && !m.hasClaimCreds(p.Id) {
		// Cổng bị kết nối khác chiếm (thường là cổng tạm outbound — dải tạm của
		// Linux 32768–60999 chồng dải proxy). Proxy CHƯA có cred khách → đổi cổng
		// được. Proxy ĐÃ có khách thì giữ cổng (Central đã lưu), báo lỗi để thử lại.
		if port, aerr := m.AllocPort(); aerr == nil {
			if ln2, lerr := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port))); lerr == nil {
				log.Printf("[proxysrv] proxy %d: cổng %d bị chiếm, đổi sang %d", p.Id, p.Port, port)
				p.Port = port
				m.db.Model(&models.Proxy{}).Where("id = ?", p.Id).Update("port", port)
				ln, err = ln2, nil
			}
		}
	}
	if err != nil {
		return fmt.Errorf("listen :%d: %w", p.Port, err)
	}

	ctx, cancel := context.WithCancel(m.rootCtx)
	l := &listener{
		proxyID:   p.Id,
		sessionID: p.SessionId,
		port:      p.Port,
		ifaceFn:   m.makeIfaceFn(p.SessionId),
		creds:     &credSet{},
		rules:     &ruleSet{},
		ln:        ln,
		ctx:       ctx,
		cancel:    cancel,
	}
	l.creds.set(m.loadCreds(p.Id))
	l.rules.set(m.mergedRulesFor(p.SessionId))

	l.wg.Add(1)
	go l.acceptLoop()

	m.mu.Lock()
	m.listeners[p.Id] = l
	m.mu.Unlock()

	m.db.Model(&models.Proxy{}).Where("id = ?", p.Id).Update("status", "running")
	log.Printf("[proxysrv] proxy %d listening :%d", p.Id, p.Port)
	if m.hub != nil {
		m.hub.Publish("proxy.started", map[string]any{"proxy_id": p.Id, "port": p.Port})
	}
	return nil
}

func (m *Manager) Stop(proxyID uint) error {
	m.mu.Lock()
	l, ok := m.listeners[proxyID]
	if ok {
		delete(m.listeners, proxyID)
	}
	m.mu.Unlock()
	if !ok {
		m.db.Model(&models.Proxy{}).Where("id = ?", proxyID).Update("status", "stopped")
		return nil
	}
	l.closeOnce.Do(func() {
		l.cancel()
		_ = l.ln.Close()
	})
	l.wg.Wait()
	m.db.Model(&models.Proxy{}).Where("id = ?", proxyID).Update("status", "stopped")
	log.Printf("[proxysrv] proxy %d stopped", proxyID)
	if m.hub != nil {
		m.hub.Publish("proxy.stopped", map[string]any{"proxy_id": proxyID})
	}
	return nil
}

// ReloadCreds — hot swap credentials, không close listener.
func (m *Manager) ReloadCreds(proxyID uint) {
	m.mu.Lock()
	l, ok := m.listeners[proxyID]
	m.mu.Unlock()
	if !ok {
		return
	}
	l.creds.set(m.loadCreds(proxyID))
	if m.hub != nil {
		m.hub.Publish("proxy.cred_changed", map[string]any{"proxy_id": proxyID})
	}
}

func (m *Manager) RestoreAll() {
	m.LoadGlobalRules()
	var proxies []models.Proxy
	m.db.Where("status = ?", "running").Find(&proxies)
	var busy []uint
	for _, p := range proxies {
		if err := m.Start(p.Id); err != nil {
			log.Printf("[proxysrv] restore proxy %d failed: %v", p.Id, err)
			if errors.Is(err, syscall.EADDRINUSE) {
				busy = append(busy, p.Id)
			}
		}
	}
	// Proxy của khách mà cổng đang bị kết nối tạm chiếm: kết nối đó sẽ đóng
	// (TIME-WAIT ~60s) → thử lại nền tới 15 phút thay vì để proxy chết lặng lẽ
	// trong khi DB vẫn ghi running.
	if len(busy) > 0 {
		go m.retryStart(busy, 30*time.Second, 30)
	}
}

func (m *Manager) retryStart(ids []uint, every time.Duration, times int) {
	for i := 0; i < times && len(ids) > 0; i++ {
		select {
		case <-m.rootCtx.Done():
			return
		case <-time.After(every):
		}
		left := ids[:0]
		for _, id := range ids {
			if err := m.Start(id); err != nil {
				left = append(left, id)
			} else {
				log.Printf("[proxysrv] proxy %d lên lại sau khi cổng được nhả", id)
			}
		}
		ids = left
	}
	for _, id := range ids {
		log.Printf("[proxysrv] ⚠ proxy %d vẫn không bind được cổng sau 15 phút", id)
	}
}

// hasClaimCreds — proxy đang có cred khách (claim) → không được tự đổi cổng.
func (m *Manager) hasClaimCreds(proxyID uint) bool {
	var n int64
	m.db.Model(&models.ProxyCredential{}).Where("proxy_id = ? AND i_user_id != ''", proxyID).Count(&n)
	return n > 0
}

// ReserveProxyPorts — đưa dải cổng proxy vào net.ipv4.ip_local_reserved_ports để
// kernel KHÔNG cấp chúng làm cổng tạm cho kết nối outbound (dải tạm mặc định
// 32768–60999 chồng 30000–40000; proxy5 10/10/2026 có 648 kết nối ra ngoài nằm
// trong dải proxy → listener "address already in use"). Giữ nguyên giá trị cũ.
func ReserveProxyPorts(portMin, portMax int) {
	const path = "/proc/sys/net/ipv4/ip_local_reserved_ports"
	cur, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[proxysrv] không đọc được %s: %v", path, err)
		return
	}
	rng := fmt.Sprintf("%d-%d", portMin, portMax)
	val := strings.TrimSpace(string(cur))
	if strings.Contains(","+val+",", ","+rng+",") {
		return
	}
	if val != "" {
		val += ","
	}
	if err := os.WriteFile(path, []byte(val+rng), 0o644); err != nil {
		log.Printf("[proxysrv] ⚠ không đặt được ip_local_reserved_ports=%s: %v", rng, err)
		return
	}
	log.Printf("[proxysrv] ip_local_reserved_ports += %s", rng)
}

func (m *Manager) StartCredCleanup(ctx context.Context) {
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.cleanExpiredCreds()
			}
		}
	}()
}

func (m *Manager) cleanExpiredCreds() {
	now := time.Now()
	var expired []models.ProxyCredential
	m.db.Where("expires_at IS NOT NULL AND expires_at <= ?", now).Find(&expired)
	if len(expired) == 0 {
		return
	}

	affectedProxies := make(map[uint]bool)
	for _, cr := range expired {
		affectedProxies[cr.ProxyId] = true
	}

	res := m.db.Where("expires_at IS NOT NULL AND expires_at <= ?", now).Delete(&models.ProxyCredential{})
	log.Printf("[cred-cleanup] deleted %d expired credentials", res.RowsAffected)

	for pid := range affectedProxies {
		m.ReloadCreds(pid)
		if m.hub != nil {
			m.hub.Publish("proxy.cred_expired", map[string]any{"proxy_id": pid})
		}
	}
}

func (m *Manager) IsRunning(proxyID uint) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.listeners[proxyID]
	return ok
}

func (m *Manager) loadCreds(proxyID uint) []Cred {
	var rows []models.ProxyCredential
	m.db.Where("proxy_id = ? AND enabled = ?", proxyID, true).Find(&rows)
	out := make([]Cred, 0, len(rows))
	for _, r := range rows {
		out = append(out, Cred{Username: r.Username, Password: r.Password})
	}
	return out
}

func (m *Manager) makeIfaceFn(sessionID uint) func() string {
	return func() string {
		var s models.Session
		if err := m.db.Select("iface").First(&s, sessionID).Error; err != nil {
			return ""
		}
		return s.Iface
	}
}

// LoadGlobalRules — query DB và cache global rules.
func (m *Manager) LoadGlobalRules() {
	var rs []models.AccessRule
	m.db.Where("scope = ?", models.RuleScopeGlobal).Find(&rs)
	compiled := compile(rs)
	m.globalMu.Lock()
	m.globalRules = compiled
	m.globalMu.Unlock()
}

// ReloadGlobalRules — re-query + push xuống tất cả listener.
func (m *Manager) ReloadGlobalRules() {
	m.LoadGlobalRules()
	m.mu.Lock()
	listeners := make([]*listener, 0, len(m.listeners))
	for _, l := range m.listeners {
		listeners = append(listeners, l)
	}
	m.mu.Unlock()
	for _, l := range listeners {
		l.rules.set(m.mergedRulesFor(l.sessionID))
	}
	if m.hub != nil {
		m.hub.Publish("rules.global_changed", map[string]any{})
	}
}

// ReloadSessionRules — push session-scoped rules tới listener của session đó.
func (m *Manager) ReloadSessionRules(sessionID uint) {
	m.mu.Lock()
	var target *listener
	for _, l := range m.listeners {
		if l.sessionID == sessionID {
			target = l
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		return
	}
	target.rules.set(m.mergedRulesFor(sessionID))
	if m.hub != nil {
		m.hub.Publish("rules.session_changed", map[string]any{"session_id": sessionID})
	}
}

// mergedRulesFor — global rules + session-scope rules.
func (m *Manager) mergedRulesFor(sessionID uint) []Rule {
	m.globalMu.RLock()
	out := make([]Rule, 0, len(m.globalRules))
	out = append(out, m.globalRules...)
	m.globalMu.RUnlock()

	var rs []models.AccessRule
	m.db.Where("scope = ? AND session_id = ?", models.RuleScopeSession, sessionID).Find(&rs)
	out = append(out, compile(rs)...)
	return out
}
