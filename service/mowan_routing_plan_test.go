package service

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRoutingPlanTest(t *testing.T) (*gorm.DB, func() *gin.Context) {
	t.Helper()
	db := setupChannelSelectAutoGroupsTest(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TokenRouting{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "routing-user", Status: common.UserStatusEnabled, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 102, UserId: 101, Key: "routing-token", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	row := &model.TokenRouting{TokenID: 102, SchemaVersion: 1, Mode: "smart", Version: 1, PolicyJSON: `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"routing-test":{"strategy":"manual","groups":["vip","default"]}}}`}
	require.NoError(t, db.Create(row).Error)
	common.RetryTimes = 5
	return db, func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		c.Set("id", 101)
		c.Set("token_id", 102)
		c.Set(smartrouting.ContextKey, row)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		return c
	}
}

func selectAndPrepareRoutingTest(t *testing.T, c *gin.Context) (*model.Channel, string) {
	t.Helper()
	ch, g, err := SelectChannelForRequest(c, "routing-test", &RetryParam{Ctx: c, ModelName: "routing-test"})
	require.Nil(t, err)
	require.NotNil(t, ch)
	c.Set("channel_id", ch.Id)
	common.SetContextKey(c, constant.ContextKeyChannelKey, ch.Key)
	return ch, g
}

func TestRoutingDynamicCandidatesFollowGrantsAndExclusions(t *testing.T) {
	for _, strategy := range []string{"balanced", "price", "speed", "success"} {
		t.Run(strategy, func(t *testing.T) {
			db, ctx := setupRoutingPlanTest(t)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Model{}, &model.Vendor{}))
			model.InvalidatePricingCache()
			t.Cleanup(model.InvalidatePricingCache)
			createChannelSelectAutoGroupsChannel(t, db, 3801, "all", "gpt-4o")
			model.InitChannelCache()
			policy := smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: strategy, ExcludedGroups: []string{"default", "vip"}}
			save := func() {
				encoded, err := common.Marshal(policy)
				require.NoError(t, err)
				require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Update("policy_json", string(encoded)).Error)
			}
			selectGroup := func(want string) {
				channel, group, err := SelectChannelForRequest(ctx(), "gpt-4o", &RetryParam{})
				require.Nil(t, err)
				require.Equal(t, want, group)
				require.Equal(t, 3801, channel.Id)
			}
			reject := func(reason string) {
				channel, _, err := SelectChannelForRequest(ctx(), "gpt-4o", &RetryParam{})
				require.Nil(t, channel)
				require.NotNil(t, err)
				require.Equal(t, reason, err.Message)
			}
			save()
			reject("routing_no_candidate")
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"audit":3}`))
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","audit":"New grant"}`))
			selectGroup("audit")
			policy.ExcludedModelGroups = map[string][]string{"gpt-4o": {"audit"}}
			save()
			reject("routing_no_candidate")
			policy.ExcludedModelGroups = nil
			policy.ExcludedModels = []string{"gpt-4o"}
			save()
			reject("routing_model_forbidden")
			policy.ExcludedModels = nil
			save()
			selectGroup("audit")
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
			reject("routing_no_candidate")
		})
	}
}

func TestRoutingPlanInitialRetryDedupAndFiniteExhaustion(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprint(memory), func(t *testing.T) {
			db, newContext := setupRoutingPlanTest(t)
			common.MemoryCacheEnabled = memory
			createChannelSelectAutoGroupsChannel(t, db, 3101, "vip", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 3102, "vip", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 3103, "default", "routing-test")
			require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "routing-test", ChannelId: 3101, Enabled: true, Weight: 100}).Error)
			model.InitChannelCache()
			c := newContext()
			seen := map[int]bool{}
			for i := 0; i < 3; i++ {
				ch, g := selectAndPrepareRoutingTest(t, c)
				require.False(t, seen[ch.Id])
				seen[ch.Id] = true
				if i < 2 {
					require.Equal(t, "vip", g)
				} else {
					require.Equal(t, "default", g)
				}
				require.NoError(t, BeginRoutingSend(c))
				require.NoError(t, BeginRoutingSend(c))
				require.Equal(t, i+1, RoutingPlanFor(c).Sent)
			}
			_, _, err := SelectChannelForRequest(c, "routing-test", &RetryParam{Ctx: c})
			require.NotNil(t, err)
			require.Equal(t, "routing_channels_exhausted", err.Message)
			require.Equal(t, 3, RoutingPlanFor(c).Sent)
		})
	}
}

func TestRoutingPlanBudgetAndAuthoritySurviveConfigurationVersion(t *testing.T) {
	db, newContext := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3201, "vip", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 3202, "default", "routing-test")
	model.InitChannelCache()
	common.RetryTimes = 0
	c := newContext()
	_, g := selectAndPrepareRoutingTest(t, c)
	require.Equal(t, "vip", g)
	deadline := RoutingPlanFor(c).Deadline
	require.NoError(t, BeginRoutingSend(c))
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Updates(map[string]any{"version": 2, "policy_json": `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"routing-test":{"strategy":"manual","groups":["default"]}}}`}).Error)
	_, _, err := SelectChannelForRequest(c, "routing-test", &RetryParam{Ctx: c})
	require.NotNil(t, err)
	require.Equal(t, "attempt_budget_exhausted", err.Message)
	require.Equal(t, deadline, RoutingPlanFor(c).Deadline)
	common.RetryTimes = 5
	fresh := newContext()
	_, g = selectAndPrepareRoutingTest(t, fresh)
	require.Equal(t, "default", g)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 102).Update("status", common.TokenStatusDisabled).Error)
	require.Error(t, BeginRoutingSend(fresh))
	require.Zero(t, RoutingPlanFor(fresh).Sent)
}

func TestRoutingAffinityPrecedesManualAndStrictCannotChangeBillingGroup(t *testing.T) {
	db, newContext := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3301, "vip", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 3302, "default", "routing-test")
	model.InitChannelCache()
	setting := operation_setting.GetChannelAffinitySetting()
	old := *setting
	t.Cleanup(func() { *setting = old })
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, SwitchOnSuccess: true, DefaultTTLSeconds: 60, Rules: []operation_setting.ChannelAffinityRule{{Name: t.Name(), ModelRegex: []string{"routing-test"}, SessionMode: "prefer", KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "Session-Id"}}}}}
	ctx := func() *gin.Context { c := newContext(); c.Request.Header.Set("Session-Id", t.Name()); return c }
	seed := ctx()
	GetPreferredChannelByAffinity(seed, "routing-test", "smart")
	key, _, ok := getChannelAffinityContext(seed)
	require.True(t, ok)
	require.NoError(t, getSmartAffinityCache().SetWithTTL(key, routingBinding{"default", 3302}, time.Minute))
	c := ctx()
	ch, g := selectAndPrepareRoutingTest(t, c)
	require.Equal(t, 3302, ch.Id)
	require.Equal(t, "default", g)
	setting.Rules[0].SessionMode = "strict"
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Update("policy_json", `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"routing-test":{"strategy":"manual","groups":["vip"]}}}`).Error)
	_, _, err := SelectChannelForRequest(ctx(), "routing-test", &RetryParam{})
	require.NotNil(t, err)
	require.Equal(t, "strict_session_binding_unavailable", err.Message)
	require.NoError(t, getSmartAffinityCache().SetWithTTL(key, routingBinding{"default", 3302}, time.Nanosecond))
	time.Sleep(time.Millisecond)
	ch, g = selectAndPrepareRoutingTest(t, ctx())
	require.Equal(t, 3301, ch.Id)
	require.Equal(t, "vip", g)
}

func TestRoutingAffinityModesRevalidateBindingAndRecordOnlySuccess(t *testing.T) {
	for _, mode := range []string{"off", "prefer", "strict"} {
		for _, change := range []string{"none", "revoked", "policy_removed", "disabled", "soft_offline", "soft_allowed"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				db, newContext := setupRoutingPlanTest(t)
				createChannelSelectAutoGroupsChannel(t, db, 3901, "default", "routing-test")
				createChannelSelectAutoGroupsChannel(t, db, 3902, "vip", "routing-test")
				model.InitChannelCache()
				policy := smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: "custom", Models: map[string]smartrouting.ModelPolicy{"routing-test": {Strategy: "manual", Groups: []string{"default", "vip"}, TrySoftOffline: change == "soft_allowed"}}}
				if change == "policy_removed" {
					policy.Models["routing-test"] = smartrouting.ModelPolicy{Strategy: "manual", Groups: []string{"default"}}
				}
				encoded, err := common.Marshal(policy)
				require.NoError(t, err)
				require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Updates(map[string]any{"version": 2, "policy_json": string(encoded)}).Error)
				affinity := operation_setting.GetChannelAffinitySetting()
				old := *affinity
				t.Cleanup(func() { *affinity = old })
				*affinity = operation_setting.ChannelAffinitySetting{Enabled: true, SwitchOnSuccess: true, DefaultTTLSeconds: 60, Rules: []operation_setting.ChannelAffinityRule{{Name: t.Name(), ModelRegex: []string{"routing-test"}, SessionMode: mode, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "Session-Id"}}}}}
				ctx := func() *gin.Context {
					c := newContext()
					c.Request.Header.Set("Session-Id", t.Name())
					return c
				}
				seed := ctx()
				GetPreferredChannelByAffinity(seed, "routing-test", "smart")
				key, _, ok := getChannelAffinityContext(seed)
				require.True(t, ok)
				cache := getSmartAffinityCache()
				t.Cleanup(func() { _, _ = cache.DeleteMany([]string{key}) })
				require.NoError(t, cache.SetWithTTL(key, routingBinding{"vip", 3902}, time.Minute))
				switch change {
				case "revoked":
					require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
				case "disabled":
					require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 3902).Update("status", common.ChannelStatusManuallyDisabled).Error)
				case "soft_offline", "soft_allowed":
					store := model.RoutingMetricsStore{DB: db}
					k := model.RoutingMetricKey{ClientModel: "routing-test", BusinessGroup: "vip", ProtocolClass: "sync", ProviderKey: "builtin:1:v1", OperationKey: "chat"}
					for _, id := range []string{"one", "two", "three"} {
						_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: k, Stage: model.RoutingStageAttempt, EventID: id, Outcome: "failure", Sent: true}, time.Time{})
						require.NoError(t, err)
					}
				}
				c := ctx()
				channel, group, selectErr := SelectChannelForRequest(c, "routing-test", &RetryParam{})
				valid := change == "none" || change == "soft_allowed"
				if mode == "strict" && !valid {
					require.Nil(t, channel)
					require.NotNil(t, selectErr)
					require.Equal(t, "strict_session_binding_unavailable", selectErr.Message)
					return
				}
				require.Nil(t, selectErr)
				want := "default"
				if mode != "off" && valid {
					want = "vip"
				}
				require.Equal(t, want, group)
				if change != "none" || mode == "off" {
					return
				}
				c.Set("channel_id", channel.Id)
				common.SetContextKey(c, constant.ContextKeyChannelKey, channel.Key)
				require.NoError(t, BeginRoutingSend(c))
				next, group, retryErr := SelectChannelForRequest(c, "routing-test", &RetryParam{})
				if mode == "strict" {
					require.Nil(t, next)
					require.NotNil(t, retryErr)
					require.Equal(t, "strict_session_binding_unavailable", retryErr.Message)
					return
				}
				require.Nil(t, retryErr)
				require.Equal(t, "default", group)
				c.Set("channel_id", next.Id)
				RecordChannelAffinity(c, next.Id)
				binding, found, err := cache.Get(key)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, routingBinding{"vip", 3902}, binding, "a failed fallback cannot replace the binding")
				MarkRequestPolicySuccess(c, nil)
				RecordChannelAffinity(c, next.Id)
				binding, found, err = cache.Get(key)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, routingBinding{"default", 3901}, binding)
			})
		}
	}
}

func TestRoutingSafetyBarriersAreMonotonicAndLegacyUnchanged(t *testing.T) {
	_, newContext := setupRoutingPlanTest(t)
	for _, reason := range []string{"submission_accepted", "submission_unknown", "upstream_websocket_connected"} {
		c := newContext()
		MarkRoutingBarrier(c, reason)
		MarkRoutingBarrier(c, "later")
		require.Equal(t, reason, RoutingRetryBarrier(c))
		require.False(t, ShouldRetryRelayError(c, types.NewError(errors.New("upstream"), types.ErrorCodeDoRequestFailed), 4))
	}
	c := newContext()
	_, _ = c.Writer.Write([]byte("heartbeat"))
	require.Equal(t, "response_started", RoutingRetryBarrier(c))
	legacy, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, BeginRoutingSend(legacy))
	require.Nil(t, RoutingPlanFor(legacy))
}

func TestRoutingPlanPriorityAndAllAbilityStayFinite(t *testing.T) {
	for _, memory := range []bool{false, true} {
		t.Run(fmt.Sprint(memory), func(t *testing.T) {
			db, ctx := setupRoutingPlanTest(t)
			common.MemoryCacheEnabled = memory
			createChannelSelectAutoGroupsChannel(t, db, 3401, "all", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 3402, "vip", "routing-test")
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 3401).Update("priority", 20).Error)
			require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 3401).Update("priority", 20).Error)
			model.InitChannelCache()
			c := ctx()
			for _, id := range []int{3401, 3402} {
				ch, g := selectAndPrepareRoutingTest(t, c)
				require.Equal(t, id, ch.Id)
				require.Equal(t, "vip", g)
				require.NoError(t, BeginRoutingSend(c))
			}
			_, _, err := SelectChannelForRequest(c, "routing-test", &RetryParam{Ctx: c})
			require.NotNil(t, err)
			require.Equal(t, "routing_channels_exhausted", err.Message)
		})
	}
}

func TestRoutingOriginPreservesGroupAndOnlyRotatesUnsentKeys(t *testing.T) {
	db, ctx := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3501, "default", "routing-test")
	channel, err := model.GetChannelById(3501, true)
	require.NoError(t, err)
	channel.Key = "first\nsecond"
	channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusEnabled, 1: common.ChannelStatusEnabled}}
	require.NoError(t, db.Save(channel).Error)
	model.InitChannelCache()
	c := ctx()
	require.NoError(t, BindRoutingOrigin(c, "routing-test", "default", 3501))
	for i, want := range []string{"first", "second"} {
		ch, g := selectAndPrepareRoutingTest(t, c)
		require.Equal(t, "default", g)
		key, index, e := RoutingChannelKey(c, ch, "first", 0)
		require.NoError(t, e)
		require.Equal(t, want, key)
		common.SetContextKey(c, constant.ContextKeyChannelKey, key)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
		require.NoError(t, BeginRoutingSend(c))
		require.Equal(t, i+1, RoutingPlanFor(c).Sent)
	}
	ch, _ := selectAndPrepareRoutingTest(t, c)
	_, _, err = RoutingChannelKey(c, ch, "first", 0)
	require.EqualError(t, err, "origin_task_keys_exhausted")
	outside := ctx()
	GetChannelConstraints(outside).AddPin(dto.ChannelPin{ChannelId: 999, Source: dto.PinSourceToken, Rank: dto.PinRankToken})
	require.EqualError(t, BindRoutingOrigin(outside, "routing-test", "default", 3501), "origin_task_channel_conflict")
}

func TestRoutingPolicyReloadAndFinalFiltersCannotEscapeKeyCandidates(t *testing.T) {
	db, ctx := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3601, "vip", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 3602, "default", "routing-test")
	model.InitChannelCache()
	c := ctx()
	_, g := selectAndPrepareRoutingTest(t, c)
	require.Equal(t, "vip", g)
	p := RoutingPlanFor(c)
	budget, deadline := p.Budget, p.Deadline
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Updates(map[string]any{"version": 2, "policy_json": `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"routing-test":{"strategy":"manual","groups":["default"]}}}`}).Error)
	_, g = selectAndPrepareRoutingTest(t, c)
	require.Equal(t, "default", g)
	require.Equal(t, budget, p.Budget)
	require.Equal(t, deadline, p.Deadline)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 3602).Update("enabled", false).Error)
	require.ErrorContains(t, BeginRoutingSend(c), "routing_ability_unavailable")
	require.Zero(t, p.Sent)
	pinned := ctx()
	GetChannelConstraints(pinned).AddPin(dto.ChannelPin{ChannelId: 3601, Source: dto.PinSourceToken, Rank: dto.PinRankToken})
	_, _, e := SelectChannelForRequest(pinned, "routing-test", &RetryParam{Ctx: pinned})
	require.NotNil(t, e)
	require.Equal(t, "pinned_channel_unavailable", e.Message)
}

func TestRoutingFirstDataExcludesControlFrames(t *testing.T) {
	for _, data := range []string{`: PING`, `[DONE]`, `{"choices":[{"delta":{"role":"assistant","content":""}}]}`, `{"usage":{"completion_tokens":3}}`, `{"type":"response.created"}`, `{"type":"session.updated"}`, `{"type":"content_block_start","content_block":{"type":"text","text":""}}`} {
		require.False(t, routingStreamHasOutput(data), data)
	}
	for _, data := range []string{`{"choices":[{"delta":{"content":"hi"}}]}`, `{"choices":[{"delta":{"reasoning_content":"think"}}]}`, `{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{"}}]}}]}`, `{"type":"response.output_text.delta","delta":"hi"}`, `{"type":"response.audio.delta","delta":"YQ=="}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`} {
		require.True(t, routingStreamHasOutput(data), data)
	}
}

func TestRoutingPolicyChangeAtSendRebuildsWithoutSendingOrResettingBudget(t *testing.T) {
	db, ctx := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3651, "vip", "routing-test")
	createChannelSelectAutoGroupsChannel(t, db, 3652, "default", "routing-test")
	model.InitChannelCache()
	c := ctx()
	selectAndPrepareRoutingTest(t, c)
	p := RoutingPlanFor(c)
	deadline, budget := p.Deadline, p.Budget
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Updates(map[string]any{"version": 2, "policy_json": `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"routing-test":{"strategy":"manual","groups":["default","vip"]}}}`}).Error)
	require.ErrorContains(t, BeginRoutingSend(c), "routing_configuration_changed")
	require.Zero(t, p.Sent)
	require.Empty(t, p.Tried)
	retry := &RetryParam{Ctx: c}
	require.True(t, RetryRoutingRebuild(c, retry))
	retry.IncreaseRetry()
	require.Zero(t, retry.GetRetry())
	ch, g := selectAndPrepareRoutingTest(t, c)
	require.Equal(t, 3652, ch.Id)
	require.Equal(t, "default", g)
	require.Equal(t, deadline, p.Deadline)
	require.Equal(t, budget, p.Budget)
	require.NoError(t, BeginRoutingSend(c))
	require.Equal(t, 1, p.Sent)
}

func TestRoutingPlaygroundTemporaryPolicyHasExactSessionBoundary(t *testing.T) {
	db, newContext := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3701, "vip", "routing-test")
	model.InitChannelCache()
	playground := func() *gin.Context {
		c := newContext()
		c.Set("token_id", 0)
		c.Set("mowan_routing_playground", true)
		c.Request.URL.Path = "/pg/chat/completions"
		return c
	}
	c := playground()
	selectAndPrepareRoutingTest(t, c)
	require.NoError(t, BeginRoutingSend(c))
	for _, kind := range []string{"path", "pat", "token"} {
		denied := playground()
		switch kind {
		case "path":
			denied.Request.URL.Path = "/v1/chat/completions"
		case "pat":
			denied.Set("access_token_ref", "legacy-pat")
		case "token":
			denied.Set("token_id", 102)
		}
		_, _, err := SelectChannelForRequest(denied, "routing-test", &RetryParam{Ctx: denied})
		require.NotNil(t, err, kind)
	}
	fresh := playground()
	selectAndPrepareRoutingTest(t, fresh)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 101).Update("status", common.UserStatusDisabled).Error)
	require.Error(t, BeginRoutingSend(fresh))
	require.Zero(t, RoutingPlanFor(fresh).Sent)
}

func TestRoutingPreparedProbeMustStillOwnEpochAtActualSend(t *testing.T) {
	db, ctx := setupRoutingPlanTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 3751, "vip", "routing-test")
	model.InitChannelCache()
	c := ctx()
	selectAndPrepareRoutingTest(t, c)
	store := model.RoutingMetricsStore{DB: db}
	k := model.RoutingMetricKey{ClientModel: "routing-test", BusinessGroup: "vip", ProtocolClass: "sync", ProviderKey: "builtin:1:v1", OperationKey: "chat"}
	for _, id := range []string{"a", "b", "c"} {
		_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: k, Stage: model.RoutingStageAttempt, EventID: id, Outcome: "failure", Sent: true}, time.Time{})
		require.NoError(t, err)
	}
	claim, won, err := store.ClaimProbeExplicit(k, model.RoutingStageAttempt, "stale-prepared", time.Time{})
	require.NoError(t, err)
	require.True(t, won)
	p := RoutingPlanFor(c)
	p.Probes = []model.RoutingProbeClaim{claim}
	c.Set("mowan_routing_probes_prepared", true)
	_, err = store.ReleaseProbe(claim, time.Time{})
	require.NoError(t, err)
	require.ErrorContains(t, BeginRoutingSend(c), "routing_probe_unavailable")
	require.Zero(t, p.Sent)
	require.Empty(t, p.Tried)
}

func TestRoutingManualSkipsObservedActiveCongestion(t *testing.T) {
	for _, scenario := range []string{"at_peak", "below_peak", "no_sample", "expired_sample", "other_key", "other_stage", "terminal_at_peak", "terminal_no_sample", "active_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			db, newContext := setupRoutingPlanTest(t)
			createChannelSelectAutoGroupsChannel(t, db, 3981, "vip", "routing-test")
			createChannelSelectAutoGroupsChannel(t, db, 3982, "default", "routing-test")
			model.InitChannelCache()
			c := newContext()
			c.Set("mowan_routing_async", true)
			ch, err := model.GetChannelById(3981, true)
			require.NoError(t, err)
			k, err := RoutingObservationKey(c, "routing-test", "vip", ch, "async")
			require.NoError(t, err)
			store := model.RoutingMetricsStore{DB: db}
			sampleKey := k
			if scenario == "other_key" {
				sampleKey.OperationKey = "other-operation"
			}
			stage := model.RoutingStageAttempt
			if scenario == "other_stage" || scenario == "terminal_at_peak" || scenario == "terminal_no_sample" {
				stage = model.RoutingStageTaskTerminal
			}
			for i := 0; i < 2; i++ {
				require.NoError(t, store.StartLease(model.RoutingMetricLease{RoutingMetricKey: sampleKey, Stage: model.RoutingStageAttempt, ChannelID: 3981, LeaseID: fmt.Sprintf("manual-active-%d", i)}, time.Time{}))
				if stage == model.RoutingStageTaskTerminal && scenario != "other_stage" {
					require.NoError(t, store.SaveTerminalSubmission(k, fmt.Sprintf("manual-terminal-%d", i), 3981, "accepted", "", time.Time{}))
				}
			}
			if scenario != "no_sample" && scenario != "terminal_no_sample" {
				at := time.Time{}
				if scenario == "expired_sample" {
					at = time.Now().Add(-6 * time.Minute)
				}
				_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: sampleKey, Stage: stage, EventID: "manual-capacity-success", Outcome: "success", Sent: true, SuccessActive: 2}, at)
				require.NoError(t, err)
			}
			if scenario == "below_peak" {
				require.NoError(t, store.FinishLease("manual-active-0"))
			}
			if scenario == "other_key" {
				// The target has active load but no capacity sample of its own.
				for i := 0; i < 2; i++ {
					require.NoError(t, store.StartLease(model.RoutingMetricLease{RoutingMetricKey: k, Stage: model.RoutingStageAttempt, ChannelID: 3981, LeaseID: fmt.Sprintf("target-active-%d", i)}, time.Time{}))
				}
			}
			if scenario == "active_unavailable" {
				require.NoError(t, db.Migrator().DropTable(&model.RoutingMetricLease{}))
			}
			selected, group, selectErr := SelectChannelForRequest(c, "routing-test", &RetryParam{})
			require.Nil(t, selectErr)
			require.NotNil(t, selected)
			want := "vip"
			if scenario == "at_peak" || scenario == "terminal_at_peak" {
				want = "default"
			}
			require.Equal(t, want, group)
			if scenario == "active_unavailable" {
				require.True(t, c.GetBool("mowan_routing_health_unknown"))
			}
		})
	}
}
