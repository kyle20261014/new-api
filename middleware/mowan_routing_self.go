package middleware

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RoutingSelfAuth deliberately does not accept dashboard sessions, PATs, or
// channel suffixes. TokenAuth still applies its complete API-key restrictions.
func RoutingSelfAuth() gin.HandlerFunc {
	auth := TokenAuth()
	return func(c *gin.Context) {
		raw, present := authorizationToken(c.GetHeader("Authorization"))
		key := strings.TrimPrefix(raw, "sk-")
		if !present || key == "" || strings.ContainsAny(key, "- \t\r\n") || strings.HasPrefix(key, model.AccessTokenPrefix) {
			common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("A valid API key is required"))
			c.Abort()
			return
		}
		if _, internal, _ := service.ParseDashboardAccessToken(raw); internal {
			common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("A valid API key is required"))
			c.Abort()
			return
		}
		// Classifying the credential against the genuine key table prevents a
		// management token from being interpreted as a user-key identity.
		token, err := model.GetTokenByKey(key, true)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Routing identity is unavailable"))
			c.Abort()
			return
		}
		if err != nil || token == nil {
			common.ApiErrorStatus(c, http.StatusUnauthorized, common.NewMessage("A valid API key is required"))
			c.Abort()
			return
		}
		c.Set("mowan_routing_api_key_auth", true)
		c.Set("token_id", token.Id)
		c.Set("id", token.UserId)
		if err := validateRoutingSelfIdentity(c, token); err != nil {
			common.ApiErrorStatus(c, http.StatusForbidden, err)
			c.Abort()
			return
		}
		auth(c)
	}
}

func validateRoutingSelfIdentity(c *gin.Context, token *model.Token) error {
	if token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp()) || (!token.UnlimitedQuota && token.RemainQuota <= 0) {
		return common.NewMessage("API key is disabled, expired or exhausted")
	}
	if allowed := token.GetIpLimits(); len(allowed) > 0 {
		ip := net.ParseIP(c.ClientIP())
		if ip == nil || !common.IsIpInCIDRList(ip, allowed) {
			return common.NewMessage("API key IP address is not allowed")
		}
	}
	user, err := model.GetUserById(token.UserId, false)
	if err != nil {
		return errors.New("routing_identity_unavailable")
	}
	if user.Status != common.UserStatusEnabled {
		return common.NewMessage("User is disabled")
	}
	c.Set("role", user.Role)
	c.Set("username", user.Username)
	user.ToBaseUser().WriteContext(c)
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	return nil
}

// This wrapper precedes authentication so denied operations are audited too.
// The hash identifies a presented credential without recording its value.
func RoutingSelfAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := authorizationToken(c.GetHeader("Authorization"))
		params := model.AuditFields{}
		common.SetContextKey(c, constant.ContextKeyTokenAuditParams, params)
		c.Next()
		if id := c.GetInt("token_id"); id > 0 {
			params["id"] = id
		}
		action := "token.routing_self_read"
		if c.Request.Method == http.MethodPut {
			action = "token.routing_self_update"
		}
		model.RecordAuditLog(c, model.AuditLog{
			UserId: c.GetInt("id"), Username: c.GetString("username"), ActorRole: c.GetInt("role"),
			Category: model.AuditCategorySecurity, Action: action, Content: "API key routing self management",
			AuthMethod: "api_key", TokenRef: model.AccessTokenFingerprint(strings.TrimPrefix(raw, "sk-")),
			Status: c.Writer.Status(), Success: c.Writer.Status() < 400,
			Other: model.AuditOther{Op: &model.AuditOperation{Action: action, Params: params}},
		})
	}
}
