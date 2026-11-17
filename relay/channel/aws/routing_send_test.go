package aws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAwsRoutingTest(t *testing.T) (*gorm.DB, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/routing.db"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TokenRouting{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	originalDB, originalRetries, originalMemory := model.DB, common.RetryTimes, common.MemoryCacheEnabled
	model.DB, common.RetryTimes, common.MemoryCacheEnabled = db, 1, false
	t.Cleanup(func() {
		require.NoError(t, service.FlushRoutingMetrics(context.Background()))
		model.DB, common.RetryTimes, common.MemoryCacheEnabled = originalDB, originalRetries, originalMemory
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "aws-routing", Status: common.UserStatusEnabled, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 102, UserId: 101, Key: "test-token", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	row := &model.TokenRouting{TokenID: 102, SchemaVersion: 1, Mode: "smart", Version: 1, PolicyJSON: fmt.Sprintf(`{"schema_version":1,"mode":"smart","strategy":"custom","models":{%q:{"strategy":"manual","groups":["default"]}}}`, awsTestModel)}
	require.NoError(t, db.Create(row).Error)
	key := "access-key|secret-key|us-east-1"
	require.NoError(t, db.Create(&model.Channel{Id: 103, Type: constant.ChannelTypeAws, Key: key, Name: "aws-test", Status: common.ChannelStatusEnabled, Models: awsTestModel, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: awsTestModel, ChannelId: 103, Enabled: true}).Error)
	c := newAwsTestContext(httptest.NewRecorder(), context.Background())
	c.Set("id", 101)
	c.Set("token_id", 102)
	c.Set("channel_id", 103)
	c.Set(smartrouting.ContextKey, row)
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	require.NoError(t, service.AuthorizeRoutingConnection(c, awsTestModel, "default", 103))
	t.Cleanup(func() { service.FinishRoutingSend(c, nil, false, 0) })
	info := newAwsTestRelayInfo()
	info.IsStream, info.ApiKey = false, key
	return db, c, info
}

func awsLocalEndpoint(a *Adaptor, url string) {
	options := a.AwsClient.Options()
	options.BaseEndpoint = aws.String(url)
	a.AwsClient = bedrockruntime.New(options)
}

func TestAwsSmartSDKSendIsCountedAtTransport(t *testing.T) {
	_, c, info := setupAwsRoutingTest(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	a := &Adaptor{}
	_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
	require.NoError(t, err)
	require.Zero(t, service.RoutingPlanFor(c).Sent, "construction is not a send")
	awsLocalEndpoint(a, server.URL)
	_, apiErr := a.DoResponse(c, nil, info)
	require.Nil(t, apiErr)
	require.EqualValues(t, 1, calls.Load())
	require.Equal(t, 1, service.RoutingPlanFor(c).Sent, "SDK invoke must consume the shared send budget")
	require.True(t, service.RoutingPlanFor(c).Tried[103])
	_, apiErr = a.DoResponse(c, nil, info)
	require.NotNil(t, apiErr)
	require.True(t, types.IsSkipRetryError(apiErr))
	require.EqualValues(t, 1, calls.Load(), "repeated invocation cannot reuse a transport claim")
}

func TestAwsSmartSDKRechecksAuthorityBeforeSend(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*gorm.DB) error
		want   string
	}{
		{"token disabled", func(db *gorm.DB) error {
			return db.Model(&model.Token{}).Where("id = ?", 102).Update("status", common.TokenStatusDisabled).Error
		}, "routing_identity_unavailable"},
		{"account disabled", func(db *gorm.DB) error {
			return db.Model(&model.User{}).Where("id = ?", 101).Update("status", common.UserStatusDisabled).Error
		}, "routing_identity_unavailable"},
		{"ability revoked", func(db *gorm.DB) error {
			return db.Model(&model.Ability{}).Where("channel_id = ?", 103).Update("enabled", false).Error
		}, "routing_ability_unavailable"},
		{"key rotated", func(db *gorm.DB) error {
			return db.Model(&model.Channel{}).Where("id = ?", 103).Update("key", "new-access|new-secret|us-east-1").Error
		}, "routing_key_changed"},
		{"policy changed", func(db *gorm.DB) error {
			return db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Update("version", 2).Error
		}, "routing_configuration_changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, c, info := setupAwsRoutingTest(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			a := &Adaptor{}
			_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
			require.NoError(t, err)
			awsLocalEndpoint(a, server.URL)
			require.NoError(t, test.change(db))
			_, apiErr := a.DoResponse(c, nil, info)
			require.NotNil(t, apiErr)
			require.Contains(t, apiErr.Error(), test.want)
			require.True(t, types.IsSkipRetryError(apiErr), "SDK wrappers must preserve the shared gate error")
			require.Zero(t, calls.Load())
			require.Zero(t, service.RoutingPlanFor(c).Sent)
			require.Empty(t, service.RoutingPlanFor(c).Tried)
			var leases int64
			require.NoError(t, db.Model(&model.RoutingMetricLease{}).Count(&leases).Error)
			require.Zero(t, leases)
		})
	}
}

func TestAwsSmartSDKLocalFailuresDoNotSend(t *testing.T) {
	for _, failure := range []string{"malformed body", "SDK missing model", "SDK missing credentials"} {
		t.Run(failure, func(t *testing.T) {
			_, c, info := setupAwsRoutingTest(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer server.Close()
			a := &Adaptor{}
			body := `{"messages":[],"max_tokens":8}`
			if failure == "malformed body" {
				body = `{`
			}
			_, err := a.DoRequest(c, info, strings.NewReader(body))
			if failure == "malformed body" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				awsLocalEndpoint(a, server.URL)
				if failure == "SDK missing model" {
					a.AwsReq.(*bedrockruntime.InvokeModelInput).ModelId = nil
				} else {
					options := a.AwsClient.Options()
					options.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
						return aws.Credentials{}, errors.New("test credentials unavailable")
					})
					a.AwsClient = bedrockruntime.New(options)
				}
				_, apiErr := a.DoResponse(c, nil, info)
				require.NotNil(t, apiErr)
			}
			require.Zero(t, calls.Load())
			require.Zero(t, service.RoutingPlanFor(c).Sent)
			require.Empty(t, service.RoutingPlanFor(c).Tried)
		})
	}
}

func TestAwsSDKRetryAndRedirectBoundaries(t *testing.T) {
	for _, smart := range []bool{true, false} {
		t.Run(fmt.Sprintf("smart=%t", smart), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, `{"message":"temporary failure"}`)
			}))
			defer server.Close()
			c := newAwsTestContext(httptest.NewRecorder(), context.Background())
			info := newAwsTestRelayInfo()
			info.IsStream, info.ApiKey = false, "access-key|secret-key|us-east-1"
			if smart {
				_, c, info = setupAwsRoutingTest(t)
			}
			a := &Adaptor{}
			_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
			require.NoError(t, err)
			options := a.AwsClient.Options()
			want := 3
			if smart {
				want = 1
			}
			require.Equal(t, want, options.Retryer.MaxAttempts())
			// Retain the production retry policy; only remove backoff from the test.
			options.Retryer = retry.AddWithMaxBackoffDelay(options.Retryer, time.Millisecond)
			options.BaseEndpoint = aws.String(server.URL)
			a.AwsClient = bedrockruntime.New(options)
			_, apiErr := a.DoResponse(c, nil, info)
			require.NotNil(t, apiErr)
			require.EqualValues(t, want, calls.Load())
			if smart {
				require.Equal(t, 1, service.RoutingPlanFor(c).Sent)
			}
		})
	}
	t.Run("smart redirects", func(t *testing.T) {
		_, c, info := setupAwsRoutingTest(t)
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			http.Redirect(w, r, "/second-send", http.StatusTemporaryRedirect)
		}))
		defer server.Close()
		a := &Adaptor{}
		_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
		require.NoError(t, err)
		awsLocalEndpoint(a, server.URL)
		_, apiErr := a.DoResponse(c, nil, info)
		require.NotNil(t, apiErr)
		require.Equal(t, http.StatusTemporaryRedirect, apiErr.StatusCode)
		require.EqualValues(t, 1, calls.Load())
		require.Equal(t, 1, service.RoutingPlanFor(c).Sent)
	})
}

func TestAwsSmartSDKGlobalBudgetAndPhysicalDedup(t *testing.T) {
	db, c, info := setupAwsRoutingTest(t)
	groups, ratios := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(ratios))
	})
	require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", 102).Update("policy_json", fmt.Sprintf(`{"schema_version":1,"mode":"smart","strategy":"custom","models":{%q:{"strategy":"manual","groups":["default","vip"]}}}`, awsTestModel)).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: awsTestModel, ChannelId: 103, Enabled: true}).Error)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	a := &Adaptor{}
	_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
	require.NoError(t, err)
	awsLocalEndpoint(a, server.URL)
	_, apiErr := a.DoResponse(c, nil, info)
	require.NotNil(t, apiErr)
	service.FinishRoutingSend(c, apiErr, false, 0)
	plan := service.RoutingPlanFor(c)
	require.Equal(t, 1, plan.Sent)
	_, _, selectErr := service.SelectChannelForRequest(c, awsTestModel, &service.RetryParam{Ctx: c, ModelName: awsTestModel})
	require.NotNil(t, selectErr)
	require.Contains(t, selectErr.Message, "routing_channels_exhausted", "shared physical channel cannot be reused through another group")
	require.EqualValues(t, 1, calls.Load())
	// The next physical channel still shares the original request's budget.
	require.NoError(t, db.Create(&model.Channel{Id: 104, Type: constant.ChannelTypeAws, Key: info.ApiKey, Status: common.ChannelStatusEnabled, Models: awsTestModel, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: awsTestModel, ChannelId: 104, Enabled: true}).Error)
	ch, _, selectErr := service.SelectChannelForRequest(c, awsTestModel, &service.RetryParam{Ctx: c, ModelName: awsTestModel})
	require.Nil(t, selectErr)
	require.Equal(t, 104, ch.Id)
	c.Set("channel_id", ch.Id)
	_, apiErr = a.DoResponse(c, nil, info)
	require.NotNil(t, apiErr)
	require.Equal(t, 2, plan.Sent)
	service.FinishRoutingSend(c, apiErr, false, 0)
	_, _, selectErr = service.SelectChannelForRequest(c, awsTestModel, &service.RetryParam{Ctx: c, ModelName: awsTestModel})
	require.NotNil(t, selectErr)
	require.Equal(t, "attempt_budget_exhausted", selectErr.Message)
	_, apiErr = a.DoResponse(c, nil, info)
	require.NotNil(t, apiErr)
	require.Contains(t, apiErr.Error(), "attempt_budget_exhausted")
	require.EqualValues(t, 2, calls.Load())
}

func TestAwsSmartSDKStreamAndNovaUseSharedAttempt(t *testing.T) {
	for _, variant := range []string{"control only stream", "content stream", "nova"} {
		t.Run(variant, func(t *testing.T) {
			db, c, info := setupAwsRoutingTest(t)
			stream := variant != "nova"
			info.IsStream = stream
			common.SetContextKey(c, constant.ContextKeyIsStream, stream)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"output":{"message":{"content":[{"text":"nova hello"}]}},"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
				w.Header().Set("X-Amzn-Bedrock-Content-Type", "application/json")
				events := []string{`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`}
				if variant == "content stream" {
					events = append(events, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)
				}
				events = append(events, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, `{"type":"message_stop"}`)
				for _, event := range events {
					if err := writeAwsStreamEvent(w, event); err != nil {
						return
					}
				}
			}))
			defer server.Close()
			a := &Adaptor{IsNova: !stream}
			if !stream {
				info.UpstreamModelName = "amazon.nova-pro-v1:0"
			}
			_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[],"max_tokens":8}`))
			require.NoError(t, err)
			awsLocalEndpoint(a, server.URL)
			usage, apiErr := a.DoResponse(c, nil, info)
			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			require.Equal(t, 1, service.RoutingPlanFor(c).Sent)
			require.EqualValues(t, 1, calls.Load())
			service.FinishRoutingSend(c, nil, true, 0)
			require.NoError(t, service.FlushRoutingMetrics(context.Background()))
			var buckets []model.RoutingMetricBucket
			require.NoError(t, db.Find(&buckets).Error)
			require.Len(t, buckets, 1)
			require.EqualValues(t, 1, buckets[0].Sent)
			wantSpeed := int64(1)
			if variant == "control only stream" {
				wantSpeed = 0
			}
			require.Equal(t, wantSpeed, buckets[0].SpeedSamples, "control frames must not invent TTFT")
			require.Equal(t, "response_started", service.RoutingRetryBarrier(c))
			info.SendResponseCount = 0
			require.Equal(t, "response_started", service.RoutingRetryBarrier(c), "output barrier survives attempt-local resets")
		})
	}
}
