package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRoutingSubmissionController(t *testing.T) (*model.Task, *model.RoutingSubmission) {
	t.Helper()
	task := setupGenericTaskTest(t)
	// These extension tables have fixed names, unlike the harness's prefixed Task.
	if model.DB.Migrator().HasTable(&model.RoutingSubmission{}) || model.DB.Migrator().HasTable(&model.RoutingFundingSession{}) {
		t.Skip("refusing to use existing routing extension tables in an external test database")
	}
	require.NoError(t, model.DB.AutoMigrate(&model.RoutingSubmission{}, &model.RoutingFundingSession{}, &model.Midjourney{}))
	db := model.DB
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(&model.RoutingSubmission{}, &model.RoutingFundingSession{}, &model.Midjourney{}))
	})
	s := &model.RoutingSubmission{SubmissionID: "owner-sub", RequestID: "request", AttemptNo: 1, UserID: task.UserId, FundingID: "owner-fund", ClientModel: "client-model", BusinessGroup: "paid", State: "unknown", Quota: 500000, CreatedAt: 100, UpdatedAt: 101, RecoveryJSON: "private-recovery-key", PriceSnapshot: "private-price", UpstreamTaskID: "private-upstream", ProviderKey: "private-provider", ChannelID: 99}
	require.NoError(t, model.DB.Create(s).Error)
	require.NoError(t, model.DB.Create(&model.RoutingFundingSession{FundingID: s.FundingID, UserID: s.UserID, Status: "reserved"}).Error)
	return task, s
}

func TestRoutingSubmissionOwnerDetailNeverExposesPrivateFields(t *testing.T) {
	task, s := setupRoutingSubmissionController(t)
	for _, userID := range []int{task.UserId, 999} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", userID)
		c.Set("role", common.RoleRootUser)
		c.Params = gin.Params{{Key: "submission_id", Value: s.SubmissionID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/api/mowan/smart-routing/submissions/"+s.SubmissionID, nil)
		GetRoutingSubmissionDetail(c)
		if userID != task.UserId {
			assert.Equal(t, http.StatusNotFound, w.Code)
			continue
		}
		require.Equal(t, http.StatusOK, w.Code)
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		assert.Nil(t, response.Data["task_id"])
		assert.Equal(t, "manual_review", response.Data["funding_status"])
		assert.Equal(t, "client-model", response.Data["model"])
		for _, private := range []string{"private-", "recovery", "price_snapshot", "upstream", "channel_id", "provider", "funding_id", "token_id", "user_id"} {
			assert.NotContains(t, w.Body.String(), private)
		}
	}
}

func TestRoutingSubmissionTaskListLinksOnceAndPreservesTaskDTO(t *testing.T) {
	task, s := setupRoutingSubmissionController(t)
	require.NoError(t, model.DB.Model(task).Update("group", "legacy-task-group").Error)
	for _, linked := range []bool{false, true} {
		if linked {
			require.NoError(t, model.DB.Model(s).Updates(map[string]any{"local_task_id": task.ID, "state": "accepted"}).Error)
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", task.UserId)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/self?p=1&page_size=10", nil)
		GetUserTask(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Data struct {
				Total int              `json:"total"`
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		if !linked {
			require.Equal(t, 2, response.Data.Total)
			assert.Equal(t, "submission", response.Data.Items[0]["row_type"])
			assert.Nil(t, response.Data.Items[0]["task_id"])
			continue
		}
		require.Equal(t, 1, response.Data.Total)
		require.Len(t, response.Data.Items, 1)
		item := response.Data.Items[0]
		assert.Equal(t, "task", item["row_type"])
		assert.Equal(t, task.TaskID, item["task_id"])
		assert.Equal(t, s.SubmissionID, item["submission_id"])
		assert.Equal(t, "accepted", item["submission_status"])
		assert.Contains(t, item, "data")
		assert.Nil(t, item["data"])
		nested, ok := item["submission"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "paid", nested["group"])
		assert.Equal(t, "legacy-task-group", item["group"])
		assert.Equal(t, task.TaskID, nested["task_id"])
		assert.EqualValues(t, s.CreatedAt, nested["submit_time"])
		assert.EqualValues(t, s.UpdatedAt, nested["updated_at"])
		assert.NotContains(t, w.Body.String(), "private-")
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", task.UserId)
	c.Params = gin.Params{{Key: "submission_id", Value: s.SubmissionID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/mowan/smart-routing/submissions/"+s.SubmissionID, nil)
	GetRoutingSubmissionDetail(c)
	require.Equal(t, http.StatusOK, w.Code)
	var detail struct {
		Data routingSubmissionDetail `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Data.TaskID)
	assert.Equal(t, task.TaskID, *detail.Data.TaskID)
	assert.NotContains(t, w.Body.String(), "private-upstream")
}

func TestRoutingSubmissionMidjourneyDetailAndListUseRealTypedIdentity(t *testing.T) {
	task, s := setupRoutingSubmissionController(t)
	require.NoError(t, model.DB.Create(&model.Midjourney{Id: int(task.ID), UserId: task.UserId, MjId: "mj-real-public", Prompt: "private-prompt"}).Error)
	require.NoError(t, model.DB.Model(s).Updates(map[string]any{"local_task_id": task.ID, "local_task_kind": "midjourney", "state": "accepted"}).Error)
	for _, detail := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", task.UserId)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/self?submission_id="+s.SubmissionID, nil)
		if detail {
			c.Params = gin.Params{{Key: "submission_id", Value: s.SubmissionID}}
			GetRoutingSubmissionDetail(c)
		} else {
			GetUserTask(c)
		}
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		item := response.Data
		if !detail {
			items, ok := item["items"].([]any)
			require.True(t, ok)
			require.Len(t, items, 1)
			item = items[0].(map[string]any)
			assert.Equal(t, "submission", item["row_type"])
			assert.NotContains(t, item, "id")
		}
		assert.Equal(t, "mj-real-public", item["task_id"])
		assert.NotContains(t, w.Body.String(), task.TaskID)
		assert.NotContains(t, w.Body.String(), "private-")
	}
}

func TestRoutingSubmissionAdminListRetainsAllUsersAndUserFilter(t *testing.T) {
	task, _ := setupRoutingSubmissionController(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 8, Username: "other-owner", AffCode: "other-owner-code"}).Error)
	require.NoError(t, model.DB.Create(&model.Task{TaskID: "foreign-task", UserId: 8, ChannelId: 17, SubmitTime: 200}).Error)
	for _, tc := range []struct {
		query string
		total int
	}{{"", 3}, {"?user_id=8&channel_id=17", 1}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", task.UserId)
		c.Set("role", common.RoleAdminUser)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+tc.query, nil)
		GetAllTask(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var response struct {
			Data struct {
				Total int              `json:"total"`
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, tc.total, response.Data.Total)
		assert.Equal(t, "foreign-task", response.Data.Items[0]["task_id"])
		assert.EqualValues(t, 17, response.Data.Items[0]["channel_id"])
	}
}

func TestRoutingSubmissionTaskListFiltersOwnershipAndReadFailures(t *testing.T) {
	task, s := setupRoutingSubmissionController(t)
	for _, tc := range []struct {
		query               string
		user, status, total int
	}{
		{"?submission_id=owner-sub", task.UserId, 200, 1}, {"?submission_id=owner-sub", 999, 200, 0}, {"?status=unknown", task.UserId, 200, 1}, {"?status=invalid", task.UserId, 400, 0}, {"?task_id=" + task.TaskID, task.UserId, 200, 1}, {"?channel_id=99", task.UserId, 200, 2},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", tc.user)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/self"+tc.query, nil)
		GetUserTask(c)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status == 200 {
			var response struct {
				Data struct {
					Total int `json:"total"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, tc.total, response.Data.Total)
		}
	}
	require.NoError(t, model.DB.Migrator().DropTable(&model.RoutingFundingSession{}))
	for _, detail := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", task.UserId)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/task/self", nil)
		if detail {
			c.Params = gin.Params{{Key: "submission_id", Value: s.SubmissionID}}
			GetRoutingSubmissionDetail(c)
		} else {
			GetUserTask(c)
		}
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	}
}

func TestRoutingSubmissionReservedAmountUsesSiteDisplayUnit(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previous := *settings
	quota, rate := common.QuotaPerUnit, operation_setting.USDExchangeRate
	t.Cleanup(func() { *settings = previous; common.QuotaPerUnit = quota; operation_setting.USDExchangeRate = rate })
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate = 7
	for _, tc := range []struct {
		display, unit string
		amount        float64
	}{{"USD", "USD", 1}, {"CNY", "CNY", 7}, {"TOKENS", "TOKENS", 500000}, {"CUSTOM", "points", 3}} {
		settings.QuotaDisplayType = tc.display
		settings.CustomCurrencySymbol = "points"
		settings.CustomCurrencyExchangeRate = 3
		detail, err := projectRoutingSubmission(&model.RoutingSubmission{Quota: 500000, State: "accepted"}, "settled")
		require.NoError(t, err)
		assert.Equal(t, tc.unit, detail.ReservedUnit)
		assert.Equal(t, tc.amount, detail.ReservedAmount)
		assert.Equal(t, "settled", detail.FundingStatus)
	}
	detail, err := projectRoutingSubmission(&model.RoutingSubmission{Quota: 0, State: "unknown"}, "refunded")
	require.NoError(t, err)
	assert.Zero(t, detail.ReservedAmount)
	assert.Equal(t, "released", detail.FundingStatus)
	settings.QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	common.QuotaPerUnit = 0
	_, err = projectRoutingSubmission(&model.RoutingSubmission{State: "accepted", Quota: 1}, "reserved")
	require.Error(t, err)
	_, err = projectRoutingSubmission(&model.RoutingSubmission{State: "invalid"}, "reserved")
	require.Error(t, err)
}
