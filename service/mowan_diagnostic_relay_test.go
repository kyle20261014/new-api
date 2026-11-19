package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDiagnosticRelayOriginHasSessionAndFundingBoundary(t *testing.T) {
	user := setupAuthSessionTestDB(t) // One SQLite connection also detects nested-pool deadlocks.
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.CasbinRule{}, &model.AuthzRole{}))
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = wasMaster })
	require.NoError(t, authz.Init(db))
	session := &model.UserSession{SID: "diagnostic-session", UserID: user.Id, UserAuthVersion: user.AuthVersion, Version: 1, Status: model.UserSessionStatusActive, LoginMethod: "test", ExpiresAt: time.Now().Unix() + 3600}
	require.NoError(t, db.Create(session).Error)
	identity := AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: user.AuthVersion, SessionVersion: 1}
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Set("id", user.Id)
		c.Set("token_id", 0)
		c.Set(smartrouting.ContextKey, &model.TokenRouting{Mode: "fixed", Version: 1, PolicyJSON: `{"schema_version":1,"mode":"fixed","fixed_group":"default"}`})
		c.Set(diagnosticOriginKey, &diagnosticOrigin{identity: identity, group: "default", request: "persisted-step-id"})
		return c
	}
	accept := func(c *gin.Context) error {
		return routingAuthority(c, "test-model", func(_ *gorm.DB, _ *model.TokenRouting, groups []string, _ string) error {
			require.Equal(t, []string{"default"}, groups)
			return nil
		})
	}
	c := newContext()
	require.NoError(t, accept(c))
	require.Equal(t, "persisted-step-id", RoutingPlanFor(c).RequestID)
	for _, change := range []func(*gin.Context){
		func(c *gin.Context) { c.Set(diagnosticOriginKey, "forged-by-header") },
		func(c *gin.Context) { c.Set("use_access_token", true) },
		func(c *gin.Context) { c.Set("token_id", 42) },
		func(c *gin.Context) { c.Request.URL.Path = "/pg/chat/completions" },
		func(c *gin.Context) { diagnosticRelayOrigin(c).group = "not-granted" },
	} {
		c := newContext()
		change(c)
		require.Error(t, accept(c))
	}
	// Direct DB revocation must win even if an old session was already validated.
	require.NoError(t, db.Model(session).Update("status", model.UserSessionStatusRevoked).Error)
	require.Error(t, accept(newContext()))
	require.NoError(t, db.Model(session).Update("status", model.UserSessionStatusActive).Error)
	require.NoError(t, db.Model(user).Update("auth_version", user.AuthVersion+1).Error)
	require.Error(t, accept(newContext()))
}

func TestDiagnosticConfigHashTracksWildcardConfigurationNotUsage(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Vendor{}, &model.Option{}))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	createChannelSelectAutoGroupsChannel(t, db, 9901, "all", "diagnostic-hash-model")
	before, err := DiagnosticConfigHash("diagnostic-hash-model", "default")
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 9901).Update("used_quota", 9999).Error)
	afterUsage, err := DiagnosticConfigHash("diagnostic-hash-model", "default")
	require.NoError(t, err)
	require.Equal(t, before, afterUsage)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 9901).Update("header_override", `{"X-Test":"changed"}`).Error)
	afterConfiguration, err := DiagnosticConfigHash("diagnostic-hash-model", "default")
	require.NoError(t, err)
	require.NotEqual(t, before, afterConfiguration, "all-group abilities must participate in configuration invalidation")
}
