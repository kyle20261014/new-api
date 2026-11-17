package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyLegacyOriginSubmissionUsesActualChannel(t *testing.T) {
	db := setupRelayChannelDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Midjourney{}, &model.Log{}))
	require.NoError(t, model.MigrateRoutingFunding(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	previousLogDB, previousRedis := model.LOG_DB, common.RedisEnabled
	model.LOG_DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.LOG_DB, common.RedisEnabled = previousLogDB, previousRedis })
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1,"description":"accepted","result":"origin-child"}`))
	}))
	t.Cleanup(server.Close)
	origin := &model.Channel{Name: "origin", Type: constant.ChannelTypeMidjourney, Key: "same-key", BaseURL: &server.URL, Status: common.ChannelStatusEnabled}
	initial := &model.Channel{Name: "initial", Type: constant.ChannelTypeMidjourney, Key: "same-key", BaseURL: &server.URL, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(origin).Error)
	require.NoError(t, db.Create(initial).Error)
	require.NoError(t, db.Create(&model.User{Id: 7, Username: "mj-origin", Quota: 1000000, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 7, UserId: 7, Key: "mj-token", RemainQuota: 1000000, Status: common.TokenStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Midjourney{UserId: 7, ChannelId: origin.Id, MjId: "origin-task", Prompt: "origin prompt", Status: "SUCCESS", Progress: "100%"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/change", strings.NewReader(`{"taskId":"origin-task","action":"UPSCALE","index":1}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", 7)
	c.Set("channel_id", initial.Id)
	c.Set("channel_type", constant.ChannelTypeMidjourney)
	c.Set("channel_key", "same-key")
	c.Set("base_url", server.URL)
	info := &relaycommon.RelayInfo{UserId: 7, TokenId: 7, TokenKey: "mj-token", OriginModelName: "mj_upscale", UsingGroup: "default", UserGroup: "default", StartTime: time.Now(), RelayMode: relayconstant.RelayModeMidjourneyChange}
	require.Nil(t, RelayMidjourneySubmit(c, info))
	require.Equal(t, origin.Id, info.ChannelId)
	stored := model.GetByMJId(7, "origin-child")
	require.NotNil(t, stored)
	require.Equal(t, origin.Id, stored.ChannelId)
	require.Equal(t, "origin prompt", stored.Prompt)
	submission, err := model.GetRoutingSubmission(service.CurrentRoutingSubmission(c).SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "accepted", submission.State)
	require.Equal(t, origin.Id, submission.ChannelID)
}

func TestMidjourneySmartTaskQueryDoesNotStartSubmission(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"code":1,"description":"seed","result":"42"}`))
	}))
	t.Cleanup(server.Close)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/mj/task/origin-task/image-seed", nil)
	c.Set(smartrouting.ContextKey, &model.TokenRouting{Mode: "smart"})
	response, _, err := service.DoMidjourneyHttpRequest(c, time.Second, server.URL)
	require.NoError(t, err)
	require.Equal(t, "42", response.Response.Result)
	require.Nil(t, service.CurrentRoutingSubmission(c))
	_, hasPlan := c.Get("mowan_routing_plan")
	require.False(t, hasPlan, "origin queries must not create a routing send plan")
}
