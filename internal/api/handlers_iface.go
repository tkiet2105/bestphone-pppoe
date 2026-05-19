package api

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

type ifaceInfo struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac"`
	IPs   []string `json:"ips"`
	State string   `json:"state"` // up | down
}

// ListIfaces — physical NIC đang có trên máy. Filter bỏ loopback, ppp*, mvbp*,
// docker*, veth*, br-*, tun*, tap* — tức chỉ giữ NIC vật lý có thể dial PPPoE.
func ListIfaces(c *gin.Context) {
	ifaces, err := net.Interfaces()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	out := make([]ifaceInfo, 0, len(ifaces))
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Filter virtual / bestphone runtime ifaces
		skip := false
		for _, prefix := range []string{"ppp", "mvbp", "mv-", "docker", "veth", "br-", "tun", "tap", "wg", "virbr"} {
			if strings.HasPrefix(i.Name, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		info := ifaceInfo{Name: i.Name, MAC: i.HardwareAddr.String(), State: "down"}
		if i.Flags&net.FlagUp != 0 {
			info.State = "up"
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					info.IPs = append(info.IPs, ip4.String())
				}
			}
		}
		out = append(out, info)
	}
	ok(c, out)
}
