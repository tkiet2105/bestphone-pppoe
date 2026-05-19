package server

import (
	"crypto/subtle"
	"sync"
)

type Cred struct {
	Username string
	Password string
}

type credSet struct {
	mu    sync.RWMutex
	creds []Cred
}

func (c *credSet) set(creds []Cred) {
	c.mu.Lock()
	c.creds = creds
	c.mu.Unlock()
}

func (c *credSet) hasAuth() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.creds) > 0
}

// match — constant-time, iterate all (không early-return → tránh timing leak).
func (c *credSet) match(user, pass []byte) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	matched := 0
	for _, cr := range c.creds {
		u := subtle.ConstantTimeCompare(user, []byte(cr.Username))
		p := subtle.ConstantTimeCompare(pass, []byte(cr.Password))
		matched |= u & p
	}
	return matched == 1
}
