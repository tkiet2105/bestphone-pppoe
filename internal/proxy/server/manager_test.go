package server

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

func setupTestMgr(t *testing.T, min, max int) *Manager {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := g.AutoMigrate(&models.Proxy{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &Manager{db: g, portMin: min, portMax: max}
}

func TestAllocPort_InRange(t *testing.T) {
	m := setupTestMgr(t, 30000, 30009)
	for i := 0; i < 10; i++ {
		p, err := m.AllocPort()
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		if p < 30000 || p > 30009 {
			t.Errorf("port %d out of range [30000,30009]", p)
		}
		m.db.Create(&models.Proxy{SessionId: uint(i + 1), Port: p, Status: "stopped"})
	}
	// Range exhausted
	if _, err := m.AllocPort(); err == nil {
		t.Errorf("expected error when range exhausted")
	}
}

func TestAllocPort_SkipsUsed(t *testing.T) {
	m := setupTestMgr(t, 30000, 30002)
	// Reserve 30000 and 30002 — only 30001 free
	m.db.Create(&models.Proxy{SessionId: 1, Port: 30000, Status: "stopped"})
	m.db.Create(&models.Proxy{SessionId: 2, Port: 30002, Status: "stopped"})
	p, err := m.AllocPort()
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if p != 30001 {
		t.Errorf("expected 30001 (only free port), got %d", p)
	}
}

func TestAllocPort_Randomness(t *testing.T) {
	// Với range 1000 port, 100 lần alloc liên tiếp (mỗi lần insert) phải không
	// theo thứ tự liên tiếp (sequential = bug cũ).
	m := setupTestMgr(t, 30000, 30999)
	var picks []int
	for i := 0; i < 100; i++ {
		p, err := m.AllocPort()
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		picks = append(picks, p)
		m.db.Create(&models.Proxy{SessionId: uint(i + 1), Port: p, Status: "stopped"})
	}
	// Kiểm tra không phải sequential: nếu sequential thì picks[i+1] = picks[i]+1 toàn bộ.
	sequential := true
	for i := 1; i < len(picks); i++ {
		if picks[i] != picks[i-1]+1 {
			sequential = false
			break
		}
	}
	if sequential {
		t.Errorf("AllocPort vẫn sequential: %v", picks[:10])
	}
	// Phải có ít nhất 1 cặp adjacent picks (i, i+1) cách nhau > 1 (rải đều)
	gaps := 0
	for i := 1; i < len(picks); i++ {
		d := picks[i] - picks[i-1]
		if d < 0 {
			d = -d
		}
		if d > 5 {
			gaps++
		}
	}
	if gaps < 50 {
		t.Errorf("ít gap > 5: %d/99 — có thể không random thực sự", gaps)
	}
}
