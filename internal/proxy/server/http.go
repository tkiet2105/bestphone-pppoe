package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// handleHTTP — HTTP CONNECT (tunneling) hoặc HTTP forward proxy.
// `firstByte` đã được peek (1 ASCII char đầu của method).
func (l *listener) handleHTTP(conn net.Conn, firstByte byte) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	br := bufio.NewReader(io.MultiReader(strings.NewReader(string([]byte{firstByte})), conn))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if l.creds.hasAuth() {
		user, pass, ok := parseProxyAuth(req.Header.Get("Proxy-Authorization"))
		if !ok || !l.creds.match([]byte(user), []byte(pass)) {
			writeHTTPStatus(conn, 407, "Proxy Authentication Required", "Proxy-Authenticate: Basic realm=\"bestphone-pppoe\"")
			return
		}
	}

	if req.Method == http.MethodConnect {
		host := req.URL.Host
		if host == "" {
			host = req.Host
		}
		// host có dạng "example.com:443" — strip port để filter
		hostOnly := host
		if h, _, err := net.SplitHostPort(host); err == nil {
			hostOnly = h
		}
		if l.rules != nil && !l.rules.allowed(hostOnly) {
			writeHTTPStatus(conn, 403, "Forbidden by ruleset", "")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		upstream, err := dialBound(ctx, l.iface(), "tcp", host)
		if err != nil {
			writeHTTPStatus(conn, 502, "Bad Gateway", "")
			return
		}
		defer upstream.Close()
		if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		_ = conn.SetDeadline(time.Time{})
		pipe(conn, upstream)
		return
	}

	// HTTP forward (GET/POST proxy):
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	hostOnly := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostOnly = h
	}
	if l.rules != nil && !l.rules.allowed(hostOnly) {
		writeHTTPStatus(conn, 403, "Forbidden by ruleset", "")
		return
	}
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	upstream, err := dialBound(ctx, l.iface(), "tcp", host)
	if err != nil {
		writeHTTPStatus(conn, 502, "Bad Gateway", "")
		return
	}
	defer upstream.Close()

	// Re-build request line theo origin-form (path-only) cho upstream
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.RequestURI = ""
	if err := req.Write(upstream); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	pipe(conn, upstream)
}

func parseProxyAuth(h string) (user, pass string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(h, prefix) {
		return "", "", false
	}
	dec, err := base64.StdEncoding.DecodeString(h[len(prefix):])
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(dec), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeHTTPStatus(conn net.Conn, code int, status, extra string) {
	body := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: 0\r\n", code, status)
	if extra != "" {
		body += extra + "\r\n"
	}
	body += "Connection: close\r\n\r\n"
	_, _ = conn.Write([]byte(body))
}
