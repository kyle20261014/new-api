package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func routingRecoveryServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "recovery.db")), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Midjourney{}, &model.User{}, &model.Token{}, &model.SystemTask{}, &model.SystemTaskLock{}))
	require.NoError(t, model.MigrateRoutingFunding(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
	old := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = old; require.NoError(t, sqlDB.Close()) })
	return db
}

func routingRecoveryServiceHandler(boot string) *routingRecoveryHandler {
	return &routingRecoveryHandler{bootID: boot, observers: make(map[string]model.RoutingProbeObserver)}
}

func routingRecoveryServiceSubmission(t *testing.T, db *gorm.DB, id string, probe bool) (*model.RoutingSubmission, model.RoutingMetricKey) {
	t.Helper()
	u := model.User{Username: "user-" + id, Quota: 100, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&u).Error)
	k := model.RoutingMetricKey{ClientModel: "video", BusinessGroup: "default", ProtocolClass: "async", ProviderKey: "plugin:video:1", OperationKey: "video.create"}
	s := &model.RoutingSubmission{SubmissionID: id, RequestID: id, AttemptNo: 1, ChannelID: 1, ClientModel: k.ClientModel, BusinessGroup: k.BusinessGroup, ProtocolClass: k.ProtocolClass, ProviderKey: k.ProviderKey, OperationKey: k.OperationKey}
	if probe {
		store := model.RoutingMetricsStore{DB: db}
		for i := 0; i < 3; i++ {
			_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: k, Stage: model.RoutingStageTaskTerminal, EventID: fmt.Sprintf("open-%s-%d", id, i), Outcome: "failure", OccurredAt: time.Now().Add(-40 * time.Second)}, time.Now().Add(-40*time.Second))
			require.NoError(t, err)
		}
		claim, ok, err := store.ClaimProbe(k, model.RoutingStageTaskTerminal, "probe-"+id, time.Time{})
		require.NoError(t, err)
		require.True(t, ok)
		s.TerminalProbeEpoch, s.TerminalProbeLeaseID = claim.Epoch, claim.LeaseID
	}
	_, err := model.PrepareRoutingFunding(model.RoutingFundingRequest{FundingID: "fund-" + id, OperationID: "reserve-" + id, UserID: u.Id, Source: "wallet", Kind: "reserve", Quota: 30}, s)
	require.NoError(t, err)
	s, err = model.MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.NoError(t, err)
	return s, k
}

func TestRoutingRecoveryAcceptedProbeOutlivesSubmitAndFencesOldObserver(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	s, k := routingRecoveryServiceSubmission(t, db, "accepted", true)
	store := model.RoutingMetricsStore{DB: db}
	material := &model.RoutingTaskRecovery{Task: model.Task{TaskID: "task-local", UserId: s.UserID, Group: "default", ChannelId: 1, Quota: 30, Status: model.TaskStatusInProgress, SubmitTime: time.Now().Unix() - 60}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream"}}
	accepted, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "upstream", material, s.SendOwnerID)
	require.NoError(t, err)
	// Ending the submission attempt cannot delete or close the terminal probe.
	require.NoError(t, store.StartLease(model.RoutingMetricLease{LeaseID: "submit", RoutingMetricKey: k, Stage: model.RoutingStageAttempt, ChannelID: 1}, time.Time{}))
	require.NoError(t, store.FinishLease("submit"))
	a := routingRecoveryServiceHandler("boot-a")
	require.NoError(t, a.claimObservers(store))
	require.Len(t, a.observers, 1)
	old := a.observers[s.SubmissionID]
	require.NoError(t, a.recoverSubmission(context.Background(), store, accepted))
	require.NoError(t, a.heartbeat(store, true))
	health, err := store.ReadHealth(k, model.RoutingStageTaskTerminal, 1, time.Time{})
	require.NoError(t, err)
	require.Equal(t, model.RoutingHealthHalfOpen, health.Health.State)
	require.Equal(t, old.Owner, health.Health.ObserverOwner)
	require.GreaterOrEqual(t, health.Health.ObserverUntil, time.Now().Unix()+13)
	require.GreaterOrEqual(t, health.Health.ProbeUntil, time.Now().Unix()+28)
	a.releaseObservers()
	require.Empty(t, a.observers)
	health, err = store.ReadHealth(k, model.RoutingStageTaskTerminal, 1, time.Time{})
	require.NoError(t, err)
	require.Empty(t, health.Health.ObserverOwner)
	require.GreaterOrEqual(t, health.Health.ProbeUntil, time.Now().Unix()+28)
	// A successor can claim immediately during the scheduler's <=15s gap.
	a.probeCursor = ""
	require.NoError(t, a.claimObservers(store))
	old = a.observers[s.SubmissionID]

	// Simulate a crashed observer while its independently renewed probe is live.
	require.NoError(t, db.Model(&model.RoutingHealth{}).Where("probe_submission_id = ?", s.SubmissionID).Update("observer_until", time.Now().Unix()-1).Error)
	b := routingRecoveryServiceHandler("boot-b")
	require.NoError(t, b.claimObservers(store))
	next := b.observers[s.SubmissionID]
	require.Greater(t, next.Generation, old.Generation)
	linked, err := model.GetRoutingSubmission(s.SubmissionID)
	require.NoError(t, err)
	var task model.Task
	require.NoError(t, db.First(&task, linked.LocalTaskID).Error)
	task.Status, task.FinishTime = model.TaskStatusSuccess, time.Now().Unix()
	won, err := task.UpdateWithUpstreamStatus(context.Background(), model.TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	recorded, err := store.ObserveRoutingTerminal(s.SubmissionID, "boot-a", &old, time.Time{})
	require.ErrorIs(t, err, model.ErrRoutingMetricsConflict)
	require.False(t, recorded)
	require.NoError(t, b.heartbeat(store, true))
	require.Empty(t, b.observers)
	recorded, err = store.ObserveRoutingTerminal(s.SubmissionID, "boot-c", nil, time.Time{})
	require.NoError(t, err)
	require.False(t, recorded)
	health, err = store.ReadHealth(k, model.RoutingStageTaskTerminal, 0, time.Time{})
	require.NoError(t, err)
	require.Equal(t, model.RoutingHealthClosed, health.Health.State)
	fold, err := store.Fold(k, model.RoutingStageTaskTerminal, []int{1}, time.Now().Add(-time.Hour), time.Time{})
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Success)
	require.Zero(t, fold.Active)
}

func TestRoutingRecoveryLocalFailureCannotRefundAcceptedTask(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	s, _ := routingRecoveryServiceSubmission(t, db, "local-failure-funding", false)
	recovery := &model.RoutingTaskRecovery{Task: model.Task{TaskID: "local-failure", UserId: s.UserID, Group: "default", ChannelId: 1, Quota: 30, Status: model.TaskStatusInProgress}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider-running"}}
	accepted, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "provider-running", recovery, s.SendOwnerID)
	require.NoError(t, err)
	task, err := model.PersistAcceptedRoutingTask(accepted.SubmissionID)
	require.NoError(t, err)
	task.Status = model.TaskStatusFailure
	won, err := task.UpdateWithStatus(model.TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	err = routingRecoveryServiceHandler("local-failure-boot").recoverSubmission(context.Background(), model.RoutingMetricsStore{DB: db}, accepted)
	require.ErrorIs(t, err, model.ErrRoutingTerminalUnconfirmed)
	var user model.User
	require.NoError(t, db.First(&user, s.UserID).Error)
	require.Equal(t, 70, user.Quota)
	var fund model.RoutingFundingSession
	require.NoError(t, db.First(&fund, "funding_id = ?", s.FundingID).Error)
	require.Equal(t, "reserved", fund.Status)
	var refunds int64
	require.NoError(t, db.Model(&model.RoutingFundingOperation{}).Where("funding_id = ? AND kind = ?", s.FundingID, "refund").Count(&refunds).Error)
	require.Zero(t, refunds)
}

func TestRoutingRecoveryHandlerActuallyTicksBeforeSchedulerInterval(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	s, k := routingRecoveryServiceSubmission(t, db, "ticker", true)
	_, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "ticker-upstream", &model.RoutingTaskRecovery{Task: model.Task{TaskID: "ticker-local", UserId: s.UserID, Group: "default", ChannelId: 1, Status: model.TaskStatusInProgress}}, s.SendOwnerID)
	require.NoError(t, err)
	h := routingRecoveryServiceHandler("boot-ticker")
	withSystemTaskRegistry(t, h)
	runSystemTaskScheduler()
	task, err := model.GetLatestSystemTask(h.Type())
	require.NoError(t, err)
	require.NotNil(t, task)
	task, ok, err := model.ClaimSystemTask(task.ID, h.Type(), "runner", time.Now().Unix()+60)
	require.NoError(t, err)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 6500*time.Millisecond)
	defer cancel()
	h.Run(ctx, task, "runner")
	require.GreaterOrEqual(t, h.beat, uint64(2), "the production handler must heartbeat inside the scheduler's 15s interval")
	health, err := (model.RoutingMetricsStore{DB: db}).ReadHealth(k, model.RoutingStageTaskTerminal, 1, time.Time{})
	require.NoError(t, err)
	require.Equal(t, model.RoutingHealthHalfOpen, health.Health.State)
	require.Empty(t, health.Health.ObserverOwner, "cancellation releases the observer")
	require.GreaterOrEqual(t, health.Health.ProbeUntil, time.Now().Unix()+28, "handoff leaves a renewed probe")
	task, err = model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusFailed, task.Status)
	require.Contains(t, task.Error, context.DeadlineExceeded.Error())
}

func TestRoutingRecoveryUnknownNeverCreatesOrRefunds(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	s, _ := routingRecoveryServiceSubmission(t, db, "unknown", false)
	require.NoError(t, db.Model(&model.RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", time.Now().Unix()-1).Error)
	h := routingRecoveryServiceHandler("boot")
	store := model.RoutingMetricsStore{DB: db}
	require.NoError(t, h.recoverSubmission(context.Background(), store, s))
	s, err := model.GetRoutingSubmission(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "unknown", s.State)
	for i := 0; i < 2; i++ {
		require.ErrorContains(t, h.recoverSubmission(context.Background(), store, s), "manual reconciliation required")
	}
	var logs bytes.Buffer
	writer := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	h.step(context.Background())
	gin.DefaultErrorWriter = writer
	require.Contains(t, logs.String(), "manual reconciliation required")
	var user model.User
	require.NoError(t, db.First(&user, s.UserID).Error)
	require.Equal(t, 70, user.Quota)
	for _, target := range []any{&model.Task{}, &model.Midjourney{}} {
		var n int64
		require.NoError(t, db.Model(target).Count(&n).Error)
		require.Zero(t, n)
	}
	var fund model.RoutingFundingSession
	require.NoError(t, db.First(&fund, "funding_id = ?", s.FundingID).Error)
	require.Equal(t, "reserved", fund.Status)
	var n int64
	require.NoError(t, db.Model(&model.RoutingFundingOperation{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestRoutingRecoveryNegativeCrashWindowIsScannedOnlyUntilReleased(t *testing.T) {
	for _, state := range []string{"not_sent", "rejected"} {
		t.Run(state, func(t *testing.T) {
			db := routingRecoveryServiceDB(t)
			s, _ := routingRecoveryServiceSubmission(t, db, "negative", false)
			s, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, state, "", nil, s.SendOwnerID)
			require.NoError(t, err)
			h := routingRecoveryServiceHandler("boot")
			store := model.RoutingMetricsStore{DB: db}
			require.NoError(t, h.recoverPage(context.Background(), store))
			var user model.User
			require.NoError(t, db.First(&user, s.UserID).Error)
			require.Equal(t, 70, user.Quota, "live fallback owner retains its reservation")
			require.NoError(t, db.Model(&model.RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
			h.submissionCursor = ""
			require.NoError(t, h.recoverPage(context.Background(), store))
			require.NoError(t, db.First(&user, s.UserID).Error)
			require.Equal(t, 100, user.Quota)
			rows, err := store.RoutingRecoveryPage("", 64)
			require.NoError(t, err)
			require.Empty(t, rows, "completed negative journals must not starve recovery pages")
		})
	}
}

func TestRoutingRecoveryRetentionRunsThroughExistingSchedulerAndLogsDBFailure(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	h := routingMetricsRetentionHandler{}
	withSystemTaskRegistry(t, h)
	store := model.RoutingMetricsStore{DB: db}
	k := routingAttemptTestKey("sync")
	old := time.Now().Add(-49 * time.Hour)
	_, err := store.UpsertSnapshot(model.RoutingMetricBucket{RoutingMetricKey: k, Stage: model.RoutingStageAttempt, NodeBootID: "old", ChannelID: 1, Minute: old.Unix() / 60 * 60, Revision: 1}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.RoutingTerminalObservation{SubmissionID: "unresolved", Status: "unknown", UpdatedAt: old.Unix()}).Error)
	require.NoError(t, db.Create(&model.RoutingFundingSession{FundingID: "unresolved-funds", Quota: 30, Status: "reserved", UpdatedAt: old.Unix()}).Error)
	require.NoError(t, db.Create(&model.RoutingHealth{ID: "unresolved-health", Stage: model.RoutingStageTaskTerminal, State: model.RoutingHealthOpen, Version: 1, LastEventAt: old.Unix()}).Error)
	runSystemTaskScheduler()
	task, err := model.GetLatestSystemTask(h.Type())
	require.NoError(t, err)
	require.NotNil(t, task)
	task, ok, err := model.ClaimSystemTask(task.ID, h.Type(), "runner", time.Now().Unix()+60)
	require.NoError(t, err)
	require.True(t, ok)
	h.Run(context.Background(), task, "runner")
	var n int64
	require.NoError(t, db.Model(&model.RoutingMetricBucket{}).Count(&n).Error)
	require.Zero(t, n)
	for _, target := range []any{&model.RoutingTerminalObservation{}, &model.RoutingFundingSession{}, &model.RoutingHealth{}} {
		require.NoError(t, db.Model(target).Count(&n).Error)
		require.EqualValues(t, 1, n, "retention must preserve unresolved state and funds")
	}
	// Finished journals survive retention, but must not be treated as a new
	// terminal observation after its 48h dedup row has been deleted.
	require.NoError(t, db.Create(&model.RoutingFundingSession{FundingID: "finished-funds", Status: "settled"}).Error)
	require.NoError(t, db.Create(&model.RoutingSubmission{SubmissionID: "finished", RequestID: "finished", AttemptNo: 1, State: "accepted", FundingID: "finished-funds", LocalTaskID: 1}).Error)
	rows, err := store.RoutingRecoveryPage("", 64)
	require.NoError(t, err)
	require.Empty(t, rows)
	task, err = model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusSucceeded, task.Status)

	var logs bytes.Buffer
	writer := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	t.Cleanup(func() { gin.DefaultErrorWriter = writer })
	require.NoError(t, db.Migrator().DropTable(&model.RoutingMetricBucket{}))
	failed, err := model.CreateSystemTask(h.Type(), nil, nil)
	require.NoError(t, err)
	failed, ok, err = model.ClaimSystemTask(failed.ID, h.Type(), "runner", time.Now().Unix()+60)
	require.NoError(t, err)
	require.True(t, ok)
	h.Run(context.Background(), failed, "runner")
	failed, err = model.GetSystemTaskByTaskID(failed.TaskID)
	require.NoError(t, err)
	require.Equal(t, model.SystemTaskStatusFailed, failed.Status)
	require.Contains(t, failed.Error, "48h retention")
	require.Contains(t, logs.String(), "48h retention")
	require.Contains(t, logs.String(), "no such table")
}

func TestRoutingRecoveryPageCursorAndCancellation(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	for i := 0; i < model.RoutingRecoveryPageSize+1; i++ {
		require.NoError(t, db.Create(&model.RoutingSubmission{SubmissionID: fmt.Sprintf("sub-%03d", i), RequestID: fmt.Sprintf("req-%03d", i), AttemptNo: 1, State: "unknown"}).Error)
	}
	h := routingRecoveryServiceHandler("boot")
	store := model.RoutingMetricsStore{DB: db}
	require.ErrorContains(t, h.recoverPage(context.Background(), store), "manual reconciliation required")
	require.Equal(t, "sub-063", h.submissionCursor)
	require.ErrorContains(t, h.recoverPage(context.Background(), store), "manual reconciliation required")
	require.Equal(t, "sub-064", h.submissionCursor)
	require.NoError(t, h.recoverPage(context.Background(), store))
	require.Empty(t, h.submissionCursor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := routingRecoveryCall(ctx, func(_ context.Context, s model.RoutingMetricsStore) error {
		_, err := s.RoutingRecoveryPage("", 1)
		return err
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRoutingRecoveryObserverCapacityAndMaximumExpiryAreVisible(t *testing.T) {
	db := routingRecoveryServiceDB(t)
	s, _ := routingRecoveryServiceSubmission(t, db, "capacity", true)
	_, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "capacity-upstream", &model.RoutingTaskRecovery{Task: model.Task{TaskID: "capacity-local", UserId: s.UserID, Group: "default", ChannelId: 1, Status: model.TaskStatusInProgress}}, s.SendOwnerID)
	require.NoError(t, err)
	h := routingRecoveryServiceHandler("boot")
	for i := 0; i < routingRecoveryObserverLimit; i++ {
		h.observers[fmt.Sprint(i)] = model.RoutingProbeObserver{}
	}
	store := model.RoutingMetricsStore{DB: db}
	require.ErrorContains(t, h.claimObservers(store), "capacity reached: limit=128")
	clear(h.observers)
	h.probeCursor = ""
	// The lease is still live, but the maximum 24h probe tenure has elapsed.
	require.NoError(t, db.Model(&model.RoutingHealth{}).Where("probe_submission_id = ?", s.SubmissionID).Updates(map[string]any{"probe_started_at": time.Now().Unix() - 86401, "probe_until": time.Now().Unix() + 30}).Error)
	var logs bytes.Buffer
	writer := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logs
	t.Cleanup(func() { gin.DefaultErrorWriter = writer })
	require.NoError(t, h.claimObservers(store))
	require.Contains(t, logs.String(), "maximum_24h")
	require.Contains(t, logs.String(), "original task and funds retained")
	var fund model.RoutingFundingSession
	require.NoError(t, db.First(&fund, "funding_id = ?", s.FundingID).Error)
	require.Equal(t, "reserved", fund.Status)
}

func TestRoutingRecoveryActualPollingWritesOnlyRealTerminalReceipt(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("batch=%t/real-terminal=%t", batch, terminal), func(t *testing.T) {
				truncate(t)
				seedTaskPollingChannel(t, 903, true)
				task := seedPollingTask(t, 903, "receipt-task", "receipt-upstream")
				info := &relaycommon.TaskInfo{Status: model.TaskStatusFailure, Reason: "upstream failure"}
				if batch {
					if !terminal {
						info.Status = model.TaskStatusInProgress
					}
					adaptor := &batchPollingAdaptor{results: map[string]*BatchTaskResult{task.GetUpstreamTaskID(): {TaskInfo: *info}}}
					require.NoError(t, UpdateBatchTasks(context.Background(), adaptor, map[int][]string{903: {task.GetUpstreamTaskID()}}, map[string]*model.Task{task.GetUpstreamTaskID(): task}))
				} else {
					code := http.StatusOK
					if !terminal {
						code = http.StatusNotFound // local query failure is not task completion
					}
					adaptor := &scriptedPollingAdaptor{statusCode: code, parse: info}
					channel := &model.Channel{Id: 903, Type: constant.ChannelTypeKling, Key: "test"}
					require.NoError(t, updateVideoSingleTask(context.Background(), adaptor, channel, task.GetUpstreamTaskID(), map[string]*model.Task{task.GetUpstreamTaskID(): task}))
				}
				var stored model.Task
				require.NoError(t, model.DB.First(&stored, task.ID).Error)
				if terminal {
					require.NotNil(t, stored.PrivateData.RoutingTerminal)
					require.Equal(t, string(model.TaskStatusFailure), stored.PrivateData.RoutingTerminal.Status)
					require.Positive(t, stored.PrivateData.RoutingTerminal.ObservedAt)
				} else {
					require.Nil(t, stored.PrivateData.RoutingTerminal)
				}
			})
		}
	}
}
