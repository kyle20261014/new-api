package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	diagnostics "github.com/QuantumNous/new-api/internal/mowan/modeldiagnostics"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticReportVisibilityAndAccounting(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Option{}, &model.RoutingFundingSession{}, &model.RoutingFundingOperation{}))
	require.NoError(t, model.MigrateModelDiagnostics(db))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	plan, err := diagnostics.NewPlan("connection", false)
	require.NoError(t, err)
	encoded, err := common.Marshal(plan)
	require.NoError(t, err)
	config, err := service.DiagnosticConfigHash("test-chat", "default")
	require.NoError(t, err)
	now := time.Now().Unix()
	run := &model.DiagnosticRun{ID: "report", UserID: 1, SubmissionKey: "submission", SessionID: "private-session", ModelName: "test-chat", GroupName: "default", Kind: "connection", Version: plan.Version, ConfigHash: config, State: "completed", PlanJSON: string(encoded), CreatedAt: now, CompletedAt: now}
	require.NoError(t, db.Create(run).Error)
	step := &model.DiagnosticStep{ID: "step", RunID: run.ID, RequestID: "request", FundingID: "private-funding", SourceHash: "private-source", ChannelID: 999, State: "completed", Answer: "OK", Complete: true}
	require.NoError(t, db.Create(step).Error)
	ctx, _ := newAuthenticatedContext(t, http.MethodGet, "/", nil, 1)
	view, err := diagnosticView(ctx, run)
	require.NoError(t, err)
	require.Equal(t, "pending", view["fee"].(gin.H)["status"])
	require.NotContains(t, view["fee"], "quota", "content success does not imply financial settlement")
	require.NoError(t, db.Create(&model.RoutingFundingSession{FundingID: step.FundingID, UserID: 1, Source: "wallet", Quota: 123, Status: "settled"}).Error)
	view, err = diagnosticView(ctx, run)
	require.NoError(t, err)
	require.EqualValues(t, 123, view["fee"].(gin.H)["quota"])
	operationID := service.ViolationFeeOperationID(step.RequestID)
	require.NoError(t, db.Create(&model.RoutingFundingOperation{OperationID: operationID, FundingID: operationID, UserID: 1, Source: "wallet", Kind: "delta", Delta: 70, Quota: 70}).Error)
	view, err = diagnosticView(ctx, run)
	require.NoError(t, err)
	require.EqualValues(t, 193, view["fee"].(gin.H)["quota"])
	require.NoError(t, db.Model(&model.RoutingFundingSession{}).Where("funding_id = ?", step.FundingID).Update("status", "refunded").Error)
	view, err = diagnosticView(ctx, run)
	require.NoError(t, err)
	require.EqualValues(t, 70, view["fee"].(gin.H)["quota"], "a generation refund does not erase an existing policy charge")
	run.CompletedAt = now - 86400
	ctx.Set("id", 2)
	view, err = diagnosticView(ctx, run)
	require.NoError(t, err)
	require.Equal(t, true, view["stale"])
	require.NotContains(t, view, "fee")
	public, err := common.Marshal(view)
	require.NoError(t, err)
	for _, secret := range []string{"private-session", "private-funding", "private-source", "channel_id", "user_id"} {
		require.NotContains(t, string(public), secret)
	}
	require.NoError(t, db.Model(run).Update("creative", true).Error)
	ctx, response := newAuthenticatedContext(t, http.MethodGet, "/api/mowan/model-diagnostics/reports/report", nil, 2)
	ctx.Params = append(ctx.Params, gin.Param{Key: "id", Value: run.ID})
	GetModelDiagnosticReport(ctx)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestDiagnosticRejectsInvalidRequestBeforeAnyWork(t *testing.T) {
	for _, input := range []map[string]any{
		{"kind": "drawing", "model": "chat", "group": "default"},
		{"kind": "other", "idempotency_key": "key"},
		{"kind": "connection", "creative": true, "idempotency_key": "key"},
	} {
		ctx, response := newAuthenticatedContext(t, http.MethodPost, "/api/mowan/model-diagnostics/runs", input, 1)
		StartModelDiagnostic(ctx)
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
}

func TestDiagnosticRateLimitBeforeRelayHasNoPendingCharge(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Option{}, &model.UserSession{}, &model.RoutingFundingSession{}, &model.RoutingFundingOperation{}))
	require.NoError(t, model.MigrateModelDiagnostics(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	user := &model.User{Id: 987654, Username: "diagnostic-limit", Group: "default", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	url := "http://127.0.0.1:1"
	require.NoError(t, db.Create(&model.Channel{Id: 8877, Type: constant.ChannelTypeOpenAI, Key: "test-only", Models: "diagnostic-limit-chat", Group: "default", BaseURL: &url, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 8877, Group: "default", Model: "diagnostic-limit-chat", Enabled: true}).Error)
	require.NoError(t, db.Create(&model.Model{ModelName: "diagnostic-limit-chat", Status: 1, Endpoints: `{"openai":"/v1/chat/completions"}`}).Error)
	session := &model.UserSession{SID: "limit-session", UserID: user.Id, Version: 1, UserAuthVersion: 1, LoginMethod: "test", Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Unix() + 3600}
	require.NoError(t, db.Create(session).Error)
	plan, err := diagnostics.NewPlan("connection", false)
	require.NoError(t, err)
	encoded, err := common.Marshal(plan)
	require.NoError(t, err)
	run := &model.DiagnosticRun{ID: "limit-report", UserID: user.Id, SubmissionKey: "limit-key", SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1, ModelName: "diagnostic-limit-chat", GroupName: "default", Kind: "connection", Version: plan.Version, State: "running", LeaseOwner: "owner", LeaseUntil: time.Now().Unix() + 120, PlanJSON: string(encoded)}
	step := &model.DiagnosticStep{ID: "limit-step", RunID: run.ID, RequestID: "limit-step", FundingID: "billing:limit-step", State: "queued"}
	require.NoError(t, db.Create(run).Error)
	require.NoError(t, db.Create(step).Error)
	ctx, _ := newAuthenticatedContext(t, http.MethodPost, "/v1/chat/completions", nil, user.Id)
	require.NoError(t, service.PrepareDiagnosticRelay(ctx, run, step, "owner"), "the real diagnostic authorization must succeed before testing rate rejection")
	oldEnabled, oldCount, oldSuccess, oldMinutes := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes
	setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = true, 1, 0, 1
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitDurationMinutes = oldEnabled, oldCount, oldSuccess, oldMinutes
	})
	engine := gin.New()
	engine.POST("/", func(c *gin.Context) { c.Set("id", user.Id); c.Next() }, middleware.ModelRequestRateLimit(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusNoContent, first.Code)
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	values, revoked := executeDiagnosticStep(context.Background(), run, step, plan.Steps[0], "owner")
	require.False(t, revoked)
	require.Equal(t, "failed", values["state"])
	require.Equal(t, "not_started", values["billing_state"])
	require.NoError(t, model.UpdateDiagnosticStep(run.ID, "owner", step.ID, values))
	run.State = "failed"
	view, err := diagnosticView(ctx, run)
	require.NoError(t, err)
	require.Equal(t, "settled", view["fee"].(gin.H)["status"])
	require.EqualValues(t, 0, view["fee"].(gin.H)["quota"])
	var count int64
	require.NoError(t, db.Model(&model.RoutingFundingSession{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDiagnosticChatRequestUsesRelativeUpstreamPath(t *testing.T) {
	request, err := newDiagnosticChatRequest(context.Background(), []byte(`{"model":"chat-test"}`))
	require.NoError(t, err)
	require.Empty(t, request.URL.Scheme)
	require.Empty(t, request.URL.Host)
	require.Equal(t, "/v1/chat/completions", request.URL.String())
	require.Equal(t, "/v1/chat/completions", request.RequestURI)
	require.Equal(t, "application/json", request.Header.Get("Content-Type"))
	require.Equal(t, "https://antrouter.ai/v1/chat/completions", relaycommon.GetFullRequestURL("https://antrouter.ai", request.URL.String(), constant.ChannelTypeOpenAI))
	require.NotContains(t, request.URL.String(), "diagnostics.internal")
}

func TestDiagnosticResponseSupportsGinFlush(t *testing.T) {
	response := &diagnosticResponse{header: http.Header{}}
	require.Implements(t, (*http.Flusher)(nil), response)
	engine := gin.New()
	engine.GET("/", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.WriteString("ok")
		c.Writer.Flush()
	})
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, response.status)
	require.Equal(t, "ok", response.body.String())
}
