package channel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRequestReturnsUpstreamRedirectWithoutFollowing(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	sharedClient := service.GetHttpClient()
	require.NotNil(t, sharedClient)
	require.NotNil(t, sharedClient.CheckRedirect)
	originalRedirectPolicy := reflect.ValueOf(sharedClient.CheckRedirect).Pointer()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	const responseBody = "redirect response"
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, statusCode := range tests {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			targetRequests.Store(0)
			var sourceRequests atomic.Int32
			type sourceResult struct {
				body []byte
				err  error
			}
			sourceResultCh := make(chan sourceResult, 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				body, err := io.ReadAll(r.Body)
				sourceResultCh <- sourceResult{body: body, err: err}
				w.Header().Set("Location", target.URL+"/redirect-target")
				w.WriteHeader(statusCode)
				_, _ = io.WriteString(w, responseBody)
			}))
			defer source.Close()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)

			req, err := http.NewRequest(http.MethodPost, source.URL, bytes.NewReader([]byte("request body")))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

			resp, err := doRequest(ctx, req, info)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			gotSource := <-sourceResultCh
			require.NoError(t, gotSource.err)

			assert.Equal(t, statusCode, resp.StatusCode)
			assert.Equal(t, target.URL+"/redirect-target", resp.Header.Get("Location"))
			assert.Equal(t, responseBody, string(body))
			assert.Equal(t, []byte("request body"), gotSource.body)
			assert.EqualValues(t, 1, sourceRequests.Load())
			assert.Zero(t, targetRequests.Load())
		})
	}

	assert.Equal(t, originalRedirectPolicy, reflect.ValueOf(sharedClient.CheckRedirect).Pointer(), "the cached client must not be mutated")
}

func TestSmartRoutingTransportCountsOnlyActualSendsAndRechecksAuthority(t *testing.T) {
	previousDB, previousMemory, previousRetry := model.DB, common.MemoryCacheEnabled, common.RetryTimes
	groups, ratios := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.TokenRouting{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	model.DB, common.MemoryCacheEnabled, common.RetryTimes = db, false, 1
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled, common.RetryTimes = previousDB, previousMemory, previousRetry
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(groups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(ratios))
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "routing-transport", Status: common.UserStatusEnabled, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 2, UserId: 1, Key: "routing-transport", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}).Error)
	row := &model.TokenRouting{TokenID: 2, Mode: "fixed", SchemaVersion: 1, Version: 1, PolicyJSON: `{"schema_version":1,"mode":"fixed","fixed_group":"default"}`}
	require.NoError(t, db.Create(row).Error)
	for _, id := range []int{1, 2} {
		require.NoError(t, db.Create(&model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprint(id), Status: common.ChannelStatusEnabled, Group: "default", Models: "routing-model"}).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "routing-model", ChannelId: id, Enabled: true, Weight: 100}).Error)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(requestCtx)
	c.Set("id", 1)
	c.Set("token_id", 2)
	c.Set(smartrouting.ContextKey, row)
	selectChannel := func() {
		ch, _, e := service.SelectChannelForRequest(c, "routing-model", &service.RetryParam{Ctx: c})
		require.Nil(t, e)
		c.Set("channel_id", ch.Id)
		common.SetContextKey(c, constant.ContextKeyChannelKey, ch.Key)
	}
	selectChannel()
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	service.InitHttpClient()
	send := func() error {
		req, e := http.NewRequestWithContext(requestCtx, http.MethodPost, server.URL, bytes.NewReader(nil))
		require.NoError(t, e)
		resp, e := DoRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
		if resp != nil {
			_ = resp.Body.Close()
		}
		return e
	}
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 2).Update("status", common.TokenStatusDisabled).Error)
	require.Error(t, send())
	require.Zero(t, sends.Load())
	require.Zero(t, service.RoutingPlanFor(c).Sent)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 2).Update("status", common.TokenStatusEnabled).Error)
	for attempt := 1; attempt <= 2; attempt++ {
		require.NoError(t, send())
		require.EqualValues(t, attempt, sends.Load())
		service.FinishRoutingSend(c, nil, true, 0)
		require.Error(t, send())
		require.EqualValues(t, attempt, sends.Load())
		if attempt < 2 {
			selectChannel()
		}
	}
	_, _, selectionErr := service.SelectChannelForRequest(c, "routing-model", &service.RetryParam{Ctx: c})
	require.NotNil(t, selectionErr)
	require.Equal(t, "attempt_budget_exhausted", selectionErr.Message)
}
