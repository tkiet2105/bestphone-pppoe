package server

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

// Cổng proxy bị socket khác chiếm (cổng tạm outbound): proxy chưa có khách → tự
// đổi cổng; proxy có cred khách → GIỮ cổng, trả EADDRINUSE để thử lại sau.
func TestStart_PortBusy(t *testing.T) {
	testutil.SetupTestDB(t)
	Init(db.DB, events.NewHub(), 57200, 57220)
	t.Cleanup(func() { M.rootCancel() })

	busy := func() (net.Listener, int) {
		ln, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln, ln.Addr().(*net.TCPAddr).Port
	}

	// 1) proxy không có khách
	ln1, p1 := busy()
	defer ln1.Close()
	free := models.Proxy{SessionId: 1, Port: p1, Status: "stopped"}
	db.DB.Create(&free)
	if err := M.Start(free.Id); err != nil {
		t.Fatalf("proxy không khách phải tự đổi cổng, got %v", err)
	}
	var got models.Proxy
	db.DB.First(&got, free.Id)
	if got.Port == p1 || got.Port < 57200 || got.Port > 57220 {
		t.Fatalf("cổng mới sai: %d (cũ %d)", got.Port, p1)
	}
	_ = M.Stop(free.Id)

	// 2) proxy có cred khách
	ln2, p2 := busy()
	defer ln2.Close()
	cust := models.Proxy{SessionId: 2, Port: p2, Status: "stopped"}
	db.DB.Create(&cust)
	db.DB.Create(&models.ProxyCredential{ProxyId: cust.Id, Label: "claim", Username: "u1", Password: "p1", Enabled: true, IUserId: "khach"})
	err := M.Start(cust.Id)
	if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("proxy có khách phải giữ cổng và báo EADDRINUSE, got %v", err)
	}
	var gotCust models.Proxy
	db.DB.First(&gotCust, cust.Id)
	if gotCust.Port != p2 {
		t.Fatalf("KHÔNG được đổi cổng proxy của khách: %d → %d", p2, gotCust.Port)
	}
}
