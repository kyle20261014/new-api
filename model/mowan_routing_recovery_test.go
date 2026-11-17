package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func routingRecoveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := routingMetricsTestStore(t).DB
	require.NoError(t, db.AutoMigrate(&Task{}, &Midjourney{}, &RoutingSubmission{}))
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })
	return db
}

func TestRoutingRecoveryTaskCASPreservesFundingAndReceipt(t *testing.T) {
	db := routingRecoveryTestDB(t)
	task := Task{TaskID: "actual", Status: TaskStatusInProgress, Quota: 9, PrivateData: TaskPrivateData{BillingSource: "subscription", SubscriptionId: 3, TokenId: 4}}
	require.NoError(t, db.Create(&task).Error)
	stale := task
	stale.Quota = 99
	stale.PrivateData = TaskPrivateData{}
	stale.Status = TaskStatusFailure
	won, err := stale.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	var stored Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, 9, stored.Quota)
	require.Equal(t, "subscription", stored.PrivateData.BillingSource)
	require.Equal(t, 3, stored.PrivateData.SubscriptionId)
	require.Equal(t, 4, stored.PrivateData.TokenId)
	require.Nil(t, stored.PrivateData.RoutingTerminal)
}

func routingRecoveryTestSubmission(t *testing.T, db *gorm.DB, id, kind string, status TaskStatus, receipt bool) RoutingSubmission {
	t.Helper()
	now := time.Now().Unix()
	k := RoutingMetricKey{ClientModel: "video", BusinessGroup: "default", ProtocolClass: "async", ProviderKey: "plugin:video:1", OperationKey: "video.create"}
	s := RoutingSubmission{SubmissionID: id, RequestID: id, AttemptNo: 1, UserID: 1, ChannelID: 2, State: "accepted", Version: 3, ClientModel: k.ClientModel, BusinessGroup: k.BusinessGroup, ProtocolClass: k.ProtocolClass, ProviderKey: k.ProviderKey, OperationKey: k.OperationKey, UpstreamTaskID: "upstream-" + id, LocalTaskKind: kind, CreatedAt: now - 90}
	var marker *RoutingTaskTerminalReceipt
	if receipt {
		marker = &RoutingTaskTerminalReceipt{Status: string(status), ObservedAt: now}
	}
	localID := "local-" + id
	if kind == "midjourney" {
		task := Midjourney{MjId: s.UpstreamTaskID, UserId: 1, ChannelId: 2, Status: string(status), SubmitTime: (now - 80) * 1000, FinishTime: now * 1000, RoutingTerminal: marker}
		require.NoError(t, db.Create(&task).Error)
		s.LocalTaskID, localID = int64(task.Id), task.MjId
	} else {
		task := Task{TaskID: localID, UserId: 1, ChannelId: 2, Group: "default", Status: status, SubmitTime: now - 80, FinishTime: now, PrivateData: TaskPrivateData{UpstreamTaskID: s.UpstreamTaskID, RoutingTerminal: marker}}
		require.NoError(t, db.Create(&task).Error)
		s.LocalTaskID = task.ID
	}
	require.NoError(t, db.Create(&s).Error)
	require.NoError(t, (RoutingMetricsStore{DB: db}).SaveTerminalSubmission(k, id, 2, "accepted", localID, time.Time{}))
	return s
}

func TestRoutingRecoveryTerminalRealTypedTaskExactlyOnce(t *testing.T) {
	db := routingRecoveryTestDB(t)
	store := RoutingMetricsStore{DB: db}
	for _, kind := range []string{"task", "midjourney"} {
		s := routingRecoveryTestSubmission(t, db, "real-"+kind, kind, TaskStatusSuccess, true)
		recorded, err := store.ObserveRoutingTerminal(s.SubmissionID, "boot-a", nil, time.Time{})
		require.NoError(t, err)
		require.True(t, recorded)
		recorded, err = store.ObserveRoutingTerminal(s.SubmissionID, "boot-b", nil, time.Time{})
		require.NoError(t, err)
		require.False(t, recorded)
	}
	fold, err := store.Fold(RoutingMetricKey{ClientModel: "video", BusinessGroup: "default", ProtocolClass: "async", ProviderKey: "plugin:video:1", OperationKey: "video.create"}, RoutingStageTaskTerminal, []int{2}, time.Now().Add(-time.Hour), time.Time{})
	require.NoError(t, err)
	require.EqualValues(t, 2, fold.Success)
	require.EqualValues(t, 2, fold.SpeedSamples)
	require.Equal(t, float64(160000), fold.SpeedTotalMS)
	require.Zero(t, fold.Active)

	synthetic := routingRecoveryTestSubmission(t, db, "local-timeout", "task", TaskStatusFailure, false)
	recorded, err := store.ObserveRoutingTerminal(synthetic.SubmissionID, "boot-a", nil, time.Time{})
	require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
	require.False(t, recorded)
	var observation RoutingTerminalObservation
	require.NoError(t, db.First(&observation, "submission_id = ?", synthetic.SubmissionID).Error)
	require.Equal(t, "accepted", observation.Status)
}

func TestRoutingRecoveryUpstreamCASReceiptOnlyForWinner(t *testing.T) {
	db := routingRecoveryTestDB(t)
	task := Task{TaskID: "provider-task", Status: TaskStatusInProgress, Quota: 8}
	require.NoError(t, db.Create(&task).Error)
	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithUpstreamStatus(context.Background(), TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	var stored Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, string(TaskStatusSuccess), stored.PrivateData.RoutingTerminal.Status)
	require.Positive(t, stored.PrivateData.RoutingTerminal.ObservedAt)
	task.Status = TaskStatusFailure
	won, err = task.UpdateWithUpstreamStatus(context.Background(), TaskStatusInProgress)
	require.NoError(t, err)
	require.False(t, won)
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, string(TaskStatusSuccess), stored.PrivateData.RoutingTerminal.Status)
}

func TestRoutingRecoveryTerminalBucketFailureRollsBackEventAndCanRetry(t *testing.T) {
	db := routingRecoveryTestDB(t)
	s := routingRecoveryTestSubmission(t, db, "atomic-terminal", "task", TaskStatusFailure, true)
	const hook = "routing_recovery_bucket_failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "mowan_routing_metric_buckets" {
			tx.AddError(errors.New("injected terminal bucket failure"))
		}
	}))
	store := RoutingMetricsStore{DB: db}
	recorded, err := store.ObserveRoutingTerminal(s.SubmissionID, "boot", nil, time.Time{})
	require.ErrorContains(t, err, "injected terminal bucket failure")
	require.False(t, recorded)
	require.NoError(t, db.Callback().Create().Remove(hook))
	var observation RoutingTerminalObservation
	require.NoError(t, db.First(&observation, "submission_id = ?", s.SubmissionID).Error)
	require.Equal(t, "accepted", observation.Status)
	var n int64
	require.NoError(t, db.Model(&RoutingHealthEvent{}).Count(&n).Error)
	require.Zero(t, n)
	recorded, err = store.ObserveRoutingTerminal(s.SubmissionID, "boot", nil, time.Time{})
	require.NoError(t, err)
	require.True(t, recorded)
	fold, err := store.Fold(s.metricKey(), RoutingStageTaskTerminal, []int{2}, time.Now().Add(-time.Hour), time.Time{})
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Failure)
}
