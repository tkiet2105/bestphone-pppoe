// Package config — env-driven runtime config. KHÔNG đụng file system ngoài đọc env.
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr     string // default 0.0.0.0:8080
	DBPath         string // default /var/lib/bestphone-pppoe/data.db
	ProxyPortMin   int    // default 30000
	ProxyPortMax   int    // default 40000
	AdminToken     string // seed token (insert nếu DB rỗng)
	DialConcurrent int    // global cap dial pppoe đồng thời
	RotateNewIP    bool   // BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP=1
}

func Load() *Config {
	c := &Config{
		ListenAddr:     env("LISTEN_ADDR", "0.0.0.0:8080"),
		DBPath:         env("DB_PATH", "/var/lib/bestphone-pppoe/data.db"),
		ProxyPortMin:   envInt("PROXY_PORT_MIN", 30000),
		ProxyPortMax:   envInt("PROXY_PORT_MAX", 40000),
		AdminToken:     strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
		DialConcurrent: envInt("DIAL_CONCURRENT", 5),
		RotateNewIP:    os.Getenv("BESTPHONE_PPPOE_ROTATE_REQUIRE_NEW_IP") == "1",
	}
	if c.ProxyPortMax < c.ProxyPortMin {
		c.ProxyPortMax = c.ProxyPortMin + 1000
	}
	return c
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
