package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"LISTEN_ADDR", "DB_PATH", "PROXY_PORT_MIN", "PROXY_PORT_MAX", "DIAL_CONCURRENT", "ADMIN_TOKEN", "ADMIN_USERNAME", "ADMIN_PASSWORD", "BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP"} {
		os.Unsetenv(k)
	}
	c := Load()
	if c.ListenAddr != "0.0.0.0:8080" {
		t.Fatalf("expected 0.0.0.0:8080, got %s", c.ListenAddr)
	}
	if c.DBPath != "/var/lib/bestphone-pppoe/data.db" {
		t.Fatalf("unexpected db path: %s", c.DBPath)
	}
	if c.ProxyPortMin != 30000 {
		t.Fatalf("expected 30000, got %d", c.ProxyPortMin)
	}
	if c.ProxyPortMax != 40000 {
		t.Fatalf("expected 40000, got %d", c.ProxyPortMax)
	}
	if c.DialConcurrent != 5 {
		t.Fatalf("expected 5, got %d", c.DialConcurrent)
	}
	if c.RotateNewIP {
		t.Fatal("RotateNewIP should default false")
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("LISTEN_ADDR", "127.0.0.1:9090")
	os.Setenv("PROXY_PORT_MIN", "50000")
	os.Setenv("PROXY_PORT_MAX", "51000")
	os.Setenv("DIAL_CONCURRENT", "10")
	os.Setenv("BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP", "1")
	defer func() {
		os.Unsetenv("LISTEN_ADDR")
		os.Unsetenv("PROXY_PORT_MIN")
		os.Unsetenv("PROXY_PORT_MAX")
		os.Unsetenv("DIAL_CONCURRENT")
		os.Unsetenv("BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP")
	}()
	c := Load()
	if c.ListenAddr != "127.0.0.1:9090" {
		t.Fatalf("expected 127.0.0.1:9090, got %s", c.ListenAddr)
	}
	if c.ProxyPortMin != 50000 {
		t.Fatalf("expected 50000, got %d", c.ProxyPortMin)
	}
	if c.ProxyPortMax != 51000 {
		t.Fatalf("expected 51000, got %d", c.ProxyPortMax)
	}
	if c.DialConcurrent != 10 {
		t.Fatalf("expected 10, got %d", c.DialConcurrent)
	}
	if !c.RotateNewIP {
		t.Fatal("RotateNewIP should be true")
	}
}

func TestPortMaxLessThanMin(t *testing.T) {
	os.Setenv("PROXY_PORT_MIN", "50000")
	os.Setenv("PROXY_PORT_MAX", "40000")
	defer func() {
		os.Unsetenv("PROXY_PORT_MIN")
		os.Unsetenv("PROXY_PORT_MAX")
	}()
	c := Load()
	if c.ProxyPortMax < c.ProxyPortMin {
		t.Fatalf("max should be auto-corrected: min=%d max=%d", c.ProxyPortMin, c.ProxyPortMax)
	}
	if c.ProxyPortMax != 51000 {
		t.Fatalf("expected auto-corrected to 51000, got %d", c.ProxyPortMax)
	}
}
