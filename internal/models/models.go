// Package models — DB schema (gorm tags). Plain structs, không có method (logic ở service).
package models

import "time"

type Line struct {
	Id          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Iface       string    `gorm:"size:32;not null" json:"iface"`
	UseMacvlan  bool      `gorm:"default:false" json:"use_macvlan"`
	MaxSessions int       `gorm:"default:8" json:"max_sessions"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	StatusDisconnected = "disconnected"
	StatusDialing      = "dialing"
	StatusConnected    = "connected"
	StatusError        = "error"
)

type Session struct {
	Id           uint       `gorm:"primaryKey" json:"id"`
	LineId       uint       `gorm:"index;not null" json:"line_id"`
	PppUnit      int        `gorm:"uniqueIndex;not null" json:"ppp_unit"`
	Iface        string     `gorm:"size:32" json:"iface"`
	Username     string     `gorm:"size:128;not null" json:"username"`
	Password     string     `gorm:"size:256;not null" json:"password"`
	MAC          string     `gorm:"size:32;index" json:"mac"`
	Status       string     `gorm:"size:16;default:'disconnected'" json:"status"`
	IP           string     `gorm:"size:64" json:"ip"`
	PublicIP     string     `gorm:"size:64" json:"public_ip"`
	LastError    string     `gorm:"size:256" json:"last_error"`
	LastRotateAt *time.Time `json:"last_rotate_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Proxy struct {
	Id        uint   `gorm:"primaryKey" json:"id"`
	SessionId uint   `gorm:"uniqueIndex;not null" json:"session_id"`
	Port      int    `gorm:"uniqueIndex;not null" json:"port"`
	Status    string `gorm:"size:16;default:'stopped'" json:"status"`
}

type ProxyCredential struct {
	Id       uint   `gorm:"primaryKey" json:"id"`
	ProxyId  uint   `gorm:"index;not null" json:"proxy_id"`
	Label    string `gorm:"size:64" json:"label"`
	Username string `gorm:"size:128;not null" json:"username"`
	Password string `gorm:"size:256;not null" json:"password"`
	Enabled  bool   `gorm:"default:true" json:"enabled"`
}

type Token struct {
	Id        uint      `gorm:"primaryKey" json:"id"`
	Token     string    `gorm:"size:128;uniqueIndex;not null" json:"-"`
	Label     string    `gorm:"size:64" json:"label"`
	CreatedAt time.Time `json:"created_at"`
}
