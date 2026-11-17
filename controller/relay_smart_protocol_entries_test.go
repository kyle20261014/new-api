package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This fixture owns every accounting table used by the real middleware and send path.
func smartProtocolEntryDB(t *testing.T, name string, kind int, upstream string) (*gorm.DB, *model.Token) {
	t.Helper()
	oldDB, oldLog := model.DB, model.LOG_DB
	memory, redis, batch, consume, retry := common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes
	groups, ratios, prices, models := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString(), ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Midjourney{}, &model.Log{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.Model{}, &model.Vendor{}, &model.Option{}))
	require.NoError(t, model.MigrateTokenRouting(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	require.NoError(t, model.MigrateRoutingFunding(db))
	model.DB, model.LOG_DB = db, db
	common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes = false, false, false, false, 2
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"mj_imagine":0.001,"swap_face":0.001,"kling-v3":0.001,"doubao-seedream-5-0-lite-260128":0.001}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-realtime-preview":1}`))
	require.NoError(t, db.Create(&model.Option{Key: "ModelPrice", Value: ratio_setting.ModelPrice2JSONString()}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "ModelRatio", Value: ratio_setting.ModelRatio2JSONString()}).Error)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		model.InvalidatePricingCache()
		common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes = memory, redis, batch, consume, retry
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(ratios))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(models))
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "smart-entry", Status: common.UserStatusEnabled, Group: "default", Quota: 1_000_000, Setting: `{"billing_preference":"wallet_only"}`}).Error)
	token := &model.Token{UserId: 1, Key: "smartprotocolentrysecret", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1_000_000}
	require.NoError(t, db.Create(token).Error)
	require.NoError(t, db.Create(&model.TokenRouting{TokenID: token.Id, Mode: "smart", SchemaVersion: 1, Version: 1, PolicyJSON: `{"schema_version":1,"mode":"smart","strategy":"balanced"}`}).Error)
	ch := &model.Channel{Type: kind, Name: "smart-entry", Key: "fixture-upstream-key", BaseURL: &upstream, Status: common.ChannelStatusEnabled, Models: name, Group: "vip"}
	require.NoError(t, db.Create(ch).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: name, ChannelId: ch.Id, Enabled: true, Weight: 100}).Error)
	model.InvalidatePricingCache()
	service.InitHttpClient()
	return db, token
}

func assertSmartEntryReserved(t *testing.T, db *gorm.DB) {
	t.Helper()
	var s model.RoutingSubmission
	require.NoError(t, db.First(&s).Error)
	assert.Equal(t, "sending", s.State)
	assert.Equal(t, "vip", s.BusinessGroup)
	assert.Equal(t, "async", s.ProtocolClass)
	assert.Contains(t, s.PriceSnapshot, `"GroupRatio":2`)
	require.Positive(t, s.Quota)
	var funding model.RoutingFundingSession
	require.NoError(t, db.First(&funding, "funding_id = ?", s.FundingID).Error)
	assert.Equal(t, s.Quota, funding.Quota)
	assertSmartEntryBalances(t, db, s.Quota)
}

func assertSmartEntryBalances(t *testing.T, db *gorm.DB, quota int) {
	t.Helper()
	var u model.User
	var token model.Token
	require.NoError(t, db.First(&u, 1).Error)
	require.NoError(t, db.First(&token).Error)
	assert.Equal(t, 1_000_000-quota, u.Quota)
	assert.Equal(t, 1_000_000-quota, token.RemainQuota)
}

func smartEntryInsertFailure(t *testing.T, db *gorm.DB, table string) {
	t.Helper()
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("smart_entry_insert_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			tx.AddError(errors.New("fixture local insert failure"))
		}
	}))
}

func assertSmartEntryTerminalKey(t *testing.T, db *gorm.DB, s model.RoutingSubmission) {
	t.Helper()
	var terminal model.RoutingTerminalObservation
	require.NoError(t, db.First(&terminal, "submission_id = ?", s.SubmissionID).Error)
	assert.Equal(t, "async", terminal.ProtocolClass)
	assert.Equal(t, s.ClientModel, terminal.ClientModel)
	assert.Equal(t, s.BusinessGroup, terminal.BusinessGroup)
	assert.Equal(t, s.ProviderKey, terminal.ProviderKey)
	assert.Equal(t, s.OperationKey, terminal.OperationKey)
}

func smartEntryOpenHealth(t *testing.T, db *gorm.DB, key model.RoutingMetricKey) {
	t.Helper()
	store := model.RoutingMetricsStore{DB: db}
	for _, id := range []string{"protocol-a", "protocol-b", "protocol-c"} {
		_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: key, Stage: model.RoutingStageAttempt, EventID: id, Outcome: "failure", Sent: true}, time.Time{})
		require.NoError(t, err)
	}
	view, err := store.ReadHealth(key, model.RoutingStageAttempt, 0, time.Time{})
	require.NoError(t, err)
	require.Equal(t, model.RoutingHealthOpen, view.Health.State)
	require.False(t, view.ProbeReady)
}

func TestRelayMidjourneySubmitSmartActualEntry(t *testing.T) {
	for _, surface := range []string{"imagine", "swapface"} {
		for _, outcome := range []string{"accepted", "accepted_insert_failure", "unknown", "revoked"} {
			t.Run(surface+"/"+outcome, func(t *testing.T) {
				path, name, body := "/mj/submit/imagine", "mj_imagine", `{"prompt":"a lighthouse"}`
				if surface == "swapface" {
					path, name, body = "/mj/insight-face/swap", "swap_face", `{"sourceBase64":"source","targetBase64":"target"}`
				}
				var sends atomic.Int32
				var db *gorm.DB
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sends.Add(1)
					assert.Equal(t, path, r.URL.Path)
					assert.Equal(t, "fixture-upstream-key", r.Header.Get("mj-api-secret"))
					assertSmartEntryReserved(t, db)
					if outcome == "unknown" {
						panic(http.ErrAbortHandler)
					}
					_, _ = io.WriteString(w, `{"code":1,"description":"accepted","result":"mj-private"}`)
				}))
				defer upstream.Close()
				var token *model.Token
				db, token = smartProtocolEntryDB(t, name, constant.ChannelTypeMidjourney, upstream.URL)
				if outcome == "accepted_insert_failure" {
					smartEntryInsertFailure(t, db, "midjourneys")
				}
				router := gin.New()
				router.Use(middleware.TokenAuth(), middleware.Distribute())
				var plan *service.RoutingPlan
				router.POST(path, func(c *gin.Context) {
					if outcome == "revoked" {
						require.NoError(t, db.Model(token).Update("status", common.TokenStatusDisabled).Error)
					}
					assert.Equal(t, "async", service.RoutingProtocolClass(c), "classification must precede initial selection")
					RelayMidjourney(c)
					plan = service.RoutingPlanFor(c)
					if outcome != "revoked" {
						assert.NotEmpty(t, service.RoutingRetryBarrier(c))
						_, _, e := service.SelectChannelForRequest(c, name, &service.RetryParam{Ctx: c})
						assert.NotNil(t, e)
					}
				})
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer sk-"+token.Key)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.NotNil(t, plan, w.Body.String())
				expected := 1
				if outcome == "revoked" {
					expected = 0
				}
				assert.EqualValues(t, expected, sends.Load())
				assert.Equal(t, expected, plan.Sent)
				assert.Equal(t, 3, plan.Budget)
				if outcome == "revoked" {
					var n int64
					require.NoError(t, db.Model(&model.RoutingSubmission{}).Count(&n).Error)
					assert.Zero(t, n)
					assertSmartEntryBalances(t, db, 0)
					return
				}
				var s model.RoutingSubmission
				require.NoError(t, db.First(&s).Error)
				state := map[string]string{"accepted": "accepted", "accepted_insert_failure": "accepted", "unknown": "unknown"}[outcome]
				assert.Equal(t, state, s.State, w.Body.String())
				assertSmartEntryBalances(t, db, s.Quota)
				assert.Equal(t, "async", s.ProtocolClass)
				if outcome == "accepted_insert_failure" {
					assert.Zero(t, s.LocalTaskID)
					assert.NotEmpty(t, s.RecoveryJSON)
					assert.Equal(t, "submission_accepted", plan.Barrier)
					assert.Contains(t, w.Body.String(), "local_save_pending")
				}
				if outcome == "unknown" {
					assert.Equal(t, "submission_unknown", plan.Barrier)
					assert.Contains(t, w.Body.String(), "submission_id=")
					assert.Contains(t, w.Body.String(), "state=unknown")
					assert.NotContains(t, w.Body.String(), upstream.URL)
					assert.NotContains(t, w.Body.String(), "fixture-upstream-key")
				}
				if outcome == "accepted" {
					assertSmartEntryTerminalKey(t, db, s)
					assert.Positive(t, s.LocalTaskID)
					assert.Equal(t, "submission_accepted", plan.Barrier)
					assert.Equal(t, http.StatusOK, w.Code)
					var mj model.Midjourney
					require.NoError(t, db.First(&mj).Error)
					assert.Equal(t, "mj-private", mj.MjId)
				}
			})
		}
	}
}

func TestRelayTaskSubmitSmartActualNativeAndSharedEntry(t *testing.T) {
	for _, surface := range []string{"native", "shared_video", "shared_responses", "shared_image"} {
		for _, outcome := range []string{"accepted", "accepted_insert_failure", "unknown", "async_open", "sync_open"} {
			t.Run(surface+"/"+outcome, func(t *testing.T) {
				name, kind, upstreamPath := "kling-v3", constant.ChannelTypeKling, "/v1/videos/text2video"
				if surface == "shared_image" {
					name, kind, upstreamPath = "doubao-seedream-5-0-lite-260128", constant.ChannelTypeDoubaoVideo, "/api/v3/images/generations"
				}
				var sends atomic.Int32
				var db *gorm.DB
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sends.Add(1)
					assert.Equal(t, upstreamPath, r.URL.Path)
					assertSmartEntryReserved(t, db)
					if outcome == "unknown" {
						panic(http.ErrAbortHandler)
					}
					w.Header().Set("Content-Type", "application/json")
					if surface == "shared_image" {
						_, _ = io.WriteString(w, `{"created":123,"data":[{"url":"http://`+r.Host+`/fixture.png"}],"usage":{"generated_images":1}}`)
						return
					}
					_, _ = io.WriteString(w, `{"code":0,"data":{"task_id":"kling-private","task_status":"submitted"}}`)
				}))
				defer upstream.Close()
				var token *model.Token
				db, token = smartProtocolEntryDB(t, name, kind, upstream.URL)
				if strings.HasSuffix(outcome, "_open") {
					generation := pluginruntime.DefaultRegistry.Generation()
					require.NotNil(t, generation)
					plugin, operation := "kling", "video.create"
					if surface == "native" {
						binding, ok := generation.LookupDeclaredRoute(http.MethodPost, "/kling/v1/videos/text2video")
						require.True(t, ok)
						operation = "native:" + binding.Route.Action
					}
					if surface == "shared_responses" {
						operation = "responses"
					}
					if surface == "shared_image" {
						plugin, operation = "doubao", "image.generate"
					}
					smartEntryOpenHealth(t, db, model.RoutingMetricKey{ClientModel: name, BusinessGroup: "vip", ProtocolClass: strings.TrimSuffix(outcome, "_open"), ProviderKey: fmt.Sprintf("plugin:%s:%d", plugin, generation.Number), OperationKey: operation})
				}
				if outcome == "accepted_insert_failure" {
					smartEntryInsertFailure(t, db, "tasks")
				}
				router := gin.New()
				var plan *service.RoutingPlan
				router.Use(func(c *gin.Context) { defer func() { plan = service.RoutingPlanFor(c) }(); c.Next() }, middleware.TokenAuth())
				path, body := "/kling/v1/videos/text2video", `{"model_name":"kling-v3","prompt":"lighthouse"}`
				inspect := func(c *gin.Context) {
					if surface == "native" {
						RelayTask(c)
					} else {
						RelayTaskPluginEndpoint(c, RelayTask)
					}
					plan = service.RoutingPlanFor(c)
					assert.NotEmpty(t, service.RoutingRetryBarrier(c))
					_, _, err := service.SelectChannelForRequest(c, name, &service.RetryParam{Ctx: c})
					assert.NotNil(t, err)
				}
				if surface == "native" {
					generation := pluginruntime.DefaultRegistry.Generation()
					require.NotNil(t, generation)
					binding, ok := generation.LookupDeclaredRoute(http.MethodPost, path)
					require.True(t, ok)
					router.POST(path, func(c *gin.Context) {
						c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: binding.Plugin, Route: binding.Route})
					}, middleware.PrepareTaskPluginRoute(), middleware.Distribute(), inspect)
				} else {
					path, body = "/v1/videos", `{"model":"kling-v3","prompt":"lighthouse"}`
					if surface == "shared_responses" {
						path, body = "/v1/responses", `{"model":"kling-v3","input":"lighthouse","background":true}`
					}
					if surface == "shared_image" {
						path, body = "/v1/images/generations", `{"model":"doubao-seedream-5-0-lite-260128","prompt":"lighthouse","response_format":"url"}`
					}
					router.POST(path, middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), inspect)
				}
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer sk-"+token.Key)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.NotNil(t, plan, w.Body.String())
				if outcome == "async_open" {
					assert.Zero(t, plan.FirstSelected.ChannelID, "correct protocol health must block the earliest selection")
					assert.Zero(t, sends.Load())
					assert.Zero(t, plan.Sent)
					return
				}
				assert.EqualValues(t, 1, sends.Load())
				assert.Equal(t, 1, plan.Sent)
				assert.Equal(t, 3, plan.Budget)
				var s model.RoutingSubmission
				require.NoError(t, db.First(&s).Error)
				assertSmartEntryBalances(t, db, s.Quota)
				if outcome == "accepted_insert_failure" {
					assert.Equal(t, "accepted", s.State)
					assert.Zero(t, s.LocalTaskID)
					assert.NotEmpty(t, s.RecoveryJSON)
					assert.Equal(t, "submission_accepted", plan.Barrier)
					assert.GreaterOrEqual(t, w.Code, 400)
					return
				}
				if outcome != "unknown" {
					assertSmartEntryTerminalKey(t, db, s)
					assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
					assert.Equal(t, "submission_accepted", plan.Barrier)
					assert.Equal(t, "accepted", s.State)
					assert.Positive(t, s.LocalTaskID)
					var task model.Task
					require.NoError(t, db.First(&task).Error)
					if surface == "shared_image" {
						assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
						assert.Contains(t, w.Body.String(), `"data":`)
					} else {
						assert.Equal(t, "kling-private", task.GetUpstreamTaskID())
					}
					if surface == "shared_responses" {
						assert.Contains(t, w.Body.String(), `"object":"response"`)
					}
				} else {
					assert.Equal(t, "unknown", s.State)
					assert.Equal(t, "submission_unknown", plan.Barrier)
					assert.GreaterOrEqual(t, w.Code, 400)
				}
			})
		}
	}
}

func TestWssHelperSmartActualRealtimeEntry(t *testing.T) {
	for _, outcome := range []string{"connected", "handshake_rejected", "revoked", "realtime_open", "sync_open"} {
		t.Run(outcome, func(t *testing.T) {
			var sends atomic.Int32
			var db *gorm.DB
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				assert.Equal(t, "/v1/realtime", r.URL.Path)
				assert.Equal(t, "Bearer fixture-upstream-key", r.Header.Get("Authorization"))
				var reservation model.RoutingFundingSession
				if !assert.NoError(t, db.First(&reservation).Error) {
					return
				}
				assert.Equal(t, "reserved", reservation.Status)
				assert.Zero(t, reservation.Quota, "empty realtime session reserves zero before usage")
				if outcome == "handshake_rejected" {
					http.Error(w, "try another channel", http.StatusServiceUnavailable)
					return
				}
				conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
				if !assert.NoError(t, err) {
					return
				}
				defer conn.Close()
				// Invalid payload after a successful handshake forces response termination.
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{`))
			}))
			defer upstream.Close()
			var token *model.Token
			db, token = smartProtocolEntryDB(t, "gpt-4o-realtime-preview", constant.ChannelTypeOpenAI, upstream.URL)
			if strings.HasSuffix(outcome, "_open") {
				smartEntryOpenHealth(t, db, model.RoutingMetricKey{ClientModel: "gpt-4o-realtime-preview", BusinessGroup: "vip", ProtocolClass: strings.TrimSuffix(outcome, "_open"), ProviderKey: "builtin:1:v1", OperationKey: "realtime"})
			}
			// Three eligible real channels make the request-wide budget observable.
			for range 2 {
				ch := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "fixture-upstream-key", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled, Models: "gpt-4o-realtime-preview", Group: "vip"}
				require.NoError(t, db.Create(&ch).Error)
				require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: ch.Models, ChannelId: ch.Id, Enabled: true, Weight: 100}).Error)
			}
			done := make(chan *service.RoutingPlan, 1)
			router := gin.New()
			var initialPlan *service.RoutingPlan
			router.Use(func(c *gin.Context) { defer func() { initialPlan = service.RoutingPlanFor(c) }(); c.Next() })
			router.GET("/v1/realtime", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) {
				if outcome == "revoked" {
					require.NoError(t, db.Model(token).Update("status", common.TokenStatusDisabled).Error)
				}
				Relay(c, types.RelayFormatOpenAIRealtime)
				if outcome != "revoked" {
					_, _, selectionErr := service.SelectChannelForRequest(c, "gpt-4o-realtime-preview", &service.RetryParam{Ctx: c})
					if assert.NotNil(t, selectionErr) {
						reason := "upstream_websocket_connected"
						if outcome == "handshake_rejected" {
							reason = "attempt_budget_exhausted"
						}
						assert.Equal(t, reason, selectionErr.Message)
					}
				}
				done <- service.RoutingPlanFor(c)
			})
			gateway := httptest.NewServer(router)
			defer gateway.Close()
			header := http.Header{"Authorization": []string{"Bearer sk-" + token.Key}}
			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/realtime?model=gpt-4o-realtime-preview", header)
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if outcome == "realtime_open" {
				require.Error(t, err)
				require.NotNil(t, initialPlan)
				assert.Zero(t, initialPlan.FirstSelected.ChannelID)
				assert.Zero(t, sends.Load())
				return
			}
			require.NoError(t, err)
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = conn.ReadMessage()
			select {
			case plan := <-done:
				require.NotNil(t, plan)
				expected := 1
				if outcome == "revoked" {
					expected = 0
				}
				if outcome == "handshake_rejected" {
					expected = 3
				}
				assert.EqualValues(t, expected, sends.Load())
				assert.Equal(t, expected, plan.Sent)
				assert.Equal(t, "vip", plan.Selected.Group)
				assert.Equal(t, 3, plan.Budget)
				if outcome == "connected" {
					assert.Equal(t, "upstream_websocket_connected", plan.Barrier)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Realtime entry did not finish")
			}
		})
	}
}

func TestRelayTaskPluginEndpointSmartMixedProviderHealthIsolation(t *testing.T) {
	for _, offlineProtocol := range []string{"sync", "async"} {
		t.Run(offlineProtocol, func(t *testing.T) {
			var ordinarySends, pluginSends atomic.Int32
			var db *gorm.DB
			selectedGroups := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/responses":
					ordinarySends.Add(1)
					assert.Equal(t, "default", <-selectedGroups)
					var funding model.RoutingFundingSession
					require.NoError(t, db.First(&funding).Error)
					assert.Equal(t, "reserved", funding.Status)
					require.Positive(t, funding.Quota)
					assertSmartEntryBalances(t, db, funding.Quota)
					_, _ = io.WriteString(w, `{"id":"resp_owned","object":"response","created_at":1,"status":"completed","model":"kling-v3","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
				case "/v1/videos/text2video":
					pluginSends.Add(1)
					assert.Equal(t, "vip", <-selectedGroups)
					assertSmartEntryReserved(t, db)
					_, _ = io.WriteString(w, `{"code":0,"data":{"task_id":"kling-private","task_status":"submitted"}}`)
				default:
					t.Errorf("unexpected actual upstream path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			var token *model.Token
			db, token = smartProtocolEntryDB(t, "kling-v3", constant.ChannelTypeKling, upstream.URL)
			ordinary := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "ordinary-key", BaseURL: &upstream.URL, Status: common.ChannelStatusEnabled, Models: "kling-v3", Group: "default"}
			require.NoError(t, db.Create(&ordinary).Error)
			require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "kling-v3", ChannelId: ordinary.Id, Enabled: true, Weight: 100}).Error)
			require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", token.Id).Update("policy_json", `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"kling-v3":{"groups":["default","vip"],"strategy":"manual"}}}`).Error)
			smartEntryOpenHealth(t, db, model.RoutingMetricKey{ClientModel: "kling-v3", BusinessGroup: "default", ProtocolClass: offlineProtocol, ProviderKey: "builtin:1:v1", OperationKey: "responses"})
			var plan *service.RoutingPlan
			router := gin.New()
			router.POST("/v1/responses", middleware.TokenAuth(), middleware.RouteTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(c *gin.Context) {
				selectedGroups <- common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
				RelayTaskPluginEndpoint(c, func(c *gin.Context) { Relay(c, types.RelayFormatOpenAIResponses) })
				plan = service.RoutingPlanFor(c)
				_, _, retryErr := service.SelectChannelForRequest(c, "kling-v3", &service.RetryParam{Ctx: c, ModelName: "kling-v3", TokenGroup: "default", Retry: common.GetPointer(0)})
				assert.NotNil(t, retryErr, "output or acceptance must stop replay")
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"kling-v3","input":"lighthouse","background":true}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.NotNil(t, plan, w.Body.String())
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, 1, plan.Sent)
			assert.Equal(t, 3, plan.Budget)
			if offlineProtocol == "sync" {
				assert.Equal(t, "vip", plan.FirstSelected.Group)
				assert.EqualValues(t, 1, pluginSends.Load())
				assert.Zero(t, ordinarySends.Load())
			} else {
				assert.Equal(t, "default", plan.FirstSelected.Group)
				assert.EqualValues(t, 1, ordinarySends.Load())
				assert.Zero(t, pluginSends.Load())
			}
		})
	}
}

func TestResponsesWSNewAPIBoundTaskPluginRankIsolation(t *testing.T) {
	db, token := smartProtocolEntryDB(t, "kling-v3", constant.ChannelTypeNewAPI, "http://owned.invalid")
	var channel model.Channel
	require.NoError(t, db.First(&channel).Error)
	channel.SetSetting(dto.ChannelSettings{ResponsesWebSocketEnabled: true, TaskExtendPluginKeys: []string{"kling"}})
	require.NoError(t, db.Save(&channel).Error)
	generation := pluginruntime.DefaultRegistry.Generation()
	require.NotEmpty(t, generation.LookupEndpointCandidates(http.MethodPost, "/v1/responses", "kling-v3"))
	now := time.Now().Truncate(time.Second)
	key := model.RoutingMetricKey{ClientModel: "kling-v3", BusinessGroup: "vip", ProtocolClass: "responses_ws", ProviderKey: "builtin:60:v1", OperationKey: "responses"}
	_, err := (model.RoutingMetricsStore{DB: db}).UpsertSnapshot(model.RoutingMetricBucket{RoutingMetricKey: key, Stage: model.RoutingStageAttempt, NodeBootID: "owned-ws-rank", ChannelID: channel.Id, Minute: now.Unix() / 60 * 60, Revision: 1, Eligible: 20, Success: 20, SpeedSamples: 5, SpeedTotalMS: 500, LastObservationAt: now.Unix()}, now)
	require.NoError(t, err)
	runner, _ := newResponsesWSTestRunner(t, token)
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", strings.NewReader(`{"model":"kling-v3","input":"lighthouse"}`))
	errAPI := runner(request, "owned-ws-rank", func(c *gin.Context) *types.NewAPIError {
		require.Equal(t, http.MethodPost, c.Request.Method, "real WS runner rewrites every call to HTTP POST")
		c.Set("mowan_responses_ws", true)
		constraints := service.GetChannelConstraints(c)
		constraints.AddFilter(appdto.ChannelFilter{Kind: appdto.FilterResponsesWebSocket})
		matches, _ := model.ChannelSatisfiesFilters(&channel, "kling-v3", constraints.Filters)
		require.True(t, matches)
		ranked, err := service.RoutingRankCandidates(c, "kling-v3", []string{"vip"})
		require.NoError(t, err)
		result, err := smartrouting.Rank(smartrouting.RankRequest{Strategy: "speed", ClientModel: "kling-v3", Now: time.Now(), Candidates: ranked})
		require.NoError(t, err)
		require.Len(t, result.Candidates, 1)
		row := result.Candidates[0]
		assert.False(t, row.MixedProviderOperation, "an HTTP-only task plugin never executes the WS request")
		assert.True(t, row.ReliabilityTrusted)
		assert.True(t, row.SpeedTrusted)
		assert.Len(t, row.Observations, 1, "catalog must expose only the executable native WS variant")
		if len(row.Observations) == 1 {
			assert.Equal(t, "builtin:60:v1", row.Observations[0].Key.ProviderKey)
			assert.Equal(t, "responses_ws", row.Observations[0].Key.ProtocolClass)
			assert.Equal(t, float64(100), row.Observations[0].MeanMS)
		}
		c.Set("mowan_responses_ws", false)
		constraints.Filters = nil
		httpRanked, err := service.RoutingRankCandidates(c, "kling-v3", []string{"vip"})
		require.NoError(t, err)
		httpResult, err := smartrouting.Rank(smartrouting.RankRequest{Strategy: "speed", ClientModel: "kling-v3", Now: time.Now(), Candidates: httpRanked})
		require.NoError(t, err)
		require.Len(t, httpResult.Candidates, 1)
		assert.True(t, httpResult.Candidates[0].MixedProviderOperation, "ordinary HTTP keeps plugin async and builtin sync variants")
		var pluginAsync, builtinSync bool
		for _, observation := range httpResult.Candidates[0].Observations {
			pluginAsync = pluginAsync || strings.HasPrefix(observation.Key.ProviderKey, "plugin:kling:") && observation.Key.ProtocolClass == "async"
			builtinSync = builtinSync || observation.Key.ProviderKey == "builtin:60:v1" && observation.Key.ProtocolClass == "sync"
		}
		assert.True(t, pluginAsync)
		assert.True(t, builtinSync)
		return nil
	})
	require.Nil(t, errAPI)
}
