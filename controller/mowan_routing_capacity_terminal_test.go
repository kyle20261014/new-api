package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingMidjourneyLateProviderTerminalPrecedesLocalCutoff(t *testing.T) {
	for _, status := range []string{"SUCCESS", "FAILURE", "IN_PROGRESS_THEN_SUCCESS", "IN_PROGRESS_THEN_FAILURE", "REASON_THEN_SUCCESS", "REASON_THEN_FAILURE", "PROGRESS_THEN_SUCCESS", "PROGRESS_THEN_FAILURE"} {
		t.Run(status, func(t *testing.T) {
			oldSubmit := time.Now().Add(-61 * time.Minute).UnixMilli()
			var polls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/mj/submit/imagine" {
					_, _ = io.WriteString(w, `{"code":1,"description":"accepted","result":"late-mj"}`)
					return
				}
				assert.Equal(t, "/mj/task/list-by-condition", r.URL.Path)
				providerStatus := status
				if _, terminal, delayed := strings.Cut(status, "_THEN_"); delayed {
					providerStatus = "IN_PROGRESS"
					if polls.Add(1) > 1 {
						providerStatus = terminal
					}
				}
				progress, reason := "100%", ""
				if providerStatus == "IN_PROGRESS" {
					progress = "45%"
					if strings.HasPrefix(status, "REASON_") {
						reason = "provider informational reason"
					} else if strings.HasPrefix(status, "PROGRESS_") {
						progress = "100%"
					}
				} else if providerStatus == "FAILURE" {
					progress = "45%"
					reason = "provider rejected composition"
					if strings.HasPrefix(status, "PROGRESS_") {
						reason = ""
					}
				}
				body, err := common.Marshal([]dto.MidjourneyDto{{MjId: "late-mj", Status: providerStatus, Progress: progress, FailReason: reason, SubmitTime: oldSubmit}})
				require.NoError(t, err)
				_, _ = w.Write(body)
			}))
			defer upstream.Close()
			db, token := smartProtocolEntryDB(t, "mj_imagine", constant.ChannelTypeMidjourney, upstream.URL)
			router := gin.New()
			router.Use(middleware.TokenAuth(), middleware.Distribute())
			router.POST("/mj/submit/imagine", RelayMidjourney)
			req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{"prompt":"a lighthouse"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var task model.Midjourney
			var submission model.RoutingSubmission
			require.NoError(t, db.First(&task).Error)
			require.NoError(t, db.First(&submission).Error)
			require.Positive(t, task.Quota)
			reserved := task.Quota
			require.NoError(t, db.Model(&task).Updates(map[string]any{"submit_time": oldSubmit, "progress": "10%"}).Error)
			assert.Equal(t, 1, runMidjourneyTaskUpdateOnce(context.Background(), nil).ChannelsScanned)
			require.NoError(t, db.First(&task, task.Id).Error)
			var funding model.RoutingFundingSession
			require.NoError(t, db.First(&funding, "funding_id = ?", submission.FundingID).Error)
			if strings.Contains(status, "_THEN_") {
				require.Equal(t, "IN_PROGRESS", task.Status)
				if strings.HasPrefix(status, "PROGRESS_") {
					assert.Equal(t, "100%", task.Progress)
				} else {
					assert.Equal(t, "45%", task.Progress)
				}
				assert.Nil(t, task.RoutingTerminal)
				assert.Equal(t, "reserved", funding.Status)
				assertSmartEntryBalances(t, db, reserved)
				require.True(t, model.HasUnfinishedMidjourneyTasks())
				assert.Equal(t, 1, runMidjourneyTaskUpdateOnce(context.Background(), nil).ChannelsScanned)
				require.NoError(t, db.First(&task, task.Id).Error)
				require.NoError(t, db.First(&funding, "funding_id = ?", submission.FundingID).Error)
			}
			_, terminal, delayed := strings.Cut(status, "_THEN_")
			if !delayed {
				terminal = status
			}
			assert.Equal(t, terminal, task.Status)
			require.NotNil(t, task.RoutingTerminal)
			assert.Equal(t, terminal, task.RoutingTerminal.Status)
			assert.Positive(t, task.RoutingTerminal.ObservedAt)
			if terminal == "FAILURE" {
				assert.Equal(t, "refunded", funding.Status)
				assert.Zero(t, task.Quota)
				assertSmartEntryBalances(t, db, 0)
			} else {
				assert.Equal(t, "settled", funding.Status)
				assertSmartEntryBalances(t, db, reserved)
			}
		})
	}
}

func TestRoutingLegacyMidjourneyLocalCutoffStillRefunds(t *testing.T) {
	oldSubmit := time.Now().Add(-61 * time.Minute).UnixMilli()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/mj/task/list-by-condition", r.URL.Path)
		body, err := common.Marshal([]dto.MidjourneyDto{{MjId: "legacy-late-mj", Status: "IN_PROGRESS", Progress: "45%", SubmitTime: oldSubmit}})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	db, token := smartProtocolEntryDB(t, "mj_imagine", constant.ChannelTypeMidjourney, upstream.URL)
	var channel model.Channel
	require.NoError(t, db.First(&channel).Error)
	task := model.Midjourney{MjId: "legacy-late-mj", UserId: token.UserId, TokenId: token.Id, ChannelId: channel.Id, Quota: 30, Status: "IN_PROGRESS", Progress: "10%", SubmitTime: oldSubmit}
	require.NoError(t, db.Create(&task).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", token.UserId).Update("quota", 1_000_000-task.Quota).Error)
	require.NoError(t, db.Model(token).Update("remain_quota", 1_000_000-task.Quota).Error)
	assert.Equal(t, 1, runMidjourneyTaskUpdateOnce(context.Background(), nil).ChannelsScanned)
	require.NoError(t, db.First(&task, task.Id).Error)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Nil(t, task.RoutingTerminal)
	assert.Zero(t, task.Quota)
	assertSmartEntryBalances(t, db, 0)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
}

func TestRoutingLegacyMidjourneyReasonStillCompletesAndRefunds(t *testing.T) {
	submit := time.Now().Add(-time.Minute).UnixMilli()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := common.Marshal([]dto.MidjourneyDto{{MjId: "legacy-reason-mj", Status: "IN_PROGRESS", Progress: "45%", FailReason: "legacy reason", SubmitTime: submit}})
		require.NoError(t, err)
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	db, token := smartProtocolEntryDB(t, "mj_imagine", constant.ChannelTypeMidjourney, upstream.URL)
	var channel model.Channel
	require.NoError(t, db.First(&channel).Error)
	task := model.Midjourney{MjId: "legacy-reason-mj", UserId: token.UserId, TokenId: token.Id, ChannelId: channel.Id, Quota: 30, Status: "IN_PROGRESS", Progress: "10%", SubmitTime: submit}
	require.NoError(t, db.Create(&task).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", token.UserId).Update("quota", 1_000_000-task.Quota).Error)
	require.NoError(t, db.Model(token).Update("remain_quota", 1_000_000-task.Quota).Error)
	assert.Equal(t, 1, runMidjourneyTaskUpdateOnce(context.Background(), nil).ChannelsScanned)
	require.NoError(t, db.First(&task, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Nil(t, task.RoutingTerminal)
	assert.Zero(t, task.Quota)
	assertSmartEntryBalances(t, db, 0)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
}
