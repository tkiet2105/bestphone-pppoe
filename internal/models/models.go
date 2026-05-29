// Package models — DB schema (gorm tags). Plain structs, không có method (logic ở service).
package models

import "time"

// Line — 1 line PPPoE = 1 NIC + 1 cặp ISP cred. Mirror Mode 2 schema.
// Tất cả session của line dùng chung Username/Password này (mỗi session khác
// MAC qua macvlan). BRAS treat như N gateway độc lập cùng account.
type Line struct {
	Id          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Iface       string    `gorm:"size:32;not null" json:"iface"`
	Username    string    `gorm:"size:128" json:"username"` // ISP PPPoE PAP/CHAP user
	Password    string    `gorm:"size:256" json:"password"` // ISP PPPoE password
	UseMacvlan  bool      `gorm:"default:false" json:"use_macvlan"`
	MaxSessions int       `gorm:"default:8" json:"max_sessions"`
	// CustomMacs — danh sách MAC user tự cấp (mỗi MAC 1 dòng, format
	// "aa:bb:cc:dd:ee:ff"). Khi tạo session, system pick MAC chưa dùng từ
	// pool này; nếu hết hoặc field rỗng → sinh ngẫu nhiên.
	CustomMacs string    `gorm:"type:text" json:"custom_macs"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	StatusDisconnected = "disconnected"
	StatusDialing      = "dialing"
	StatusConnected    = "connected"
	StatusError        = "error"
)

// Session type: phân biệt để claim đúng và giới hạn max creds/session.
// Mọi chức năng (rotate, change, ...) đều giống nhau giữa 3 type.
const (
	SessionTypeStatic   = "static"   // tĩnh, max 5 user
	SessionTypePrivate  = "private"  // riêng, max 1 user
	SessionTypeRotating = "rotating" // xoay, max 5 user
)

// MaxCredsForType — max active creds (đã claim) cho 1 session theo type.
func MaxCredsForType(t string) int {
	switch t {
	case SessionTypePrivate:
		return 1
	case SessionTypeStatic, SessionTypeRotating:
		return 5
	default:
		return 5
	}
}

// IsValidSessionType — kiểm tra type hợp lệ.
func IsValidSessionType(t string) bool {
	return t == SessionTypeStatic || t == SessionTypePrivate || t == SessionTypeRotating
}

// ParseMacs — tách chuỗi MAC pool (newline/comma/space separated) thành slice
// đã chuẩn hóa lowercase + bỏ entry rỗng. Không validate format ở đây.
func ParseMacs(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0)
	cur := make([]byte, 0, 17)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == ',' || c == ' ' || c == '\t' {
			flush()
			continue
		}
		// lowercase ASCII
		if c >= 'A' && c <= 'Z' {
			c = c + 32
		}
		cur = append(cur, c)
	}
	flush()
	return out
}

// IsValidMac — kiểm tra format "aa:bb:cc:dd:ee:ff" (6 octet hex, separator :).
func IsValidMac(m string) bool {
	if len(m) != 17 {
		return false
	}
	for i := 0; i < 17; i++ {
		c := m[i]
		if i%3 == 2 {
			if c != ':' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

type Session struct {
	Id                uint       `gorm:"primaryKey" json:"id"`
	LineId            uint       `gorm:"index;not null" json:"line_id"`
	PppUnit           int        `gorm:"uniqueIndex;not null" json:"ppp_unit"`
	Iface             string     `gorm:"size:32" json:"iface"`
	Username          string     `gorm:"size:128;not null" json:"username"`
	Password          string     `gorm:"size:256;not null" json:"password"`
	MAC               string     `gorm:"size:32;index" json:"mac"`
	Type              string     `gorm:"size:16;index;default:'rotating'" json:"type"` // static | private | rotating
	Status            string     `gorm:"size:16;default:'disconnected'" json:"status"`
	IP                string     `gorm:"size:64" json:"ip"`
	PublicIP          string     `gorm:"size:64" json:"public_ip"`
	LastError         string     `gorm:"size:256" json:"last_error"`
	LastRotateAt      *time.Time `json:"last_rotate_at,omitempty"`
	AutoRotateSeconds int        `gorm:"default:0" json:"auto_rotate_seconds"` // 0=tắt, else chu kỳ giây
	AutoRotatePaused  bool       `gorm:"default:false" json:"auto_rotate_paused"`
	ConnectedAt       *time.Time `json:"connected_at,omitempty"`
	RotateFailCount    int        `gorm:"default:0" json:"rotate_fail_count"`
	ReconnectAttempts  int        `gorm:"default:0" json:"reconnect_attempts"`
	NextReconnectAt    *time.Time `json:"next_reconnect_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

type Proxy struct {
	Id        uint   `gorm:"primaryKey" json:"id"`
	SessionId uint   `gorm:"uniqueIndex;not null" json:"session_id"`
	Port      int    `gorm:"uniqueIndex;not null" json:"port"`
	Status    string `gorm:"size:16;default:'stopped'" json:"status"`
}

type ProxyCredential struct {
	Id      uint   `gorm:"primaryKey" json:"id"`
	ProxyId uint   `gorm:"index;not null" json:"proxy_id"`
	Label   string `gorm:"size:64" json:"label"`
	Username string `gorm:"size:128;not null" json:"username"`
	Password string `gorm:"size:256;not null" json:"password"`
	Enabled  bool   `gorm:"default:true" json:"enabled"`
	IUserId  string `gorm:"size:128;index" json:"iuser_id,omitempty"`
	// OrderId — idempotency key cấp từ main server (1 order = 1 nhóm creds).
	// Khi present, claim cùng (iuser_id, order_id, type) trả về cred cũ; khác
	// order_id → cấp creds mới. Empty = legacy idempotent theo iuser_id only.
	OrderId   string     `gorm:"size:64;index" json:"order_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type Token struct {
	Id        uint      `gorm:"primaryKey" json:"id"`
	Token     string    `gorm:"size:128;uniqueIndex;not null" json:"-"`
	Label     string    `gorm:"size:64" json:"label"`
	UserId    *uint     `gorm:"index" json:"user_id,omitempty"` // NULL = API token, !NULL = login session
	CreatedAt time.Time `json:"created_at"`
}

// User — tài khoản đăng nhập UI (username + bcrypt password). Mỗi user có thể
// có nhiều Token (session đăng nhập + API token tự sinh).
type User struct {
	Id           uint      `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string    `gorm:"size:128;not null" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Access rule constants
const (
	RuleScopeGlobal  = "global"
	RuleScopeSession = "session"
	RuleKindDomain   = "domain" // value: exact hoặc *.suffix
	RuleKindIP       = "ip"     // value: IP đơn hoặc CIDR
	RuleActionAllow  = "allow"
	RuleActionDeny   = "deny"
)

// AccessRule — whitelist/blacklist cho proxy listener.
// Deny-wins: 1 match deny → block. Allow chỉ kích hoạt strict mode (nếu có entry
// allow trong scope hiệu lực, chỉ host match allow mới qua).
type AccessRule struct {
	Id        uint      `gorm:"primaryKey" json:"id"`
	Scope     string    `gorm:"size:16;index;not null" json:"scope"`               // global | session
	SessionId *uint     `gorm:"index" json:"session_id,omitempty"`                 // NULL khi global
	Kind      string    `gorm:"size:16;not null" json:"kind"`                      // domain | ip
	Action    string    `gorm:"size:16;not null" json:"action"`                    // allow | deny
	Value     string    `gorm:"size:256;not null" json:"value"`                    // domain hoặc CIDR/IP
	Note      string    `gorm:"size:256" json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type Setting struct {
	Key   string `gorm:"primaryKey;size:64" json:"key"`
	Value string `gorm:"size:256;not null" json:"value"`
}

type AuditLog struct {
	Id           uint      `gorm:"primaryKey" json:"id"`
	TokenId      uint      `gorm:"index" json:"token_id"`
	UserId       *uint     `gorm:"index" json:"user_id,omitempty"`
	ClientIP     string    `gorm:"size:64" json:"client_ip"`
	Action       string    `gorm:"size:32;index;not null" json:"action"`
	ResourceType string    `gorm:"size:32;index;not null" json:"resource_type"`
	ResourceId   uint      `gorm:"index" json:"resource_id"`
	OldValue     *string   `gorm:"type:text" json:"old_value,omitempty"`
	NewValue     *string   `gorm:"type:text" json:"new_value,omitempty"`
	Summary      string    `gorm:"size:256" json:"summary"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// ActivityLog — sự kiện hệ thống có giá trị diagnostic, persist trong DB.
// Khác AuditLog (chỉ ghi HTTP request có token): ActivityLog ghi mọi state
// transition của session/proxy/cred kể cả các action background (watchdog,
// auto-rotate, reconnect). Mục đích: khi có vấn đề, người dùng mở trang Hoạt
// động xem ngay "session #X đang bị gì, ai làm gì lúc nào".
//
// Lifecycle: cleanup goroutine xóa entry > 30 ngày.
type ActivityLog struct {
	Id        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
	Level     string    `gorm:"size:8;index" json:"level"`    // info | warn | error
	Category  string    `gorm:"size:16;index" json:"category"` // dial | rotate | reconnect | watchdog | claim | cred | session | line | auth
	Action    string    `gorm:"size:32" json:"action"`         // free-form: "start" | "ip_assigned" | "blocked_ip" | "auth_nak" | ...
	SessionId *uint     `gorm:"index" json:"session_id,omitempty"`
	LineId    *uint     `gorm:"index" json:"line_id,omitempty"`
	ProxyId   *uint     `gorm:"index" json:"proxy_id,omitempty"`
	CredId    *uint     `gorm:"index" json:"cred_id,omitempty"`
	UserId    *uint     `gorm:"index" json:"user_id,omitempty"`     // UI user (nếu xuất phát từ API)
	IUserId   string    `gorm:"size:64;index" json:"iuser_id,omitempty"`
	ClientIP  string    `gorm:"size:64" json:"client_ip,omitempty"` // nếu từ API
	Summary   string    `gorm:"size:256" json:"summary"`            // tiếng Việt 1 dòng để UI hiển thị
	Details   string    `gorm:"type:text" json:"details,omitempty"`  // JSON metadata phụ
}
