package api

import (
	"fmt"
	"net"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

// ExportProxies — endpoint "lấy proxy nhanh".
// GET /proxies/export?type=public|local&format=text|json
//
// text/plain (default): mỗi enabled credential = 1 dòng "ip:port:user:pass".
// json: cấu trúc nested cho UI/scripting.
func ExportProxies(c *gin.Context) {
	typ := c.DefaultQuery("type", "public")
	format := c.DefaultQuery("format", "text")

	var proxies []models.Proxy
	db.DB.Where("status = ?", "running").Order("id ASC").Find(&proxies)

	localIP := firstLocalIPv4()

	type credOut struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Label    string `json:"label"`
	}
	type proxyOut struct {
		SessionId uint      `json:"session_id"`
		ProxyId   uint      `json:"proxy_id"`
		Port      int       `json:"port"`
		IP        string    `json:"ip"`
		LineId    uint      `json:"line_id"`
		Iface     string    `json:"iface"`
		Creds     []credOut `json:"creds"`
	}

	out := make([]proxyOut, 0, len(proxies))
	var textLines []string

	for _, p := range proxies {
		var sess models.Session
		if err := db.DB.First(&sess, p.SessionId).Error; err != nil {
			continue
		}
		var ip string
		if typ == "local" {
			ip = localIP
		} else {
			ip = sess.PublicIP
			if ip == "" {
				ip = localIP // fallback
			}
		}
		var creds []models.ProxyCredential
		db.DB.Where("proxy_id = ? AND enabled = ?", p.Id, true).Order("id ASC").Find(&creds)
		credList := make([]credOut, 0, len(creds))
		for _, cr := range creds {
			credList = append(credList, credOut{Username: cr.Username, Password: cr.Password, Label: cr.Label})
			textLines = append(textLines, fmt.Sprintf("%s:%d:%s:%s", ip, p.Port, cr.Username, cr.Password))
		}
		out = append(out, proxyOut{
			SessionId: sess.Id,
			ProxyId:   p.Id,
			Port:      p.Port,
			IP:        ip,
			LineId:    sess.LineId,
			Iface:     sess.Iface,
			Creds:     credList,
		})
	}

	if format == "json" {
		ok(c, out)
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(200, strings.Join(textLines, "\n")+"\n")
}

func firstLocalIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagUp == 0 {
			continue
		}
		// Skip ppp* (đó là PPPoE iface, không phải LAN local)
		if strings.HasPrefix(ifc.Name, "ppp") || strings.HasPrefix(ifc.Name, "mvbp") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}
