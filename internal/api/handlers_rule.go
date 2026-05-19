package api

import (
	"net"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
	proxysrv "github.com/tkiet2105/bestphone-pppoe/internal/proxy/server"
)

type createRuleReq struct {
	Scope     string `json:"scope" binding:"required"`     // global | session
	SessionId *uint  `json:"session_id"`                   // bắt buộc khi scope=session
	Kind      string `json:"kind" binding:"required"`      // domain | ip
	Action    string `json:"action" binding:"required"`    // allow | deny
	Value     string `json:"value" binding:"required"`     // domain hoặc CIDR
	Note      string `json:"note"`
}

func ListRules(c *gin.Context) {
	q := db.DB.Order("id ASC")
	if scope := c.Query("scope"); scope != "" {
		q = q.Where("scope = ?", scope)
	}
	if sid := c.Query("session_id"); sid != "" {
		q = q.Where("session_id = ?", sid)
	}
	if action := c.Query("action"); action != "" {
		q = q.Where("action = ?", action)
	}
	if kind := c.Query("kind"); kind != "" {
		q = q.Where("kind = ?", kind)
	}
	var rs []models.AccessRule
	q.Find(&rs)
	ok(c, rs)
}

func CreateRule(c *gin.Context) {
	var req createRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if err := validateRule(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	rule := models.AccessRule{
		Scope:     req.Scope,
		SessionId: req.SessionId,
		Kind:      req.Kind,
		Action:    req.Action,
		Value:     strings.TrimSpace(req.Value),
		Note:      req.Note,
	}
	if err := db.DB.Create(&rule).Error; err != nil {
		fail(c, 500, err.Error())
		return
	}
	if rule.Scope == models.RuleScopeGlobal {
		proxysrv.M.ReloadGlobalRules()
	} else if rule.SessionId != nil {
		proxysrv.M.ReloadSessionRules(*rule.SessionId)
	}
	ok(c, rule)
}

type updateRuleReq struct {
	Action *string `json:"action"`
	Value  *string `json:"value"`
	Note   *string `json:"note"`
}

func UpdateRule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var rule models.AccessRule
	if err := db.DB.First(&rule, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	var req updateRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, err.Error())
		return
	}
	if req.Action != nil {
		if *req.Action != models.RuleActionAllow && *req.Action != models.RuleActionDeny {
			fail(c, 400, "action must be allow|deny")
			return
		}
		rule.Action = *req.Action
	}
	if req.Value != nil {
		rule.Value = strings.TrimSpace(*req.Value)
		// re-validate kind specifically
		if rule.Kind == models.RuleKindIP && parseCIDR(rule.Value) == nil {
			fail(c, 400, "invalid IP/CIDR")
			return
		}
	}
	if req.Note != nil {
		rule.Note = *req.Note
	}
	db.DB.Save(&rule)
	if rule.Scope == models.RuleScopeGlobal {
		proxysrv.M.ReloadGlobalRules()
	} else if rule.SessionId != nil {
		proxysrv.M.ReloadSessionRules(*rule.SessionId)
	}
	ok(c, rule)
}

func DeleteRule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var rule models.AccessRule
	if err := db.DB.First(&rule, id).Error; err != nil {
		fail(c, 404, "not found")
		return
	}
	db.DB.Delete(&rule)
	if rule.Scope == models.RuleScopeGlobal {
		proxysrv.M.ReloadGlobalRules()
	} else if rule.SessionId != nil {
		proxysrv.M.ReloadSessionRules(*rule.SessionId)
	}
	ok(c, gin.H{"deleted": id})
}

func validateRule(r *createRuleReq) error {
	switch r.Scope {
	case models.RuleScopeGlobal:
		if r.SessionId != nil {
			return errMsg("session_id phải nil khi scope=global")
		}
	case models.RuleScopeSession:
		if r.SessionId == nil || *r.SessionId == 0 {
			return errMsg("session_id bắt buộc khi scope=session")
		}
		var s models.Session
		if err := db.DB.First(&s, *r.SessionId).Error; err != nil {
			return errMsg("session not found")
		}
	default:
		return errMsg("scope must be global|session")
	}
	if r.Kind != models.RuleKindDomain && r.Kind != models.RuleKindIP {
		return errMsg("kind must be domain|ip")
	}
	if r.Action != models.RuleActionAllow && r.Action != models.RuleActionDeny {
		return errMsg("action must be allow|deny")
	}
	r.Value = strings.TrimSpace(r.Value)
	if r.Value == "" {
		return errMsg("value rỗng")
	}
	if r.Kind == models.RuleKindIP && parseCIDR(r.Value) == nil {
		return errMsg("invalid IP/CIDR")
	}
	return nil
}

func parseCIDR(v string) *net.IPNet {
	if !strings.Contains(v, "/") {
		if strings.Contains(v, ":") {
			v += "/128"
		} else {
			v += "/32"
		}
	}
	_, n, err := net.ParseCIDR(v)
	if err != nil {
		return nil
	}
	return n
}

type stringErr string

func (s stringErr) Error() string { return string(s) }
func errMsg(s string) error       { return stringErr(s) }
