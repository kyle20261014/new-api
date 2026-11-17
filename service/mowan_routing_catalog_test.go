package service

import (
	"context"
	"io"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRoutingCatalogEffectiveRatioAndZero(t *testing.T) {
	original := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(original)) })
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"vip":0.25,"free":0}}`))
	entry := model.ModelPricingEntry{ModelName: "catalog-test", ModelPricingDescription: model.ModelPricingDescription{Effective: model.PricingValues{"ModelRatio": float64(2), "CompletionRatio": float64(3)}}}
	variant := CatalogVariant{Key: model.RoutingMetricKey{OperationKey: "chat"}}
	paid := catalogQuote(entry, variant, GetUserGroupRatio("default", "vip"), CatalogRequest{})
	require.Equal(t, "available", paid.Comparison)
	require.InDelta(t, 2*1e6/common.QuotaPerUnit*0.25, *paid.Prices[0].Amount, 1e-12)
	require.InDelta(t, *paid.Prices[0].Amount*3, *paid.Prices[1].Amount, 1e-12)
	require.Nil(t, paid.EstimatedTotal, "input unit price must never become total cost")
	free := catalogQuote(entry, variant, GetUserGroupRatio("default", "free"), CatalogRequest{})
	require.NotNil(t, free.Prices[0].Amount)
	require.Zero(t, *free.Prices[0].Amount)
	require.Zero(t, free.Cost)
	encoded, err := common.Marshal(free)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"amount":0`)
	require.Equal(t, "unavailable", catalogQuote(entry, variant, math.NaN(), CatalogRequest{}).Comparison)
}

func TestRoutingCatalogDifferentUnitsUnknownAndMixedProvider(t *testing.T) {
	token := model.ModelPricingEntry{ModelName: "catalog-test", ModelPricingDescription: model.ModelPricingDescription{Effective: model.PricingValues{"ModelRatio": float64(1), "CompletionRatio": float64(2)}}}
	fixed := model.ModelPricingEntry{ModelName: "catalog-test", ModelPricingDescription: model.ModelPricingDescription{Effective: model.PricingValues{"ModelPrice": float64(0.05)}}}
	v := CatalogVariant{Key: model.RoutingMetricKey{OperationKey: "chat"}}
	a := catalogQuote(token, v, 1, CatalogRequest{})
	b := catalogQuote(fixed, v, 1, CatalogRequest{})
	items := []CatalogCandidate{{Model: "catalog-test", Quote: a}, {Model: "catalog-test", Quote: b}}
	catalogComparable(items)
	require.Equal(t, "unavailable", items[0].Quote.Comparison)
	require.Equal(t, "unavailable", items[1].Quote.Comparison)
	require.Nil(t, items[0].Quote.EstimatedTotal)
	candidate := CatalogCandidate{Metrics: catalogUnknownMetrics("unknown"), Variants: []CatalogVariant{{ProviderKey: "builtin:1:v1", Quote: a, CatalogMetrics: CatalogMetrics{ResponseMS1h: catalogNumber(10)}}, {ProviderKey: "plugin:test:7", Quote: b, CatalogMetrics: CatalogMetrics{ResponseMS1h: catalogNumber(1000)}}}}
	catalogFoldCandidate(&candidate)
	require.Equal(t, "unavailable", candidate.Quote.Comparison)
	require.Nil(t, candidate.Quote.EstimatedTotal)
	require.Nil(t, candidate.Metrics.ResponseMS1h, "different providers cannot share a response mean")
	require.Nil(t, candidate.Quote.Prices[0].Amount)
	require.NotNil(t, candidate.Quote.Prices[0].Min)
	unknown := catalogQuote(model.ModelPricingEntry{}, v, 1, CatalogRequest{})
	require.Equal(t, "unavailable", unknown.Comparison)
	require.Empty(t, unknown.Prices)
	require.Nil(t, unknown.EstimatedTotal)
}

func TestRoutingCatalogOwnerPermissionsPaginationAndFailClosed(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Option{}, &model.Model{}, &model.Vendor{}))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "catalog-owner", AffCode: "catalog-owner", Group: "default", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 2, Username: "catalog-other", AffCode: "catalog-other", Group: "default", Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 1, UserId: 1, Key: "catalog-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "gpt-4o"}).Error)
	createChannelSelectAutoGroupsChannel(t, db, 7101, "all", "gpt-4o")
	createChannelSelectAutoGroupsChannel(t, db, 7102, "revoked", "gpt-4o")
	createChannelSelectAutoGroupsChannel(t, db, 7103, "default", "gpt-4o-mini")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 7102).Update("status", common.ChannelStatusManuallyDisabled).Error)
	request := CatalogRequest{UserID: 1, TokenID: 1, Page: 1, PageSize: 1, Category: "text"}
	result, err := RoutingCatalog(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 2, result.Total, "all expands only into current authorized business groups")
	require.Len(t, result.Items, 1)
	require.Equal(t, "default", result.Items[0].Group)
	require.Equal(t, "gpt-4o", result.Items[0].Model)
	require.Equal(t, []int{7101}, result.Items[0].ChannelIDs)
	require.Equal(t, "unknown", result.Items[0].Health)
	require.Nil(t, result.Items[0].Metrics.Success24h)
	encoded, err := common.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "channel")
	require.NotContains(t, string(encoded), "catalog-key")
	request.Page = 2
	result, err = RoutingCatalog(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "vip", result.Items[0].Group)
	request.Page = math.MaxInt
	result, err = RoutingCatalog(context.Background(), request)
	require.NoError(t, err)
	require.Empty(t, result.Items)
	request.UserID = 2
	_, err = RoutingCatalog(context.Background(), request)
	require.Error(t, err, "caller-owned token snapshots cannot bypass owner query")
	request.UserID = 1
	request.PageSize = 101
	_, err = RoutingCatalog(context.Background(), request)
	require.Error(t, err)
	request.PageSize = 1
	request.Page = 1
	request.Search = "mini"
	result, err = RoutingCatalog(context.Background(), request)
	require.NoError(t, err)
	require.Zero(t, result.Total)
	request.Search = ""
	request.Model = "gpt-4o"
	preview, err := RoutingCatalogPreview(context.Background(), CatalogPreviewRequest{CatalogRequest: request, Routing: smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: "custom", Models: map[string]smartrouting.ModelPolicy{"gpt-4o": {Strategy: "manual", Groups: []string{"vip", "default"}}}}})
	require.NoError(t, err)
	require.Equal(t, "vip", preview.SelectedGroup)
	require.Len(t, preview.Items, 2)
	require.Equal(t, "smart-routing-v1", preview.AlgorithmVersion)
	require.NoError(t, db.Migrator().DropTable(&model.Ability{}))
	request.Search = ""
	_, err = RoutingCatalog(context.Background(), request)
	require.ErrorContains(t, err, "abilities")
}

func TestRoutingCatalogEstimateUsesOutputAndUnknownUsage(t *testing.T) {
	n := uint(32)
	request := CatalogRequest{Model: "gpt-4o", Request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}, MaxTokens: &n}}
	variant := CatalogVariant{ProviderKey: "builtin:1:v1", Key: model.RoutingMetricKey{OperationKey: "chat"}}
	entry := model.ModelPricingEntry{ModelName: "gpt-4o", ModelPricingDescription: model.ModelPricingDescription{Effective: model.PricingValues{"billing_setting.billing_mode": billing_setting.BillingModeTieredExpr, "billing_setting.billing_expr": `tier("text", p * 2 + c * 8)`}}}
	quote := catalogQuote(entry, variant, 0.25, request)
	require.Equal(t, "available", quote.Comparison)
	require.NotNil(t, quote.EstimatedTotal)
	params, _, ok := catalogEstimate(request)
	require.True(t, ok)
	require.InDelta(t, (params.P*2+32*8)*0.25/1e6, *quote.EstimatedTotal, 1e-12)
	entry.Effective["billing_setting.billing_expr"] = `tier("task", u("seconds") * 2)`
	quote = catalogQuote(entry, variant, 1, request)
	require.Equal(t, "unavailable", quote.Comparison)
	require.Nil(t, quote.EstimatedTotal)
	entry.Effective["billing_setting.billing_expr"] = `tier("text", p * 2 + c * 8)`
	variant.PluginKey = "missing-provider"
	quote = catalogQuote(entry, variant, 1, request)
	require.Equal(t, "unavailable", quote.Comparison, "task providers never fall back to ordinary token pricing")
}

func TestRoutingRankCandidatesRequestEstimateMatchesPreview(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Option{}, &model.Model{}, &model.Vendor{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"a-expensive":"A","b-cheap":"B"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"a-expensive":2,"b-cheap":1}`))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "catalog-rank", AffCode: "catalog-rank", Group: "default", Status: common.UserStatusEnabled}).Error)
	createChannelSelectAutoGroupsChannel(t, db, 7201, "a-expensive", "gpt-4o")
	createChannelSelectAutoGroupsChannel(t, db, 7202, "b-cheap", "gpt-4o")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 7202).Update("type", constant.ChannelTypeAzure).Error)
	require.NoError(t, db.Create(&model.Option{Key: "billing_setting.billing_mode", Value: `{"gpt-4o":"tiered_expr"}`}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "billing_setting.billing_expr", Value: `{"gpt-4o":"tier(\"text\", p * 2 + c * 8)"}`}).Error)

	for _, tc := range []struct {
		name, fields string
		comparable   bool
		invalid      bool
	}{
		{"bounded_text", `"max_tokens":32`, true, false},
		{"completion_limit", `"max_completion_tokens":16`, true, false},
		{"explicit_zero", `"max_tokens":32,"max_completion_tokens":0`, true, false},
		{"explicit_legacy_zero", `"max_tokens":0`, true, false},
		{"missing_limit", `"temperature":0`, true, false},
		{"multimodal", `"max_tokens":32,"modalities":["audio"]`, false, false},
		{"tools", `"max_tokens":32,"tools":[{"type":"function","function":{"name":"search"}}]`, false, false},
		{"oversized_limit", `"max_tokens":1073741824`, false, true},
		{"oversized_minimum", `"max_tokens":32,"min_tokens":1073741824`, false, true},
		{"multiple_outputs", `"max_tokens":32,"n":2`, false, false},
		{"invalid_count", `"max_tokens":32,"n":0`, false, true},
		{"unknown_usage_expression", `"max_tokens":32`, false, false},
		{"legacy_token_price", `"max_tokens":32`, true, false},
		{"legacy_request_price", `"max_tokens":32`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "legacy_") {
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_mode").Update("value", `{"gpt-4o":"ratio"}`).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_mode").Update("value", `{"gpt-4o":"tiered_expr"}`).Error)
				})
				if tc.name == "legacy_request_price" {
					require.NoError(t, db.Create(&model.Option{Key: "ModelPrice", Value: `{"gpt-4o":0.05}`}).Error)
					t.Cleanup(func() { require.NoError(t, db.Where("key = ?", "ModelPrice").Delete(&model.Option{}).Error) })
				}
			}
			if tc.name == "unknown_usage_expression" {
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_expr").Update("value", `{"gpt-4o":"tier(\"text\", p * 2 + c * 8 + cr)"}`).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_expr").Update("value", `{"gpt-4o":"tier(\"text\", p * 2 + c * 8)"}`).Error)
				})
			}
			if tc.name == "missing_limit" {
				require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_expr").Update("value", `{"gpt-4o":"tier(\"text\", c * 8)"}`).Error)
				t.Cleanup(func() {
					require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "billing_setting.billing_expr").Update("value", `{"gpt-4o":"tier(\"text\", p * 2 + c * 8)"}`).Error)
				})
			}
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],` + tc.fields + `}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyUserId, 1)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			// Distribution has cached the JSON before the relay validator runs.
			var parsed dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalBodyReusable(c, &parsed))
			c.Request.Body = io.NopCloser(strings.NewReader("must use cached body"))
			actual, err := RoutingRankCandidates(c, "gpt-4o", []string{"a-expensive", "b-cheap"})
			require.NoError(t, err)
			require.Len(t, actual, 2)
			for _, candidate := range actual {
				require.Equal(t, tc.comparable, candidate.Comparable)
			}
			preview, err := RoutingCatalogPreview(context.Background(), CatalogPreviewRequest{CatalogRequest: CatalogRequest{UserID: 1, Model: "gpt-4o", Request: &parsed}, Routing: smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: "price"}})
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			ranked, err := smartrouting.Rank(smartrouting.RankRequest{Strategy: "price", ClientModel: "gpt-4o", RequestID: "catalog-regression", Now: time.Now(), Candidates: actual})
			require.NoError(t, err)
			require.Equal(t, preview.SelectedGroup, ranked.Candidates[0].Group)
			if tc.comparable {
				require.Equal(t, "b-cheap", preview.SelectedGroup)
				for _, item := range preview.Items {
					require.NotNil(t, item.Quote.EstimatedTotal)
					for _, candidate := range actual {
						if candidate.Group == item.Group {
							require.Equal(t, *item.Quote.EstimatedTotal, candidate.Cost)
						}
					}
				}
				if tc.name == "explicit_zero" || tc.name == "explicit_legacy_zero" {
					require.Less(t, actual[1].Cost, 32*8/1e6, "explicit zero must override max_tokens")
				}
				if tc.name == "missing_limit" {
					require.InDelta(t, 1024*8/1e6, actual[1].Cost, 1e-12, "unconfigured model uses the 1024-token estimate")
				}
			} else {
				for _, item := range preview.Items {
					require.Nil(t, item.Quote.EstimatedTotal)
				}
			}
			var again dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalBodyReusable(c, &again))
			require.Equal(t, parsed, again, "ranking must preserve cached request and client model")
		})
	}
	for _, body := range []string{
		`{"model":"other","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`,
		`{"model":"gpt-4o","max_tokens":32}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"unknown","text":"hello"}]}],"max_tokens":32}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"unknown","text":"ignored"}]}],"max_tokens":32}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://invalid.test/image"}}]}],"max_tokens":32}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"max_tokens":-1}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"max_tokens":18446744073686646784}`,
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"max_tokens":32`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		common.SetContextKey(c, constant.ContextKeyUserId, 1)
		t.Cleanup(func() { common.CleanupBodyStorage(c) })
		actual, err := RoutingRankCandidates(c, "gpt-4o", []string{"a-expensive", "b-cheap"})
		require.NoError(t, err)
		require.Len(t, actual, 2)
		for _, candidate := range actual {
			require.False(t, candidate.Comparable, body)
		}
	}
	for _, path := range []string{"/pg/chat/completions", "/v1/messages", "/v1/responses", "/v1/videos"} {
		t.Run(path, func(t *testing.T) {
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"max_tokens":32}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", path, strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyUserId, 1)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			actual, err := RoutingRankCandidates(c, "gpt-4o", []string{"a-expensive", "b-cheap"})
			require.NoError(t, err)
			var parsed dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(body), &parsed))
			preview, err := RoutingCatalogPreview(context.Background(), CatalogPreviewRequest{CatalogRequest: CatalogRequest{UserID: 1, Model: "gpt-4o", Request: &parsed, RequestPath: path}, Routing: smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: "price"}})
			require.NoError(t, err)
			for _, candidate := range actual {
				require.Equal(t, path == "/pg/chat/completions", candidate.Comparable)
			}
			for _, item := range preview.Items {
				require.Equal(t, path == "/pg/chat/completions", item.Quote.EstimatedTotal != nil)
			}
		})
	}
}

func TestRoutingCatalogRealFoldFiltersChannelAndFullKey(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	require.NoError(t, model.MigrateRoutingMetrics(db))
	now := time.Now().Truncate(time.Second)
	key := model.RoutingMetricKey{ClientModel: "gpt-4o", BusinessGroup: "default", ProtocolClass: "sync", ProviderKey: "builtin:1:v1", OperationKey: "chat"}
	store := model.RoutingMetricsStore{DB: db}
	for i := 0; i < 3; i++ {
		k := key
		if i == 2 {
			k.OperationKey = "responses"
		}
		channel := 1
		if i == 1 {
			channel = 2
		}
		_, err := store.UpsertSnapshot(model.RoutingMetricBucket{RoutingMetricKey: k, Stage: model.RoutingStageAttempt, NodeBootID: "catalog-test", ChannelID: channel, Minute: now.Unix() / 60 * 60, Revision: 1, Eligible: 20, Success: 18, Failure: 2, SpeedSamples: 6, SpeedTotalMS: 600 + float64(i)*6000, LastObservationAt: now.Unix()}, now)
		require.NoError(t, err)
	}
	variant := CatalogVariant{Key: key, ChannelIDs: []int{1}, CatalogMetrics: catalogUnknownMetrics("sync")}
	var healthBefore int64
	require.NoError(t, db.Model(&model.RoutingHealth{}).Count(&healthBefore).Error)
	catalogFoldVariant(db, &variant, now)
	require.Equal(t, int64(20), variant.Samples24h)
	require.InDelta(t, 0.9, *variant.Success24h, 1e-12)
	require.InDelta(t, 100, *variant.ResponseMS1h, 1e-12)
	require.Equal(t, "unknown", variant.Health, "bucket totals do not fabricate instantaneous health")
	var healthAfter int64
	require.NoError(t, db.Model(&model.RoutingHealth{}).Count(&healthAfter).Error)
	require.Equal(t, healthBefore, healthAfter, "catalog reads never seed or mutate breaker rows")
}

func TestRoutingCatalogMetricFailureKeepsUnknown(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	variant := CatalogVariant{Key: model.RoutingMetricKey{ClientModel: "gpt-4o", BusinessGroup: "default", ProtocolClass: "sync", ProviderKey: "builtin:1:v1", OperationKey: "chat"}, ChannelIDs: []int{1}, CatalogMetrics: catalogUnknownMetrics("sync")}
	catalogFoldVariant(db, &variant, time.Now())
	require.Equal(t, "unknown", variant.Health)
	require.Nil(t, variant.ResponseMS1h)
	require.Nil(t, variant.Success24h)
	require.Nil(t, variant.UpdatedAt)
	require.Len(t, variant.Observation, 1)
	require.False(t, variant.Observation[0].ActiveKnown)
}

func TestRoutingCatalogOperationAndPreviewValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	key, err := RoutingObservationKey(c, "gpt-4o", "default", &model.Channel{Type: constant.ChannelTypeOpenAI}, "stream")
	require.NoError(t, err)
	require.Equal(t, "chat", key.OperationKey)
	require.Equal(t, "stream", key.ProtocolClass)
	require.Equal(t, "builtin:1:v1", key.ProviderKey)
	_, err = RoutingOperationKey("/user-controlled-action")
	require.Error(t, err)
	n := uint(math.MaxInt32)
	request := CatalogRequest{UserID: 1, Model: "gpt-4o", Request: &dto.GeneralOpenAIRequest{MaxTokens: &n}}
	require.Error(t, catalogValidate(&request))
	request = CatalogRequest{UserID: 1, Model: "gpt-4o", Request: &dto.GeneralOpenAIRequest{Model: "other"}}
	require.Error(t, catalogValidate(&request))
}

func TestRoutingOperationMidjourneyRegisteredCreates(t *testing.T) {
	for _, tc := range []struct{ path, operation string }{
		{"/submit/action", "mj.action"},
		{"/submit/shorten", "mj.shorten"},
		{"/submit/modal", "mj.modal"},
		{"/submit/imagine", "mj.imagine"},
		{"/submit/change", "mj.change"},
		{"/submit/simple-change", "mj.change"},
		{"/submit/describe", "mj.describe"},
		{"/submit/blend", "mj.blend"},
		{"/submit/edits", "mj.edits"},
		{"/submit/video", "mj.video"},
		{"/insight-face/swap", "mj.swap_face"},
		{"/submit/upload-discord-images", "mj.upload"},
	} {
		for _, prefix := range []string{"", "/mj-fast", "/:mode"} {
			path := prefix + "/mj" + tc.path
			t.Run(path, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, nil)
				key, err := RoutingObservationKey(c, "mj_imagine", "default", &model.Channel{Type: constant.ChannelTypeMidjourney}, "async")
				require.NoError(t, err)
				require.Equal(t, tc.operation, key.OperationKey)
			})
		}
	}
	for _, path := range []string{"/mj/submit/custom-action", "/a/b/mj/submit/imagine", "/other/mj/submit/custom-action"} {
		_, err := RoutingOperationKey(path)
		require.Error(t, err)
	}
}
