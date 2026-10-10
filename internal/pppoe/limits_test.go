package pppoe

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	"github.com/tkiet2105/bestphone-pppoe/internal/testutil"
)

// pkill -f "pppd call bp-sess-7" từng khớp cả bp-sess-70/700 → ngắt nhầm session khác.
func TestPeerPattern_NoPrefixMatch(t *testing.T) {
	re := regexp.MustCompile(peerPattern("bp-sess-7"))
	for _, s := range []string{"/usr/sbin/pppd call bp-sess-7 updetach", "/usr/sbin/pppd call bp-sess-7"} {
		if !re.MatchString(s) {
			t.Errorf("phải khớp %q", s)
		}
	}
	for _, s := range []string{"/usr/sbin/pppd call bp-sess-70 updetach", "/usr/sbin/pppd call bp-sess-705 updetach", "/usr/sbin/pppd call bp-sess-7123"} {
		if re.MatchString(s) {
			t.Errorf("KHÔNG được khớp %q", s)
		}
	}
}

// Xoá 1 session KHÔNG được gỡ secret khi line/session khác còn dùng chung tài khoản.
func TestRemoveSessionSecrets_KeepWhileShared(t *testing.T) {
	testutil.SetupTestDB(t)
	dir := t.TempDir()
	oldChap, oldPap := chapSecretsFile, papSecretsFile
	chapSecretsFile, papSecretsFile = filepath.Join(dir, "chap"), filepath.Join(dir, "pap")
	t.Cleanup(func() { chapSecretsFile, papSecretsFile = oldChap, oldPap })

	line := testutil.SeedLine(t) // username isp_user
	s1 := models.Session{LineId: line.Id, PppUnit: 1, Username: "isp_user", Password: "isp_pass"}
	s2 := models.Session{LineId: line.Id, PppUnit: 2, Username: "isp_user", Password: "isp_pass"}
	db.DB.Create(&s1)
	db.DB.Create(&s2)
	for _, f := range []string{chapSecretsFile, papSecretsFile} {
		if err := upsertSecret(f, "isp_user", "isp_pass"); err != nil {
			t.Fatal(err)
		}
	}
	has := func() bool {
		b, _ := os.ReadFile(papSecretsFile)
		c, _ := os.ReadFile(chapSecretsFile)
		return strings.Contains(string(b), `"isp_user"`) && strings.Contains(string(c), `"isp_user"`)
	}

	db.DB.Delete(&models.Session{}, s1.Id)
	RemoveSessionSecrets(db.DB, "isp_user")
	if !has() {
		t.Fatal("còn session s2 + line dùng tài khoản → secret phải giữ")
	}
	db.DB.Delete(&models.Session{}, s2.Id)
	RemoveSessionSecrets(db.DB, "isp_user")
	if !has() {
		t.Fatal("line còn dùng tài khoản → secret phải giữ")
	}
	db.DB.Delete(&models.Line{}, line.Id)
	RemoveSessionSecrets(db.DB, "isp_user")
	if has() {
		t.Fatal("không còn ai dùng → secret phải được gỡ")
	}

	// EnsureSecrets tự lành secret bị xoá nhầm.
	l2 := testutil.SeedLine(t)
	testutil.SeedSession(t, l2.Id)
	EnsureSecrets(db.DB)
	if !has() {
		t.Fatal("EnsureSecrets phải ghi lại secret cho session đang có")
	}
}

func TestAllocPppUnit_SkipsUsedAndReportsExhausted(t *testing.T) {
	testutil.SetupTestDB(t)
	line := testutil.SeedLine(t)
	old, oldExists := MaxUnit, ifaceExists
	t.Cleanup(func() { MaxUnit, ifaceExists = old, oldExists })
	MaxUnit = 4
	// ppp3 bị tiến trình ngoài DB giữ (pppd trùng/mồ côi) → phải bỏ qua.
	ifaceExists = func(name string) bool { return name == "ppp3" }
	for _, u := range []int{0, 1} {
		db.DB.Create(&models.Session{LineId: line.Id, PppUnit: u, Username: "x", Password: "y"})
	}
	u, err := AllocPppUnit(db.DB)
	if err != nil || u != 2 {
		t.Fatalf("expected unit 2, got %d err=%v", u, err)
	}
	db.DB.Create(&models.Session{LineId: line.Id, PppUnit: 2, Username: "x", Password: "y"})
	if _, err := AllocPppUnit(db.DB); err == nil || !strings.Contains(err.Error(), "hết ppp unit") {
		t.Fatalf("expected lỗi rõ 'hết ppp unit', got %v", err)
	}
}

// Output thật (proxy5 09/10/2026) lẫn PADT/PADI của máy khác cùng L2: phải ra lý do thật của pppd.
func TestClassifyPppdError_PrefersPppdConclusion(t *testing.T) {
	raw := `Plugin rp-pppoe.so loaded.
Send PPPOE Discovery V1T1 PADI session 0x0 length 12
 [service-name] [host-uniq  d7 b0 34 00]
Recv PPPOE Discovery V1T1 PADT session 0x3fd length 0
Recv PPPOE Discovery V1T1 PADI session 0x0 length 12
 [service-name] [host-uniq  f4 00 05 00]
Timeout waiting for PADO packets
Unable to complete PPPoE Discovery`
	if got := classifyPppdError(raw); !strings.HasPrefix(got, "No PADO") {
		t.Fatalf("expected No PADO, got %q", got)
	}
	if got := classifyPppdError("...\nrcvd [PAP AuthNak id=0x1 \"\"]\nPAP authentication failed"); !strings.HasPrefix(got, "AuthNak") {
		t.Fatalf("expected AuthNak, got %q", got)
	}
	long := strings.Repeat("x", 1000) + "TAIL-REASON"
	if got := headTail(long, 10, 20); !strings.HasSuffix(got, "TAIL-REASON") || len(got) > 40 {
		t.Fatalf("headTail phải giữ phần cuối, got %q", got)
	}
}
