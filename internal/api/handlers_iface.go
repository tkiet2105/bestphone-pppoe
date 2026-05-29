package api

import (
	"context"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

type ifaceInfo struct {
	Name       string   `json:"name"`
	MAC        string   `json:"mac"`
	IPs        []string `json:"ips"`
	State      string   `json:"state"`       // "up" | "down"
	Carrier    bool     `json:"carrier"`     // /sys/class/net/<i>/carrier == 1
	SpeedMbps  int      `json:"speed_mbps"`  // /sys/class/net/<i>/speed (-1 = unknown)
	UsedByLine uint     `json:"used_by_line,omitempty"`
	UsedByName string   `json:"used_by_name,omitempty"`
}

func isPhysical(name string) bool {
	// Loại bỏ prefix virtual quen thuộc (kernel/docker/openvpn/wireguard)
	for _, prefix := range []string{"ppp", "mvbp", "mv-", "docker", "veth", "br-", "tun", "tap", "wg", "virbr"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	// Quy tắc cứng: NIC physical phải có /sys/class/net/<name>/device (PCI
	// device backing). VLAN, macvlan, bridge derivatives nằm trong
	// /sys/devices/virtual/net/, không có /device subdir → loại bỏ.
	// Nhờ đó các iface kiểu enp2s0_root, enp2s0_L_1, enp2s0_v201, enp2s0L1
	// được lọc, picker chỉ còn các NIC PCI thật sự.
	if _, err := os.Stat("/sys/class/net/" + name + "/device"); err != nil {
		return false
	}
	return true
}

// ListIfaces — physical NIC + carrier + speed + cross-ref line đang dùng.
func ListIfaces(c *gin.Context) {
	ifaces, err := net.Interfaces()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	// Build lookup line iface → line
	var lines []models.Line
	db.DB.Find(&lines)
	lineByIface := make(map[string]models.Line, len(lines))
	for _, l := range lines {
		lineByIface[l.Iface] = l
	}

	out := make([]ifaceInfo, 0, len(ifaces))
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || !isPhysical(i.Name) {
			continue
		}
		info := ifaceInfo{Name: i.Name, MAC: i.HardwareAddr.String(), State: "down", SpeedMbps: -1}
		if i.Flags&net.FlagUp != 0 {
			info.State = "up"
		}
		// carrier
		if data, err := os.ReadFile("/sys/class/net/" + i.Name + "/carrier"); err == nil {
			info.Carrier = strings.TrimSpace(string(data)) == "1"
		}
		if data, err := os.ReadFile("/sys/class/net/" + i.Name + "/speed"); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				info.SpeedMbps = n
			}
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					info.IPs = append(info.IPs, ip4.String())
				}
			}
		}
		if l, ok := lineByIface[i.Name]; ok {
			info.UsedByLine = l.Id
			info.UsedByName = l.Name
		}
		out = append(out, info)
	}
	ok(c, out)
}

type probeResult struct {
	Name        string `json:"name"`
	PADO        bool   `json:"pado"`
	ACName      string `json:"ac_name,omitempty"`
	ACSourceMAC string `json:"ac_source_mac,omitempty"`
	Error       string `json:"error,omitempty"`
}

var (
	probeMu      sync.Mutex
	probeRunning bool
)

var reACName = regexp.MustCompile(`Access-Concentrator:\s*(.+)`)
var reMAC = regexp.MustCompile(`AC-Ethernet-Address:\s*([0-9a-fA-F:]+)`)

// ProbeIfaces — passive PPPoE discovery (PADI/PADO handshake, NO auth → an toàn không AuthNak).
// Sequential per iface để tránh shared-switch race. Timeout 5s/iface.
// Body optional: {ifaces:[...]} — nếu rỗng, probe tất cả NIC physical có carrier.
func ProbeIfaces(c *gin.Context) {
	probeMu.Lock()
	if probeRunning {
		probeMu.Unlock()
		fail(c, 429, "probe already running")
		return
	}
	probeRunning = true
	probeMu.Unlock()
	defer func() {
		probeMu.Lock()
		probeRunning = false
		probeMu.Unlock()
	}()

	var req struct {
		Ifaces []string `json:"ifaces"`
	}
	_ = c.ShouldBindJSON(&req)

	// Build candidate list
	var candidates []string
	if len(req.Ifaces) > 0 {
		candidates = req.Ifaces
	} else {
		ifs, _ := net.Interfaces()
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback != 0 || !isPhysical(i.Name) {
				continue
			}
			if i.Flags&net.FlagUp == 0 {
				continue
			}
			data, _ := os.ReadFile("/sys/class/net/" + i.Name + "/carrier")
			if strings.TrimSpace(string(data)) != "1" {
				continue
			}
			candidates = append(candidates, i.Name)
		}
	}

	out := make([]probeResult, 0, len(candidates))
	for _, iface := range candidates {
		r := probeResult{Name: iface}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		cmd := exec.CommandContext(ctx, "pppoe-discovery", "-I", iface)
		raw, err := cmd.CombinedOutput()
		cancel()
		out_s := string(raw)
		if m := reACName.FindStringSubmatch(out_s); m != nil {
			r.PADO = true
			r.ACName = strings.TrimSpace(m[1])
		}
		if m := reMAC.FindStringSubmatch(out_s); m != nil {
			r.ACSourceMAC = strings.ToLower(strings.TrimSpace(m[1]))
		}
		if !r.PADO && err != nil {
			// pppoe-discovery exit 1 khi timeout — không phải error thực
			if strings.Contains(out_s, "Timeout") || ctx.Err() != nil {
				r.Error = "no PADO (timeout)"
			} else {
				r.Error = strings.TrimSpace(out_s)
				if r.Error == "" {
					r.Error = err.Error()
				}
			}
		}
		out = append(out, r)
		time.Sleep(1500 * time.Millisecond) // tránh BRAS throttle PADI dồn dập
	}
	ok(c, out)
}
