package pppoe

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

const peerDir = "/etc/ppp/peers"

// var (không const) để test trỏ sang thư mục tạm.
var (
	chapSecretsFile = "/etc/ppp/chap-secrets"
	papSecretsFile  = "/etc/ppp/pap-secrets"
)

var secretsMu sync.Mutex

// WritePeerFile — ghi /etc/ppp/peers/bp-sess-<id>. nodefaultroute để traffic
// KHÔNG bị add default route (bind explicit qua SO_BINDTODEVICE).
func WritePeerFile(line *models.Line, sess *models.Session) error {
	if err := os.MkdirAll(peerDir, 0o755); err != nil {
		return fmt.Errorf("mkdir peer dir: %w", err)
	}
	peerIface := line.Iface
	if sess.MAC != "" {
		peerIface = MacvlanName(sess.Id)
	}
	peerPath := filepath.Join(peerDir, fmt.Sprintf("bp-sess-%d", sess.Id))
	content := fmt.Sprintf(`# bestphone-pppoe peer — session_id=%d line_id=%d user=%q iface=%s mac=%q
# Auto-generated. Do not edit manually.

plugin rp-pppoe.so %s
unit %d
name "%s"
user "%s"
noipdefault
usepeerdns
persist
maxfail 0
holdoff 10
lcp-echo-interval 20
lcp-echo-failure 3
mtu 1492
mru 1492
nodefaultroute
noauth
debug
ipparam bp-sess-%d
`, sess.Id, line.Id, sess.Username, peerIface, sess.MAC, peerIface, sess.PppUnit, sess.Username, sess.Username, sess.Id)
	if err := os.WriteFile(peerPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write peer file: %w", err)
	}
	if err := upsertSecret(chapSecretsFile, sess.Username, sess.Password); err != nil {
		return fmt.Errorf("chap-secrets: %w", err)
	}
	if err := upsertSecret(papSecretsFile, sess.Username, sess.Password); err != nil {
		return fmt.Errorf("pap-secrets: %w", err)
	}
	return nil
}

func RemovePeerFile(sessionID uint) {
	_ = os.Remove(filepath.Join(peerDir, fmt.Sprintf("bp-sess-%d", sessionID)))
}

// RemoveMacvlan — xóa macvlan iface mvbp<sid> nếu tồn tại. Idempotent.
func RemoveMacvlan(sessionID uint) {
	_ = exec.Command("ip", "link", "del", MacvlanName(sessionID)).Run()
}

// MacvlanName — tên macvlan đảm bảo ngắn (<15 ký tự cho linux iface).
func MacvlanName(sid uint) string {
	return fmt.Sprintf("mvbp%d", sid)
}

func upsertSecret(path, username, password string) error {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	marker := fmt.Sprintf("\"%s\"", username)
	newLine := fmt.Sprintf("%-30s %-10s %-30s *", marker, "*", fmt.Sprintf("\"%s\"", password))
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), marker) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func removeSecret(path, username string) {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	marker := fmt.Sprintf("\"%s\"", username)
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), marker) {
			continue
		}
		out = append(out, l)
	}
	_ = os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600)
}

// RemoveSessionSecrets — gỡ dòng secret của username khỏi chap/pap-secrets,
// CHỈ KHI không còn line hay session nào dùng username đó.
//
// ⚠ Gọi SAU khi đã xoá session/line khỏi DB. Mọi session của 1 line dùng CHUNG
// tài khoản PPPoE của line → bản cũ gỡ vô điều kiện: xoá 1 session là mất secret
// của cả line, pppd `persist` của các session còn lại khi rớt tự quay lại sẽ
// AuthNak mãi (proxy5 10/10/2026: 2/2 line mất secret, 1899 AuthNak/48h).
func RemoveSessionSecrets(db *gorm.DB, username string) {
	if username == "" {
		return
	}
	var n int64
	db.Model(&models.Session{}).Where("username = ?", username).Count(&n)
	if n > 0 {
		return
	}
	db.Model(&models.Line{}).Where("username = ?", username).Count(&n)
	if n > 0 {
		return
	}
	removeSecret(chapSecretsFile, username)
	removeSecret(papSecretsFile, username)
}

// EnsureSecrets — ghi lại secret cho mọi tài khoản đang được session dùng
// (idempotent). Gọi lúc boot để tự lành secret đã bị bản cũ xoá nhầm, trước khi
// pppd `persist` cần quay lại.
func EnsureSecrets(db *gorm.DB) {
	type cred struct{ Username, Password string }
	var creds []cred
	db.Model(&models.Session{}).Distinct("username", "password").
		Where("username != '' AND password != ''").Find(&creds)
	for _, c := range creds {
		if err := upsertSecret(chapSecretsFile, c.Username, c.Password); err != nil {
			log.Printf("[secrets] chap upsert %s: %v", c.Username, err)
		}
		if err := upsertSecret(papSecretsFile, c.Username, c.Password); err != nil {
			log.Printf("[secrets] pap upsert %s: %v", c.Username, err)
		}
	}
}
