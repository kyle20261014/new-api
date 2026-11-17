package middleware

import (
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// RoutingPlaygroundPolicy consumes a temporary console-only policy before channel
// selection. No API key is created or changed and the policy is never forwarded.
func RoutingPlaygroundPolicy() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/pg/chat/completions" || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			abortWithOpenAiMessage(c, invalidRequestStatus(err), i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
			return
		}
		var fields map[string]common.RawMessage
		if err := common.Unmarshal(raw, &fields); err != nil || fields == nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": "invalid JSON object"}))
			return
		}
		policyData, supplied := fields["mowan_routing"]
		if !supplied {
			c.Next()
			return
		}
		if c.GetInt("id") <= 0 || c.GetBool("use_access_token") {
			abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgRelayAccessTokenUnsupported))
			return
		}
		if countTopLevelJSONKey(raw, "mowan_routing") != 1 {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": "temporary routing policy must be provided once"}))
			return
		}
		policy, err := smartrouting.ParsePolicy(policyData)
		if err != nil || policy.Mode == "fixed" && (policy.FixedGroup == nil || *policy.FixedGroup == "") {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": "invalid temporary routing policy"}))
			return
		}
		canonical, err := policy.CanonicalJSON()
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": "invalid temporary routing policy"}))
			return
		}
		user, err := model.GetUserById(c.GetInt("id"), false)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDatabaseError))
			return
		}
		if user.Status != common.UserStatusEnabled {
			abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
			return
		}
		delete(fields, "mowan_routing")
		delete(fields, "group")
		body, err := common.Marshal(fields)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": "invalid temporary routing request"}))
			return
		}
		replacement, err := common.CreateBodyStorage(body)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDatabaseError))
			return
		}
		_ = storage.Close()
		c.Set(common.KeyBodyStorage, replacement)
		c.Set(common.KeyRequestBody, nil)
		c.Request.Body = io.NopCloser(replacement)
		c.Request.ContentLength = int64(len(body))
		user.ToBaseUser().WriteContext(c)
		_ = SetupContextForToken(c, &model.Token{UserId: user.Id, Name: "playground", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
		c.Set("mowan_routing_playground", true)
		c.Set(smartrouting.ContextKey, &model.TokenRouting{SchemaVersion: 1, Mode: policy.Mode, PolicyJSON: canonical, Version: 1})
		c.Next()
	}
}
