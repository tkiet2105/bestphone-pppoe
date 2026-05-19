package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type Manager struct {
	db         *gorm.DB
	hub        *events.Hub
	mu         sync.Mutex
	listeners  map[uint]*listener // proxyID → listener
	portMin    int
	portMax    int
	rootCtx    context.Context
	rootCancel context.CancelFunc
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

// AllocPort — tìm port chưa dùng. Public để API tạo Proxy có thể reserve trước.
func (m *Manager) AllocPort() (int, error) {
	used := make(map[int]bool)
	var ports []int
	m.db.Model(&models.Proxy{}).Pluck("port", &ports)
	for _, p := range ports {
		used[p] = true
	}
	for p := m.portMin; p <= m.portMax; p++ {
		if used[p] {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free port in [%d,%d]", m.portMin, m.portMax)
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
	if err != nil {
		return fmt.Errorf("listen :%d: %w", p.Port, err)
	}

	ctx, cancel := context.WithCancel(m.rootCtx)
	l := &listener{
		proxyID: p.Id,
		port:    p.Port,
		ifaceFn: m.makeIfaceFn(p.SessionId),
		creds:   &credSet{},
		ln:      ln,
		ctx:     ctx,
		cancel:  cancel,
	}
	l.creds.set(m.loadCreds(p.Id))

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
	var proxies []models.Proxy
	m.db.Where("status = ?", "running").Find(&proxies)
	for _, p := range proxies {
		if err := m.Start(p.Id); err != nil {
			log.Printf("[proxysrv] restore proxy %d failed: %v", p.Id, err)
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
