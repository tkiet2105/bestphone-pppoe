package api

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// GetLogs — tail journal entries cho service / pppd. Read-only.
//
// Query params:
//   - source: backend | pppd | all  (default all)
//   - lines:  số dòng cuối (default 200, max 2000)
//   - since:  "5 minutes ago" / "1 hour ago" (default "30 minutes ago")
//   - filter: regex/keyword grep
func GetLogs(c *gin.Context) {
	source := c.DefaultQuery("source", "all")
	lines := c.DefaultQuery("lines", "200")
	since := c.DefaultQuery("since", "30 minutes ago")
	filter := c.Query("filter")

	n, _ := strconv.Atoi(lines)
	if n <= 0 || n > 2000 {
		n = 200
	}

	args := []string{"--no-pager", "-n", strconv.Itoa(n), "--since", since, "-o", "short-iso"}
	switch source {
	case "backend":
		args = append(args, "-u", "bestphone-pppoe.service")
	case "pppd":
		args = append(args, "SYSLOG_IDENTIFIER=pppd")
	case "all":
		// merge backend + pppd: filter qua _COMM hoặc _SYSTEMD_UNIT
		args = append(args, "-u", "bestphone-pppoe.service", "+", "SYSLOG_IDENTIFIER=pppd")
	default:
		fail(c, 400, "source must be backend|pppd|all")
		return
	}

	cmd := exec.Command("journalctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fail(c, 500, "journalctl: "+err.Error()+" / "+string(out))
		return
	}

	raw := string(out)
	if filter != "" {
		var keep []string
		f := strings.ToLower(filter)
		for _, ln := range strings.Split(raw, "\n") {
			if strings.Contains(strings.ToLower(ln), f) {
				keep = append(keep, ln)
			}
		}
		raw = strings.Join(keep, "\n")
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(200, raw)
}
