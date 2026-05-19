package api

import (
	"fmt"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tkiet2105/bestphone-pppoe/internal/events"
)

var eventHub *events.Hub

func SetEventHub(h *events.Hub) { eventHub = h }

// EventsSSE — Server-Sent Events stream. Bearer middleware đã verify, cho phép ?token=.
func EventsSSE(c *gin.Context) {
	if eventHub == nil {
		fail(c, 500, "event hub not initialized")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	sub, cancel := eventHub.Subscribe()
	defer cancel()

	clientGone := c.Writer.CloseNotify()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	c.SSEvent("hello", gin.H{"ts": time.Now().Unix()})
	c.Writer.Flush()

	c.Stream(func(w io.Writer) bool {
		select {
		case ev, ok := <-sub:
			if !ok {
				return false
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(ev.Data))
			return true
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			return true
		case <-clientGone:
			return false
		}
	})
}
