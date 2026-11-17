package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenRoutingRejectedCredentialsAreAudited(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	migrateTokenControllerTestDB(t, model.DB)
	token := seedToken(t, model.DB, user.Id, "denial-key", "routingdeniedapikeysecret123456")
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	tokens := router.Group("/api/token", middleware.TokenOperationAudit(), middleware.UserAuth())
	tokens.PUT("/", UpdateToken)
	tokens.DELETE("/:id", DeleteToken)
	for _, credential := range []string{"", "sk-" + token.Key} {
		for _, method := range []string{"PUT", "DELETE"} {
			path := "/api/token/"
			if method == "DELETE" {
				path += stringInt(token.Id)
			}
			response := accessTokenRequest(router, method, path, credential, "", `{"routing":"private-denied-request-body"}`)
			require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			var events []model.AuditLog
			require.NoError(t, model.LOG_DB.Where("request_id = ?", response.Header().Get(common.RequestIdKey)).Find(&events).Error)
			require.Len(t, events, 1, "API key and anonymous rejections must leave one operation event")
			assert.False(t, events[0].Success)
			assert.Equal(t, response.Code, events[0].Status)
			require.NotNil(t, events[0].Other.Op)
			reason, err := common.Marshal(events[0].Other.Op.Params["failure_reason"])
			require.NoError(t, err)
			assert.JSONEq(t, `"Unauthorized"`, string(reason))
			if credential == "" {
				assert.Equal(t, "anonymous", events[0].AuthMethod)
				assert.Empty(t, events[0].TokenRef)
			} else {
				assert.Equal(t, "api_key", events[0].AuthMethod)
				assert.Equal(t, model.AccessTokenFingerprint(token.Key), events[0].TokenRef)
			}
			encoded, err := common.Marshal(events)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), token.Key)
			assert.NotContains(t, string(encoded), "private-denied-request-body")
		}
	}
}

func TestTokenRoutingSessionPATScopesOwnershipAndAudit(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	db := model.DB
	require.NoError(t, db.Model(user).Update("role", common.RoleCommonUser).Error)
	migrateTokenControllerTestDB(t, db)
	_, jwt := createAccessTokenTestSession(t, user.Id, "routing-scope-session")
	reader, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), "api_key:read")
	writer, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), "api_key:write")
	revealer, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), "api_key:reveal")
	router := gin.New()
	router.Use(middleware.RequestId(), middleware.AccessTokenAudit())
	tokens := router.Group("/api/token", middleware.TokenOperationAudit(), middleware.UserAuth())
	tokens.GET("/:id", GetToken)
	tokens.PUT("/", UpdateToken)
	tokens.POST("/:id/key", GetTokenKey)
	for index, tc := range []struct {
		name, credential, method string
		foreign                  bool
		status                   int
		success                  bool
	}{
		{"session read", jwt, "GET", false, 200, true},
		{"session write", jwt, "PUT", false, 200, true},
		{"session reveal", jwt, "POST", false, 200, true},
		{"read PAT read", reader, "GET", false, 200, true},
		{"read PAT cannot write", reader, "PUT", false, 403, false},
		{"read PAT cannot reveal", reader, "POST", false, 403, false},
		{"write PAT cannot read", writer, "GET", false, 403, false},
		{"write PAT write", writer, "PUT", false, 200, true},
		{"write PAT cannot reveal", writer, "POST", false, 403, false},
		{"reveal PAT cannot read", revealer, "GET", false, 403, false},
		{"reveal PAT cannot write", revealer, "PUT", false, 403, false},
		{"reveal PAT reveal", revealer, "POST", false, 200, true},
		{"session foreign read", jwt, "GET", true, 200, false},
		{"session foreign write", jwt, "PUT", true, 200, false},
		{"session foreign reveal", jwt, "POST", true, 200, false},
		{"read PAT foreign read", reader, "GET", true, 200, false},
		{"write PAT foreign write", writer, "PUT", true, 200, false},
		{"reveal PAT foreign reveal", revealer, "POST", true, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := user.Id
			if tc.foreign {
				owner++
			}
			token := seedToken(t, db, owner, "private-routing-name", fmt.Sprintf("routing-scope-secret-%d", index))
			path, body := fmt.Sprintf("/api/token/%d", token.Id), ""
			if tc.method == "POST" {
				path += "/key"
			} else if tc.method == "PUT" {
				path = "/api/token/"
				body = fmt.Sprintf(`{"id":%d,"name":"updated","routing":{"schema_version":1,"mode":"smart","strategy":"success"},"expected_routing_version":0,"allow_routing_self_write":true}`, token.Id)
			}
			response := accessTokenRequest(router, tc.method, path, tc.credential, "", body)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			assert.Equal(t, tc.success, decodeAPIResponse(t, response).Success, response.Body.String())
			if tc.method == "POST" && tc.success {
				assert.Contains(t, response.Body.String(), token.GetFullKey())
			} else {
				assert.NotContains(t, response.Body.String(), token.Key)
			}
			if tc.foreign {
				assert.NotContains(t, response.Body.String(), token.Name)
			}
			row, err := model.GetTokenRouting(db, token.Id)
			require.NoError(t, err)
			if tc.method == "PUT" && tc.success {
				require.NotNil(t, row)
				assert.EqualValues(t, 1, row.Version)
				assert.True(t, row.AllowSelfWrite)
			} else {
				assert.Nil(t, row, "denied reads/writes must not create routing state")
			}
			var events []model.AuditLog
			require.NoError(t, model.LOG_DB.Where("request_id = ?", response.Header().Get(common.RequestIdKey)).Find(&events).Error)
			expected := 0
			if tc.credential != jwt {
				expected++
			}
			if tc.method != "GET" && tc.status != 403 {
				expected++
			}
			require.Len(t, events, expected)
			for _, event := range events {
				assert.Equal(t, tc.success, event.Success)
				assert.Equal(t, tc.status, event.Status)
				if event.Category == model.AuditCategorySecurity {
					assert.Equal(t, user.Id, event.UserId)
					assert.Equal(t, common.RoleCommonUser, event.ActorRole)
					if tc.method == "PUT" {
						assert.Equal(t, "token.update", event.Action)
					} else {
						assert.Equal(t, "token.key_view", event.Action)
					}
				}
			}
			encoded, err := common.Marshal(events)
			require.NoError(t, err)
			for _, secret := range []string{jwt, reader, writer, revealer, token.Key} {
				assert.NotContains(t, string(encoded), secret)
			}
			if tc.foreign {
				assert.NotContains(t, string(encoded), token.Name)
			}
		})
	}
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	catalog := router.Group("/api/mowan/smart-routing", middleware.UserAuth())
	catalog.GET("/candidates", GetRoutingCandidates)
	catalog.POST("/preview", PreviewRouting)
	foreign := seedToken(t, db, user.Id+1, "private-catalog-owner", "private-catalog-key")
	for _, tc := range []struct {
		name, credential string
		allowed          bool
	}{
		{"session", jwt, true}, {"read PAT", reader, true},
		{"write PAT", writer, false}, {"reveal PAT", revealer, false},
	} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("catalog/%s/preview=%v", tc.name, preview), func(t *testing.T) {
				method, path, body := "GET", "/api/mowan/smart-routing/candidates", ""
				if preview {
					method, path, body = "POST", "/api/mowan/smart-routing/preview", `{"routing":{"schema_version":1,"mode":"smart","strategy":"balanced"},"model":"routing-scope-model"}`
				}
				response := accessTokenRequest(router, method, path, tc.credential, "", body)
				if tc.allowed {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					require.True(t, decodeAPIResponse(t, response).Success, response.Body.String())
					response = accessTokenRequest(router, method, path+"?token_id="+stringInt(foreign.Id), tc.credential, "", body)
				}
				assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), foreign.Name)
				assert.NotContains(t, response.Body.String(), foreign.Key)
			})
		}
	}
}

func TestTokenRoutingCreateDefaultAndExplicitLegacy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		legacy bool
	}{
		{"default", nil, false},
		{"explicit smart", map[string]any{"routing": map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced"}, "expected_routing_version": 0}, false},
		{"explicit empty group", map[string]any{"group": ""}, true},
		{"explicit auto", map[string]any{"group": "auto"}, true},
		{"explicit auto groups", map[string]any{"auto_groups": []string{}}, true},
		{"explicit cross retry", map[string]any{"cross_group_retry": false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			body := map[string]any{"name": "routing-create", "unlimited_quota": true}
			for field, value := range tc.fields {
				body[field] = value
			}
			c, w := newAuthenticatedContext(t, http.MethodPost, "/api/token/", body, 1)
			AddToken(c)
			require.True(t, decodeAPIResponse(t, w).Success, w.Body.String())
			var token model.Token
			require.NoError(t, db.First(&token).Error)
			row, err := model.GetTokenRouting(db, token.Id)
			require.NoError(t, err)
			if tc.legacy {
				assert.Nil(t, row)
			} else {
				require.NotNil(t, row)
				assert.Equal(t, "smart", row.Mode)
				assert.EqualValues(t, 1, row.Version)
				assert.False(t, row.AllowSelfWrite)
			}
		})
	}
}

func TestTokenRoutingRenamePreservesLegacyAndHardLimits(t *testing.T) {
	for groupIndex, group := range []string{"auto", "", "revoked"} {
		for rawIndex, raw := range []string{"", `[]`, `["revoked","vip","default"]`} {
			t.Run(stringInt(groupIndex)+"-"+stringInt(rawIndex), func(t *testing.T) {
				db := setupTokenControllerTestDB(t)
				token := seedToken(t, db, 1, "old", "routing-rename-secret")
				token.Group, token.AutoGroups, token.CrossGroupRetry = group, raw, true
				token.ModelLimitsEnabled, token.ModelLimits = true, "restricted-model"
				token.AllowIps = common.GetPointer("203.0.113.0/24")
				require.NoError(t, db.Save(token).Error)
				c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "renamed"}, 1)
				UpdateToken(c)
				require.True(t, decodeAPIResponse(t, w).Success, w.Body.String())
				var saved model.Token
				require.NoError(t, db.First(&saved, token.Id).Error)
				assert.Equal(t, group, saved.Group)
				assert.Equal(t, raw, saved.AutoGroups)
				assert.True(t, saved.CrossGroupRetry)
				assert.True(t, saved.ModelLimitsEnabled)
				assert.Equal(t, token.ModelLimits, saved.ModelLimits)
				assert.Equal(t, token.AllowIps, saved.AllowIps)
				assert.Equal(t, token.RemainQuota, saved.RemainQuota)
				assert.Equal(t, token.ExpiredTime, saved.ExpiredTime)
				row, err := model.GetTokenRouting(db, token.Id)
				require.NoError(t, err)
				assert.Nil(t, row)
			})
		}
	}
}

func TestTokenRoutingCASAndRestore(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "legacy", "routing-cas-secret")
	require.NoError(t, db.Model(token).Updates(map[string]any{"group": "auto", "auto_groups": `[]`, "cross_group_retry": true}).Error)
	policy := map[string]any{"schema_version": 1, "mode": "smart", "strategy": "price"}
	write := func(body map[string]any) (int, tokenAPIResponse) {
		body["id"] = token.Id
		c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, 1)
		UpdateToken(c)
		assert.NotContains(t, w.Body.String(), token.Key)
		return w.Code, decodeAPIResponse(t, w)
	}
	status, response := write(map[string]any{"routing": policy})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.False(t, response.Success)
	status, response = write(map[string]any{"routing": policy, "expected_routing_version": 0})
	require.Equal(t, http.StatusOK, status, response.Message)
	require.True(t, response.Success, response.Message)
	var returned struct {
		Routing map[string]any `json:"routing"`
		Version int64          `json:"routing_version"`
		Allow   bool           `json:"allow_routing_self_write"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &returned))
	assert.EqualValues(t, 1, returned.Version)
	assert.Equal(t, "price", returned.Routing["strategy"])
	assert.False(t, returned.Allow)
	status, response = write(map[string]any{"routing": policy, "expected_routing_version": 0, "name": "must-rollback"})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "routing_version_conflict", response.Message)
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, "legacy", saved.Name)
	status, response = write(map[string]any{"allow_routing_self_write": true, "expected_routing_version": 1})
	require.Equal(t, http.StatusOK, status, response.Message)
	row, err := model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	assert.Equal(t, "smart", row.Mode)
	assert.True(t, row.AllowSelfWrite)
	assert.EqualValues(t, 2, row.Version)
	status, response = write(map[string]any{"routing": nil, "allow_routing_self_write": false, "expected_routing_version": 2})
	require.Equal(t, http.StatusOK, status, response.Message)
	require.True(t, response.Success, response.Message)
	row, err = model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	assert.Equal(t, "legacy", row.Mode)
	assert.EqualValues(t, 3, row.Version)
	assert.False(t, row.AllowSelfWrite)
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, "auto", saved.Group)
	assert.Equal(t, `[]`, saved.AutoGroups)
	assert.True(t, saved.CrossGroupRetry)
}

func TestTokenRoutingInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		create bool
		fields map[string]any
	}{
		{"create null", true, map[string]any{"routing": nil}},
		{"create nonzero version", true, map[string]any{"expected_routing_version": 1}},
		{"negative version", false, map[string]any{"expected_routing_version": -1}},
		{"null version", false, map[string]any{"expected_routing_version": nil}},
		{"fractional version", false, map[string]any{"expected_routing_version": 0.5}},
		{"string version", false, map[string]any{"expected_routing_version": "0"}},
		{"null switch", false, map[string]any{"allow_routing_self_write": nil}},
		{"string switch", true, map[string]any{"allow_routing_self_write": "false"}},
		{"mixed fields", true, map[string]any{"routing": map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced"}, "group": ""}},
		{"new inherited fixed", true, map[string]any{"routing": map[string]any{"schema_version": 1, "mode": "fixed", "fixed_group": ""}}},
		{"switch missing version", false, map[string]any{"allow_routing_self_write": true}},
		{"no snapshot", false, map[string]any{"routing": nil, "expected_routing_version": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			token := seedToken(t, db, 1, "old", "invalid-routing-secret")
			method := http.MethodPut
			if tc.create {
				method = http.MethodPost
			} else {
				tc.fields["id"] = token.Id
			}
			c, w := newAuthenticatedContext(t, method, "/api/token/", tc.fields, 1)
			if tc.create {
				AddToken(c)
			} else {
				UpdateToken(c)
			}
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.False(t, decodeAPIResponse(t, w).Success)
			var count int64
			require.NoError(t, db.Model(&model.TokenRouting{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestTokenRoutingStorageFailureClosesAndRollsBack(t *testing.T) {
	for _, operation := range []string{"create", "update", "get", "list", "search"} {
		t.Run(operation, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			token := seedToken(t, db, 1, "original", "routing-storage-secret")
			require.NoError(t, db.Migrator().DropTable(&model.TokenRouting{}))
			c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "mutated"}, 1)
			switch operation {
			case "create":
				AddToken(c)
			case "update":
				UpdateToken(c)
			case "get":
				c.Params = gin.Params{{Key: "id", Value: stringInt(token.Id)}}
				GetToken(c)
			case "list":
				GetAllTokens(c)
			case "search":
				SearchTokens(c)
			}
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			assert.False(t, decodeAPIResponse(t, w).Success)
			var saved model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			assert.Equal(t, "original", saved.Name)
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
	for _, operation := range []string{"create", "update"} {
		t.Run("routing-write-rollback-"+operation, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			token := seedToken(t, db, 1, "original", "rollback-routing-secret")
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("routing:fail", func(tx *gorm.DB) {
				if tx.Statement.Table == "mowan_token_routing" {
					_ = tx.AddError(errors.New("injected routing write failure"))
				}
			}))
			body := map[string]any{"id": token.Id, "name": "mutated", "routing": map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced"}, "expected_routing_version": 0}
			c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, 1)
			if operation == "create" {
				AddToken(c)
			} else {
				UpdateToken(c)
			}
			assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			var saved model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			assert.Equal(t, "original", saved.Name)
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestTokenRoutingValidatesCandidatesAndModelLimits(t *testing.T) {
	custom := func(name string, groups []string) map[string]any {
		return map[string]any{"schema_version": 1, "mode": "smart", "strategy": "custom", "models": map[string]any{name: map[string]any{"strategy": "manual", "groups": groups}}}
	}
	for _, tc := range []struct {
		name   string
		policy map[string]any
		want   int
	}{
		{"manual order", custom("allowed-model", []string{"vip", "default"}), http.StatusOK},
		{"all capability expands", custom("all-model", []string{"default"}), http.StatusOK},
		{"revoked group", custom("allowed-model", []string{"missing"}), http.StatusBadRequest},
		{"unsupported model", custom("unsupported", []string{"default"}), http.StatusBadRequest},
		{"hard model limit", custom("outside-model", []string{"default"}), http.StatusBadRequest},
		{"duplicate group", custom("allowed-model", []string{"default", "default"}), http.StatusBadRequest},
		{"empty groups", custom("allowed-model", []string{}), http.StatusBadRequest},
		{"fixed single", map[string]any{"schema_version": 1, "mode": "fixed", "fixed_group": "vip"}, http.StatusOK},
		{"fixed unauthorized", map[string]any{"schema_version": 1, "mode": "fixed", "fixed_group": "missing"}, http.StatusBadRequest},
		{"fixed pseudo group", map[string]any{"schema_version": 1, "mode": "fixed", "fixed_group": "all"}, http.StatusBadRequest},
		{"dynamic excluded group unauthorized", map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced", "excluded_groups": []string{"missing"}}, http.StatusBadRequest},
		{"dynamic excluded model unauthorized", map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced", "excluded_models": []string{"outside-model"}}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.Ability{}))
			require.NoError(t, db.Create(&[]model.Ability{
				{Group: "default", Model: "allowed-model", ChannelId: 1, Enabled: true},
				{Group: "vip", Model: "allowed-model", ChannelId: 1, Enabled: true},
				{Group: "default", Model: "outside-model", ChannelId: 1, Enabled: true},
				{Group: "all", Model: "all-model", ChannelId: 1, Enabled: true},
			}).Error)
			token := seedToken(t, db, 1, "limited", "model-limit-routing-secret")
			require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "allowed-model,all-model"}).Error)
			c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "routing": tc.policy, "expected_routing_version": 0}, 1)
			UpdateToken(c)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			var saved model.Token
			require.NoError(t, db.First(&saved, token.Id).Error)
			assert.True(t, saved.ModelLimitsEnabled)
			assert.Equal(t, "allowed-model,all-model", saved.ModelLimits)
			if tc.name == "manual order" {
				row, err := model.GetTokenRouting(db, token.Id)
				require.NoError(t, err)
				assert.Contains(t, row.PolicyJSON, `"groups":["vip","default"]`)
			}
		})
	}
}

func TestTokenRoutingPreservesSavedPolicyOnRenameAndRejectsCorruptPolicy(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "smart", "saved-policy-routing-secret")
	row := &model.TokenRouting{TokenID: token.Id, SchemaVersion: 1, Mode: "smart", PolicyJSON: `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"revoked-model":{"groups":["revoked-group"],"strategy":"manual"}}}`, Version: 7}
	require.NoError(t, db.Create(row).Error)
	c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "renamed"}, 1)
	UpdateToken(c)
	require.True(t, decodeAPIResponse(t, w).Success, w.Body.String())
	saved, err := model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 7, saved.Version)
	assert.Equal(t, row.PolicyJSON, saved.PolicyJSON)
	c, w = newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "group": "auto"}, 1)
	UpdateToken(c)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.NoError(t, db.Model(row).Update("policy_json", `{"mode":"corrupt"}`).Error)
	c, w = newAuthenticatedContext(t, http.MethodGet, "/api/token/"+stringInt(token.Id), nil, 1)
	c.Params = gin.Params{{Key: "id", Value: stringInt(token.Id)}}
	GetToken(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	c, w = newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "must-not-commit"}, 1)
	UpdateToken(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, "renamed", stored.Name)
}

func TestTokenRoutingTokenWriteFailureRollsBackPolicy(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "original", "token-write-routing-secret")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("token:fail", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			_ = tx.AddError(errors.New("injected token write failure"))
		}
	}))
	c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": "must-not-commit", "routing": map[string]any{"schema_version": 1, "mode": "smart", "strategy": "balanced"}, "expected_routing_version": 0}, 1)
	UpdateToken(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	row, err := model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	assert.Nil(t, row)
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, "original", stored.Name)
}

func TestTokenRoutingModelAliasesKeepExistingHardLimitMatching(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default"]`)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Ability{}))
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "claude-3-7-sonnet", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "gemini-2.5-flash-thinking-*", ChannelId: 1, Enabled: true},
	}).Error)
	token := seedToken(t, db, 1, "aliases", "alias-routing-secret")
	require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "claude-3-7-sonnet,gemini-2.5-flash-thinking-*"}).Error)
	models := map[string]any{}
	for _, name := range []string{"claude-3-7-sonnet@thinking:on", "claude-3-7-sonnet-thinking", "gemini-2.5-flash-thinking-8192"} {
		models[name] = map[string]any{"strategy": "balanced", "groups": []string{"default"}}
	}
	policy := map[string]any{"schema_version": 1, "mode": "smart", "strategy": "custom", "models": models}
	c, w := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "routing": policy, "expected_routing_version": 0}, 1)
	UpdateToken(c)
	require.True(t, decodeAPIResponse(t, w).Success, w.Body.String())
	row, err := model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	for name := range models {
		assert.Contains(t, row.PolicyJSON, name)
	}
}
