package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRoutingSelfNarrowIdentityCASAndAuthority(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.Model(&model.User{}).Where("id = 1").Update("role", common.RoleCommonUser).Error)
	require.NoError(t, db.AutoMigrate(&model.AuditLog{}))
	token := seedToken(t, db, 1, "self-key", "abcdefghijklmnopqrstuv1234567890")
	policy := smartrouting.DefaultPolicy()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := model.SaveTokenRoutingTx(tx, token, &policy, 0, nil, false, true, false)
		return err
	}))
	router := gin.New()
	self := router.Group("/self", middleware.RoutingSelfAudit(), middleware.RoutingSelfAuth())
	self.GET("", GetRoutingSelf)
	self.PUT("", PutRoutingSelf)
	request := func(method, credential, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/self", bytes.NewBufferString(body))
		r.RemoteAddr = "127.0.0.1:12345"
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := `{"expected_version":1,"routing":{"schema_version":1,"mode":"smart","strategy":"success"}}`
	assert.Equal(t, http.StatusUnauthorized, request("GET", "", "").Code)
	assert.Equal(t, http.StatusUnauthorized, request("GET", "sk-"+token.Key+"-42", "").Code)
	assert.Equal(t, http.StatusUnauthorized, request("GET", model.AccessTokenPrefix+token.Key, "").Code)
	read := request("GET", "sk-"+token.Key, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.NotContains(t, read.Body.String(), token.Key)
	assert.NotContains(t, read.Body.String(), "user_id")
	var fields map[string]common.RawMessage
	require.NoError(t, common.Unmarshal(decodeAPIResponse(t, read).Data, &fields))
	require.Len(t, fields, 3)
	for _, field := range []string{"routing", "routing_version", "allow_routing_self_write"} {
		assert.Contains(t, fields, field)
	}
	assert.Equal(t, http.StatusForbidden, request("PUT", "sk-"+token.Key, body).Code)
	assert.Equal(t, http.StatusBadRequest, request("PUT", "sk-"+token.Key, `{"expected_version":1,"routing":null}`).Code)
	assert.Equal(t, http.StatusBadRequest, request("PUT", "sk-"+token.Key, `{"expected_version":1,"routing":{},"remain_quota":999}`).Code)
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", token.Id).Update("allow_self_write", true).Error)
	write := request("PUT", "sk-"+token.Key, body)
	require.Equal(t, http.StatusOK, write.Code, write.Body.String())
	assert.Equal(t, http.StatusConflict, request("PUT", "sk-"+token.Key, body).Code)
	var row model.TokenRouting
	require.NoError(t, db.First(&row, "token_id = ?", token.Id).Error)
	assert.EqualValues(t, 2, row.Version)
	assert.Contains(t, row.PolicyJSON, "success")
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", token.Id).Update("allow_self_write", false).Error)
	assert.Equal(t, http.StatusForbidden, request("PUT", "sk-"+token.Key, body).Code)
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", token.Id).Update("allow_self_write", true).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = 1").Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusForbidden, request("GET", "sk-"+token.Key, "").Code)
	assert.Equal(t, http.StatusForbidden, request("PUT", "sk-"+token.Key, body).Code)
	require.NoError(t, db.Model(&model.User{}).Where("id = 1").Update("status", common.UserStatusEnabled).Error)
	require.NoError(t, db.Model(token).Update("allow_ips", "203.0.113.0/24").Error)
	assert.Equal(t, http.StatusForbidden, request("GET", "sk-"+token.Key, "").Code)
	assert.Equal(t, http.StatusForbidden, request("PUT", "sk-"+token.Key, body).Code)
	require.NoError(t, db.Model(token).Update("allow_ips", "127.0.0.0/8").Error)
	assert.Equal(t, http.StatusOK, request("GET", "sk-"+token.Key, "").Code)
	for _, updates := range []map[string]any{
		{"status": common.TokenStatusDisabled},
		{"status": common.TokenStatusEnabled, "expired_time": 1},
		{"expired_time": -1, "unlimited_quota": false, "remain_quota": 0},
	} {
		require.NoError(t, db.Model(token).Updates(updates).Error)
		assert.Equal(t, http.StatusForbidden, request("GET", "sk-"+token.Key, "").Code)
		assert.Equal(t, http.StatusForbidden, request("PUT", "sk-"+token.Key, body).Code)
	}
	var logs []model.AuditLog
	require.NoError(t, db.Find(&logs).Error)
	require.NotEmpty(t, logs)
	for _, log := range logs {
		assert.Equal(t, "api_key", log.AuthMethod)
		assert.Equal(t, log.Status < 400, log.Success)
		data, err := common.Marshal(log)
		require.NoError(t, err)
		assert.NotContains(t, string(data), token.Key)
	}
}

func TestRoutingSelfAuthenticatedDeletionCannotReviveKey(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch=%v", batch), func(t *testing.T) {
			user, _ := setupAccessTokenAudit(t)
			db := model.DB
			require.NoError(t, db.Model(user).Update("role", common.RoleCommonUser).Error)
			migrateTokenControllerTestDB(t, db)
			t.Setenv("LOG_SQL_DSN", "")
			require.NoError(t, model.InitLogDB())
			token := seedToken(t, db, user.Id, "self-delete", "selfdeleteracesecret1234567890")
			policy := smartrouting.DefaultPolicy()
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := model.SaveTokenRoutingTx(tx, token, &policy, 0, common.GetPointer(true), false, true, false)
				return err
			}))
			_, jwt := createAccessTokenTestSession(t, user.Id, "self-delete-session")
			pat, _ := createScopedAccessToken(t, user.Id, time.Now().Add(time.Hour).Unix(), "api_key:write")
			paused, done := make(chan struct{}), make(chan struct{})
			resume, release := context.WithCancel(context.Background())
			defer release()
			router := gin.New()
			router.Use(middleware.RequestId())
			self := router.Group("/api/mowan/smart-routing/self", middleware.RoutingSelfAudit(), middleware.RoutingSelfAuth())
			self.GET("", GetRoutingSelf)
			self.PUT("", func(c *gin.Context) {
				close(paused)
				select {
				case <-resume.Done():
				case <-time.After(5 * time.Second):
					c.AbortWithStatus(http.StatusGatewayTimeout)
				}
			}, PutRoutingSelf)
			console := router.Group("/api/token", middleware.TokenOperationAudit(), middleware.UserAuth())
			console.DELETE("/:id", DeleteToken)
			console.POST("/batch", DeleteTokenBatch)
			for _, credential := range []string{jwt, pat} {
				response := accessTokenRequest(router, "GET", "/api/mowan/smart-routing/self", credential, "", "")
				require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
				var event model.AuditLog
				require.NoError(t, model.LOG_DB.Where("request_id = ?", response.Header().Get(common.RequestIdKey)).First(&event).Error)
				assert.False(t, event.Success)
				assert.Equal(t, http.StatusUnauthorized, event.Status)
				assert.Equal(t, model.AccessTokenFingerprint(credential), event.TokenRef)
			}
			var response *httptest.ResponseRecorder
			go func() {
				response = accessTokenRequest(router, "PUT", "/api/mowan/smart-routing/self", "sk-"+token.Key, "", `{"expected_version":1,"routing":{"schema_version":1,"mode":"smart","strategy":"success"}}`)
				close(done)
			}()
			select {
			case <-paused:
			case <-time.After(5 * time.Second):
				t.Fatal("self request did not reach the post-authentication barrier")
			}
			method, path, body := "DELETE", fmt.Sprintf("/api/token/%d", token.Id), ""
			if batch {
				method, path, body = "POST", "/api/token/batch", fmt.Sprintf(`{"ids":[%d]}`, token.Id)
			}
			deleted := accessTokenRequest(router, method, path, jwt, "", body)
			require.True(t, decodeAPIResponse(t, deleted).Success, deleted.Body.String())
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("self request did not finish after deletion")
			}
			require.GreaterOrEqual(t, response.Code, 400, response.Body.String())
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Where("id = ?", token.Id).Count(&count).Error)
			assert.Zero(t, count)
			var tombstone model.Token
			require.NoError(t, db.Unscoped().First(&tombstone, token.Id).Error)
			assert.True(t, tombstone.DeletedAt.Valid)
			row, err := model.GetTokenRouting(db, token.Id)
			require.NoError(t, err)
			assert.Nil(t, row, "late self writes must not recreate the deleted routing row")
			late := accessTokenRequest(router, "GET", "/api/mowan/smart-routing/self", "sk-"+token.Key, "", "")
			assert.Equal(t, http.StatusUnauthorized, late.Code)
			var event model.AuditLog
			require.NoError(t, model.LOG_DB.Where("request_id = ?", response.Header().Get(common.RequestIdKey)).First(&event).Error)
			assert.Equal(t, "token.routing_self_update", event.Action)
			assert.False(t, event.Success)
			encoded, err := common.Marshal(event)
			require.NoError(t, err)
			for _, secret := range []string{jwt, pat, token.Key} {
				assert.NotContains(t, string(encoded), secret)
				assert.NotContains(t, response.Body.String(), secret)
			}
		})
	}
}

func TestRoutingSelfModelLimitsAndRateLimitDoNotConsumeQuota(t *testing.T) {
	user, _ := setupAccessTokenAudit(t)
	db := model.DB
	migrateTokenControllerTestDB(t, db)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	token := seedToken(t, db, user.Id, "self-limits", "selfhardlimitssecret1234567890")
	require.NoError(t, db.Model(token).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "allowed-model"}).Error)
	policy := smartrouting.DefaultPolicy()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := model.SaveTokenRoutingTx(tx, token, &policy, 0, common.GetPointer(true), false, true, false)
		return err
	}))
	previousEnabled, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	t.Cleanup(func() {
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnabled, previousNum, previousDuration
	})
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 2, 60
	router := gin.New()
	router.Use(middleware.RequestId())
	self := router.Group("/api/mowan/smart-routing/self", middleware.RoutingSelfAudit(), middleware.RoutingSelfAuth(), middleware.UserCriticalRateLimit(t.Name()))
	self.PUT("", PutRoutingSelf)
	self.GET("", GetRoutingSelf)
	path, credential := "/api/mowan/smart-routing/self", "sk-"+token.Key
	denied := accessTokenRequest(router, "PUT", path, credential, "", `{"expected_version":1,"routing":{"schema_version":1,"mode":"smart","strategy":"custom","models":{"outside-model":{"strategy":"price","groups":["default"]}}}}`)
	require.Equal(t, http.StatusBadRequest, denied.Code, denied.Body.String())
	assert.Contains(t, denied.Body.String(), "outside token model limits")
	read := accessTokenRequest(router, "GET", path, credential, "", "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	limited := accessTokenRequest(router, "GET", path, credential, "", "")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	var saved model.Token
	require.NoError(t, db.First(&saved, token.Id).Error)
	assert.Equal(t, token.RemainQuota, saved.RemainQuota)
	assert.Zero(t, saved.UsedQuota)
	assert.True(t, saved.ModelLimitsEnabled)
	assert.Equal(t, "allowed-model", saved.ModelLimits)
	var savedUser model.User
	require.NoError(t, db.First(&savedUser, user.Id).Error)
	assert.Equal(t, user.Quota, savedUser.Quota)
	row, err := model.GetTokenRouting(db, token.Id)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.EqualValues(t, 1, row.Version)
	var event model.AuditLog
	require.NoError(t, model.LOG_DB.Where("request_id = ?", limited.Header().Get(common.RequestIdKey)).First(&event).Error)
	assert.False(t, event.Success)
	assert.Equal(t, http.StatusTooManyRequests, event.Status)
}
