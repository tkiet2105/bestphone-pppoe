package api

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

// GetStats — tổng hợp metrics cho dashboard "Tổng quan".
// Đọc /proc trực tiếp để tránh thêm dependency. CPU đo bằng delta 100ms (nhẹ).
// Endpoint này được poll mỗi 5s → giữ logic gọn, tránh allocation lớn.
func GetStats(c *gin.Context) {
	// ── Hệ thống ───────────────────────────────────────────────
	cpu := cpuPercent()
	memTotalKB, memAvailKB := memInfo()
	var memUsedKB uint64
	memPct := 0.0
	if memTotalKB >= memAvailKB {
		memUsedKB = memTotalKB - memAvailKB
	}
	if memTotalKB > 0 {
		memPct = 100 * float64(memUsedKB) / float64(memTotalKB)
	}
	l1, l5, l15 := loadAvg()
	sysUp := sysUptime()
	diskTotal, diskUsed, diskPct := diskUsage("/var")

	// ── DB counts ──────────────────────────────────────────────
	var (
		linesCount, sessTotal, proxyTotal, proxyRunning int64
		credTotal, ruleTotal, ruleGlobal, ruleSess      int64
		userCount, apiTokenCount                        int64
	)
	db.DB.Model(&models.Line{}).Count(&linesCount)
	db.DB.Model(&models.Session{}).Count(&sessTotal)
	db.DB.Model(&models.Proxy{}).Count(&proxyTotal)
	db.DB.Model(&models.Proxy{}).Where("status = ?", "running").Count(&proxyRunning)
	db.DB.Model(&models.ProxyCredential{}).Count(&credTotal)
	db.DB.Model(&models.AccessRule{}).Count(&ruleTotal)
	db.DB.Model(&models.AccessRule{}).Where("scope = ?", "global").Count(&ruleGlobal)
	db.DB.Model(&models.AccessRule{}).Where("scope = ?", "session").Count(&ruleSess)
	db.DB.Model(&models.User{}).Count(&userCount)
	db.DB.Model(&models.Token{}).Where("user_id IS NULL").Count(&apiTokenCount)

	// Sessions theo trạng thái
	type statusRow struct {
		Status string
		Count  int64
	}
	var statusRows []statusRow
	db.DB.Model(&models.Session{}).Select("status, count(*) as count").Group("status").Scan(&statusRows)
	byStatus := map[string]int64{"connected": 0, "dialing": 0, "error": 0, "disconnected": 0}
	for _, r := range statusRows {
		byStatus[r.Status] = r.Count
	}

	// Sessions có public IP (đã quay số thành công, có IP từ ISP)
	var sessWithPublicIP int64
	db.DB.Model(&models.Session{}).Where("public_ip <> '' AND status = ?", models.StatusConnected).Count(&sessWithPublicIP)

	ok(c, gin.H{
		"service": gin.H{
			"version":        AppVersion,
			"uptime_seconds": int64(time.Since(startedAt).Seconds()),
			"started_at":     startedAt.Unix(),
		},
		"system": gin.H{
			"cpu_percent":     round1(cpu),
			"load_1":          round2(l1),
			"load_5":          round2(l5),
			"load_15":         round2(l15),
			"memory_total_mb": memTotalKB / 1024,
			"memory_used_mb":  memUsedKB / 1024,
			"memory_percent":  round1(memPct),
			"uptime_seconds":  sysUp,
			"disk_path":       "/var",
			"disk_total_gb":   round1(diskTotal),
			"disk_used_gb":    round1(diskUsed),
			"disk_percent":    round1(diskPct),
			"num_cpu":         runtime.NumCPU(),
			"num_goroutine":   runtime.NumGoroutine(),
		},
		"lines": gin.H{"total": linesCount},
		"sessions": gin.H{
			"total":           sessTotal,
			"by_status":       byStatus,
			"with_public_ip":  sessWithPublicIP,
		},
		"proxies": gin.H{
			"total":   proxyTotal,
			"running": proxyRunning,
			"stopped": proxyTotal - proxyRunning,
		},
		"credentials": gin.H{"total": credTotal},
		"rules": gin.H{
			"total":   ruleTotal,
			"global":  ruleGlobal,
			"session": ruleSess,
		},
		"auth": gin.H{
			"users":      userCount,
			"api_tokens": apiTokenCount,
		},
	})
}

// ── helpers ───────────────────────────────────────────────────

// cpuTimes — đọc dòng đầu /proc/stat: "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
func cpuTimes() (idle, total uint64, err error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, fmt.Errorf("/proc/stat empty")
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("/proc/stat invalid")
	}
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 { // idle field
			idle = v
		}
	}
	return
}

func cpuPercent() float64 {
	i1, t1, e := cpuTimes()
	if e != nil {
		return 0
	}
	time.Sleep(100 * time.Millisecond)
	i2, t2, e := cpuTimes()
	if e != nil {
		return 0
	}
	dt := t2 - t1
	di := i2 - i1
	if dt == 0 {
		return 0
	}
	return 100 * float64(dt-di) / float64(dt)
}

func memInfo() (totalKB, availKB uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := sc.Text()
		switch {
		case strings.HasPrefix(ln, "MemTotal:"):
			fmt.Sscanf(ln, "MemTotal: %d kB", &totalKB)
		case strings.HasPrefix(ln, "MemAvailable:"):
			fmt.Sscanf(ln, "MemAvailable: %d kB", &availKB)
		}
	}
	return
}

func loadAvg() (l1, l5, l15 float64) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fs := strings.Fields(string(b))
	if len(fs) >= 3 {
		l1, _ = strconv.ParseFloat(fs[0], 64)
		l5, _ = strconv.ParseFloat(fs[1], 64)
		l15, _ = strconv.ParseFloat(fs[2], 64)
	}
	return
}

func sysUptime() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fs := strings.Fields(string(b))
	if len(fs) >= 1 {
		f, _ := strconv.ParseFloat(fs[0], 64)
		return uint64(f)
	}
	return 0
}

func diskUsage(path string) (totalGB, usedGB, pct float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	if total < free {
		return
	}
	used := total - free
	totalGB = float64(total) / (1 << 30)
	usedGB = float64(used) / (1 << 30)
	if total > 0 {
		pct = 100 * float64(used) / float64(total)
	}
	return
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
