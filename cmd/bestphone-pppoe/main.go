package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/activity"
	"github.com/tkiet2105/bestphone-pppoe/internal/api"
	"github.com/tkiet2105/bestphone-pppoe/internal/config"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
	"github.com/tkiet2105/bestphone-pppoe/internal/pppoe"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

// appVersion phải khớp với /VERSION ở repo root. Bump cả 2 cùng lúc khi ready-to-ship.
const appVersion = "1.9.4"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("bestphone-pppoe v%s starting...", appVersion)

	cfg := config.Load()

	if err := db.Init(cfg.DBPath); err != nil {
		log.Fatalf("db init: %v", err)
	}
	if err := db.SeedAdminToken(cfg.AdminToken); err != nil {
		log.Printf("seed admin token: %v", err)
	}
	if err := db.SeedAdminUser(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Printf("seed admin user: %v", err)
	}
	db.SeedDefaultSettings()

	hub := events.NewHub()
	api.SetEventHub(hub)
	api.AppVersion = appVersion

	activity.Init(db.DB, hub)
	pppoe.Init(db.DB, hub, cfg.DialConcurrent, cfg.RotateNewIP)
	proxysrv.Init(db.DB, hub, cfg.ProxyPortMin, cfg.ProxyPortMax)

	pppoe.M.EnsurePhysicalNICsUp()

	// Restore: dial pending sessions + start proxy listeners đã ở status=running.
	pppoe.M.RestoreState()
	proxysrv.M.RestoreAll()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pppoe.M.StartWatchdog(ctx)
	pppoe.M.StartAutoRotate(ctx)
	proxysrv.M.StartCredCleanup(ctx)

	// ActivityLog cleanup: chạy mỗi 6h, xóa entry cũ hơn 30 ngày
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		activity.CleanupOld(30 * 24 * time.Hour) // chạy ngay lần đầu
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				activity.CleanupOld(30 * 24 * time.Hour)
			}
		}
	}()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		// Skip noisy SSE log
		if p.Path == "/api/v1/events" {
			return ""
		}
		return fmt.Sprintf("[gin] %s %s %d %v\n", p.Method, p.Path, p.StatusCode, p.Latency)
	}))
	api.RegisterRoutes(r)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      r,
		ReadTimeout:  20 * time.Second,
		WriteTimeout: 0, // SSE needs unlimited
		IdleTimeout:  120 * time.Second,
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		log.Printf("shutdown signal received")
		cancel()
		shCtx, shCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shCancel()
		_ = srv.Shutdown(shCtx)
	}()

	log.Printf("bestphone-pppoe v%s ready on %s (proxy port range %d-%d)", appVersion, cfg.ListenAddr, cfg.ProxyPortMin, cfg.ProxyPortMax)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
	log.Printf("bestphone-pppoe stopped")
}
