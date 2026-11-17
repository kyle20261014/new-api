package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFundingUsesDBDespiteStaleRedisAndBatch(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 10).Error)
	ok, err := TryReserveUserQuota(user.Id, 20)
	require.NoError(t, err)
	require.False(t, ok, "stale Redis may not authorize spending")
	require.NoError(t, IncreaseUserQuota(user.Id, 5, false))
	require.Equal(t, 15, getUserQuotaFromDB(t, user.Id), "balance writes must be synchronous even with batch enabled")
}

func setupRoutingFunds(t *testing.T) (User, Token, RoutingFundingRequest) {
	t.Helper()
	truncateTables(t)
	resetBatchUpdateTestState(t)
	require.NoError(t, MigrateRoutingFunding(DB))
	require.NoError(t, MigrateRoutingMetrics(DB))
	require.NoError(t, DB.AutoMigrate(&Midjourney{}))
	t.Cleanup(func() {
		DB.Exec("DELETE FROM mowan_routing_submissions")
		DB.Exec("DELETE FROM mowan_routing_funding_operations")
		DB.Exec("DELETE FROM mowan_routing_funding_sessions")
		DB.Exec("DELETE FROM mowan_routing_funding_mode")
		DB.Exec("DELETE FROM subscription_pre_consume_records")
		DB.Exec("DELETE FROM midjourneys")
	})
	u := createReserveTestUser(t, 100)
	token := createReserveTestToken(t, 100)
	token.UserId = u.Id
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Update("user_id", u.Id).Error)
	return u, token, RoutingFundingRequest{FundingID: "fund-" + common.GetRandomString(8), OperationID: "op-" + common.GetRandomString(8), UserID: u.Id, TokenID: token.Id, Source: "wallet", Kind: "reserve", Quota: 30}
}

func routingTestSubmission(req RoutingFundingRequest) *RoutingSubmission {
	return &RoutingSubmission{SubmissionID: "sub-" + common.GetRandomString(8), RequestID: req.FundingID, AttemptNo: 1, ClientModel: "model", BusinessGroup: "paid", ChannelID: 1, ProtocolClass: "async", ProviderKey: "test:v1", OperationKey: "image.generate"}
}

func TestRoutingFundingAtomicRollbackAndReplay(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	const fail = "funding_fail_after_token"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected after key debit"))
		}
	}))
	_, err := PrepareRoutingFunding(req, routingTestSubmission(req))
	require.ErrorContains(t, err, "injected after key debit")
	require.NoError(t, DB.Callback().Update().Remove(fail))
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	var count int64
	require.NoError(t, DB.Model(&RoutingFundingOperation{}).Count(&count).Error)
	require.Zero(t, count)
	s := routingTestSubmission(req)
	_, err = PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	op, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	require.True(t, op.Replay)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	req.Quota++
	_, err = PrepareRoutingFunding(req, nil)
	require.ErrorIs(t, err, ErrRoutingFundingConflict)
}

func TestRoutingFundingFreeToPaidAndLegacyShareWallet(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	req.Quota = 0
	_, err := PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	var wg sync.WaitGroup
	var smartErr error
	var legacyOK bool
	var legacyErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		next := req
		next.OperationID += ":paid"
		next.Quota = 80
		_, smartErr = PrepareRoutingFunding(next, nil)
	}()
	go func() { defer wg.Done(); legacyOK, legacyErr = TryReserveUserQuota(u.Id, 80) }()
	wg.Wait()
	require.NoError(t, legacyErr)
	require.NotEqual(t, legacyOK, smartErr == nil, "only one caller may spend the shared wallet")
	require.Equal(t, 20, getUserQuotaFromDB(t, u.Id))
	if smartErr != nil {
		require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	}
}

func TestRoutingSubmissionUnknownRetainsReservation(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.NoError(t, err)
	_, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.ErrorIs(t, err, ErrRoutingSubmissionConflict)
	require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
	s, err = RecoverRoutingSubmissionSending(s.SubmissionID, s.Version)
	require.NoError(t, err)
	require.Equal(t, "unknown", s.State)
	req.Kind, req.Quota, req.OperationID = "refund", 0, req.OperationID+":refund"
	_, err = PrepareRoutingFunding(req, nil)
	require.ErrorContains(t, err, "reservation retained")
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingAcceptedSurvivesTaskInsertFailureAndTerminalReplay(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.NoError(t, err)
	recovery := &RoutingTaskRecovery{Task: Task{TaskID: "task-recovery", UserId: u.Id, ChannelId: 1, Group: "paid", Quota: 50, Status: TaskStatusSubmitted}}
	s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "upstream-1", recovery, s.SendOwnerID)
	require.NoError(t, err)
	const fail = "task_insert_failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			tx.AddError(errors.New("injected task insertion"))
		}
	}))
	_, err = PersistAcceptedRoutingTask(s.SubmissionID)
	require.ErrorContains(t, err, "injected task insertion")
	require.NoError(t, DB.Callback().Create().Remove(fail))
	stored, err := GetRoutingSubmission(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "accepted", stored.State)
	require.Zero(t, stored.LocalTaskID)
	task, err := PersistAcceptedRoutingTask(s.SubmissionID)
	require.NoError(t, err)
	again, err := PersistAcceptedRoutingTask(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, task.ID, again.ID)
	require.Equal(t, 50, task.Quota)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id), "accepted persistence alone cannot debit the actual difference")
	require.NoError(t, CommitAcceptedRoutingTaskFunding(s.SubmissionID, 50))
	require.Equal(t, 50, getUserQuotaFromDB(t, u.Id))
	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithUpstreamStatus(context.Background(), TaskStatusSubmitted)
	require.NoError(t, err)
	require.True(t, won)
	for range 2 {
		handled, err := FinalizeRoutingTaskFunding(task, 20, false)
		require.True(t, handled)
		require.NoError(t, err)
	}
	require.Equal(t, 80, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 80, getTokenFromDB(t, token.Id).RemainQuota)
	var persisted Task
	require.NoError(t, DB.First(&persisted, task.ID).Error)
	require.Equal(t, 20, persisted.Quota)
}

// Synthetic gate inputs only: these values are not production migration/capacity evidence.
func syntheticRoutingMigrationReceipt(t *testing.T, db *gorm.DB) RoutingFundingMigrationReceipt {
	t.Helper()
	query := "SELECT version()"
	if db.Dialector.Name() == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	pool, err := db.DB()
	require.NoError(t, err)
	return RoutingFundingMigrationReceipt{SchemaVersion: 1, ReviewedBy: "synthetic-test-only", ReviewedAt: common.GetTimestamp(), ParticipantInventoryRef: "test/inventory", PendingSnapshotsRef: "test/snapshots", BalanceReconciliationRef: "test/reconciliation", DBCommitEvidenceRef: "test/commits", BackupRef: "test/backup", PendingDrainProven: true, OldNodesIsolated: true, CapacityEvidenceRef: "test/capacity-not-a-benchmark", CapacityPassed: true, DeploymentID: "synthetic-test-only", HardwareRef: "test/hardware", DatabaseVersion: version, PoolMaxOpenConns: pool.Stats().MaxOpenConnections, NodeCount: 1, CandidateCommit: "0000000000000000000000000000000000000000", WorkloadRef: "test/workload", StableDurationSeconds: 1800, StableThroughputRPS: 100, ExpectedPeakRPS: 70}
}

func TestRoutingMigrationMarkerCannotBypassEvidence(t *testing.T) {
	for _, existingMarker := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_marker=%t", existingMarker), func(t *testing.T) {
			setupRoutingFunds(t)
			if existingMarker {
				require.NoError(t, DB.Create(&RoutingFundingMode{ID: 1, Mode: "sync_db_v1"}).Error)
			} else {
				require.NoError(t, DB.Exec("DELETE FROM tokens").Error)
				require.NoError(t, DB.Exec("DELETE FROM users").Error)
			}
			require.ErrorIs(t, InitializeRoutingFundingMode(DB, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked, "neither an empty database nor a mode marker proves deployment capacity or old-node isolation")
			require.ErrorIs(t, ValidateRoutingFundingMode(DB, "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
		})
	}
}

func TestRoutingMigrationSQLiteRejectsMatchingMultipleNodes(t *testing.T) {
	setupRoutingFunds(t)
	receipt := syntheticRoutingMigrationReceipt(t, DB)
	receipt.NodeCount = 2
	data, err := common.Marshal(receipt)
	require.NoError(t, err)
	require.ErrorIs(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 2), ErrRoutingFundingMigrationBlocked)
	require.NoError(t, DB.Create(&RoutingFundingMode{ID: 1, Mode: "sync_db_v1", Evidence: string(data)}).Error)
	require.ErrorIs(t, ValidateRoutingFundingMode(DB, "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 2), ErrRoutingFundingMigrationBlocked, "SQLite cannot authorize multiple writer nodes even when receipt and config agree")
}

func TestRoutingMigrationWrongReplacementPreservesReviewedEvidence(t *testing.T) {
	setupRoutingFunds(t)
	receipt := syntheticRoutingMigrationReceipt(t, DB)
	original, err := common.Marshal(receipt)
	require.NoError(t, err)
	require.NoError(t, InitializeRoutingFundingMode(DB, string(original), "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 1))
	receipt.DeploymentID = "wrong-deployment"
	replacement, err := common.Marshal(receipt)
	require.NoError(t, err)
	err = InitializeRoutingFundingMode(DB, string(replacement), "sync_db_v1", "synthetic-test-only", receipt.CandidateCommit, 1)
	assert.ErrorIs(t, err, ErrRoutingFundingMigrationBlocked)
	var stored RoutingFundingMode
	require.NoError(t, DB.First(&stored, 1).Error)
	require.Equal(t, string(original), stored.Evidence, "rejected replacement may not poison other nodes' restart evidence")
}

func TestRoutingMigrationReceiptAndModeGuard(t *testing.T) {
	setupRoutingFunds(t)
	for _, invalid := range []string{"", "approved", `{}`, `{"schema_version":1,"old_nodes_isolated":true}`} {
		require.ErrorIs(t, InitializeRoutingFundingMode(DB, invalid, "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
	}
	receipt := syntheticRoutingMigrationReceipt(t, DB)
	for _, mutate := range []func(*RoutingFundingMigrationReceipt){
		func(r *RoutingFundingMigrationReceipt) { r.PendingDrainProven = false },
		func(r *RoutingFundingMigrationReceipt) { r.OldNodesIsolated = false },
		func(r *RoutingFundingMigrationReceipt) { r.CapacityPassed = false },
		func(r *RoutingFundingMigrationReceipt) { r.CapacityEvidenceRef = "" },
		func(r *RoutingFundingMigrationReceipt) { r.StableDurationSeconds = 1799 },
		func(r *RoutingFundingMigrationReceipt) { r.StableThroughputRPS = 0 },
		func(r *RoutingFundingMigrationReceipt) { r.ExpectedPeakRPS = 70.01 },
		func(r *RoutingFundingMigrationReceipt) { r.DeploymentID = "" },
	} {
		invalid := receipt
		mutate(&invalid)
		data, err := common.Marshal(invalid)
		require.NoError(t, err)
		require.ErrorIs(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
	}
	data, err := common.Marshal(receipt)
	require.NoError(t, err)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUserQuota][1] = 1
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	require.ErrorIs(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	delete(batchUpdateStores[BatchUpdateTypeUserQuota], 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	require.NoError(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1))
	require.NoError(t, InitializeRoutingFundingMode(DB, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1))
	require.NoError(t, ValidateRoutingFundingMode(DB, "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, receipt.NodeCount))
	for _, binding := range []struct {
		mode, deployment, commit string
		nodes                    int
	}{
		{"redis", receipt.DeploymentID, receipt.CandidateCommit, 1},
		{"sync_db_v1", "", receipt.CandidateCommit, 1},
		{"sync_db_v1", "other-environment", receipt.CandidateCommit, 1},
		{"sync_db_v1", receipt.DeploymentID, "other-commit", 1},
		{"sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 2},
	} {
		require.ErrorIs(t, ValidateRoutingFundingMode(DB, binding.mode, binding.deployment, binding.commit, binding.nodes), ErrRoutingFundingMigrationBlocked)
	}
	for _, mutate := range []func(*RoutingFundingMigrationReceipt){
		func(r *RoutingFundingMigrationReceipt) { r.OldNodesIsolated = false },
		func(r *RoutingFundingMigrationReceipt) { r.CapacityEvidenceRef = "" },
		func(r *RoutingFundingMigrationReceipt) { r.DatabaseVersion = "different" },
		func(r *RoutingFundingMigrationReceipt) { r.PoolMaxOpenConns++ },
	} {
		invalid := receipt
		mutate(&invalid)
		data, err := common.Marshal(invalid)
		require.NoError(t, err)
		require.NoError(t, DB.Model(&RoutingFundingMode{}).Where("id = ?", 1).Update("evidence", string(data)).Error)
		require.ErrorIs(t, ValidateRoutingFundingMode(DB, "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 1), ErrRoutingFundingMigrationBlocked)
	}
	// An explicitly supplied, reviewed replacement can repair outdated evidence.
	require.NoError(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1))
	require.NoError(t, ValidateRoutingFundingMode(DB, "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, 1))
	require.NoError(t, DB.Model(&RoutingFundingMode{}).Where("id = ?", 1).Update("mode", "redis").Error)
	require.ErrorIs(t, InitializeRoutingFundingMode(DB, string(data), "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
}

func TestRoutingMigrationFailedBatchFinishedAndEmptyMapDoNotProveDrain(t *testing.T) {
	_, token, _ := setupRoutingFunds(t)
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previous := gin.DefaultWriter
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previous
		common.LogWriterMu.Unlock()
	})
	const failure = "a21_legacy_batch_failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(failure, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected legacy batch write failure"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(failure) })
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -10)
	batchUpdate()
	require.Contains(t, output.String(), "injected legacy batch write failure")
	require.Contains(t, output.String(), "batch update finished")
	require.Empty(t, batchUpdateStores[BatchUpdateTypeTokenQuota])
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	require.ErrorIs(t, InitializeRoutingFundingMode(DB, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
	var markers int64
	require.NoError(t, DB.Model(&RoutingFundingMode{}).Count(&markers).Error)
	require.Zero(t, markers)
	t.Log(output.String())
}

func TestRoutingReconciliationRefundAtomicAndIdempotent(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
	s, err = RecoverRoutingSubmissionSending(s.SubmissionID, s.Version)
	require.NoError(t, err)
	resolution := RoutingSubmissionResolution{ExpectedVersion: s.Version, Actor: 1, Reason: "provider confirmed no task", Outcome: "not_sent", Proof: RoutingSubmissionProof{RequestID: s.RequestID, ChannelID: s.ChannelID, ProviderKey: s.ProviderKey, OperationKey: s.OperationKey, Outcome: "not_sent", ObservedAt: common.GetTimestamp(), EvidenceRef: "provider-audit/1", Confirmed: true}}
	invalid := resolution
	invalid.Proof.ProviderKey = "different"
	_, err = ResolveRoutingSubmission(s.SubmissionID, invalid)
	require.ErrorContains(t, err, "proof identity mismatch")
	const fail = "refund_token_failure"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected refund failure"))
		}
	}))
	_, err = ResolveRoutingSubmission(s.SubmissionID, resolution)
	require.ErrorContains(t, err, "injected refund failure")
	require.NoError(t, DB.Callback().Update().Remove(fail))
	stored, err := GetRoutingSubmission(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "unknown", stored.State)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	for range 2 {
		stored, err = ResolveRoutingSubmission(s.SubmissionID, resolution)
		require.NoError(t, err)
	}
	require.Equal(t, "not_sent", stored.State)
	require.Equal(t, 1, stored.ReconcileActor)
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingSubmissionOwnerLeaseFencesRecoveryAndLateAccepted(t *testing.T) {
	u, _, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	require.NotEmpty(t, s.SendOwnerID)
	_, err = RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
	require.ErrorIs(t, err, ErrRoutingSubmissionConflict)
	_, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, "wrong-owner")
	require.ErrorIs(t, err, ErrRoutingSubmissionConflict)
	_, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version)
	require.ErrorIs(t, err, ErrRoutingSubmissionConflict, "missing owner cannot be inferred from DB")
	renewed, err := RenewRoutingSubmissionSendLease(context.Background(), s.SubmissionID, s.SendOwnerID)
	require.NoError(t, err)
	require.True(t, renewed)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.NoError(t, err)
	_, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "unknown", "", nil)
	require.ErrorIs(t, err, ErrRoutingSubmissionConflict, "missing owner cannot change a durable send outcome")
	oldVersion := s.Version
	require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
	renewed, err = RenewRoutingSubmissionSendLease(context.Background(), s.SubmissionID, s.SendOwnerID)
	require.NoError(t, err)
	require.False(t, renewed, "expired owner may not revive")
	unknown, err := RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
	require.NoError(t, err)
	require.Equal(t, "unknown", unknown.State)
	recovery := &RoutingTaskRecovery{Task: Task{TaskID: "late-task", UserId: u.Id, ChannelId: 1, Group: "paid", Quota: 30}}
	accepted, err := RecordRoutingSubmissionOutcome(s.SubmissionID, oldVersion, "accepted", "late-upstream", recovery, s.SendOwnerID)
	require.NoError(t, err)
	require.Equal(t, "accepted", accepted.State)
	require.Equal(t, unknown.Version+1, accepted.Version)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
}

func TestRoutingExpiredPreparedRefundAtomicAndCannotSend(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
	for range 2 {
		released, err := RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
		require.NoError(t, err)
		require.Equal(t, "not_sent", released.State)
	}
	_, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.Error(t, err)
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingFundingSubscriptionReserveSettleAndRefundShareTransaction(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	plan := SubscriptionPlan{Title: "routing funds", TotalAmount: 100, QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, DB.Create(&plan).Error)
	sub := UserSubscription{UserId: u.Id, PlanId: plan.Id, AmountTotal: 100, Status: "active", EndTime: common.GetTimestamp() + 3600}
	require.NoError(t, DB.Create(&sub).Error)
	req.Source = "subscription"
	_, err := PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.EqualValues(t, 30, getSubscriptionResetSub(t, sub.Id).AmountUsed)
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	req.OperationID += "-extra"
	req.Quota = 80
	_, err = PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.EqualValues(t, 80, getSubscriptionResetSub(t, sub.Id).AmountUsed)
	require.Equal(t, 20, getTokenFromDB(t, token.Id).RemainQuota)
	req.OperationID += "-settle"
	req.Kind, req.Quota = "settle", 50
	const fail = "subscription_funding_failure"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			tx.AddError(errors.New("injected subscription update"))
		}
	}))
	_, err = PrepareRoutingFunding(req, nil)
	require.ErrorContains(t, err, "injected subscription update")
	require.NoError(t, DB.Callback().Update().Remove(fail))
	require.EqualValues(t, 80, getSubscriptionResetSub(t, sub.Id).AmountUsed)
	require.Equal(t, 20, getTokenFromDB(t, token.Id).RemainQuota)
	for range 2 {
		_, err = PrepareRoutingFunding(req, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 50, getSubscriptionResetSub(t, sub.Id).AmountUsed)
	require.Equal(t, 50, getTokenFromDB(t, token.Id).RemainQuota)
	req.FundingID += "-refund"
	req.OperationID += "-reserve"
	req.Kind, req.Quota = "reserve", 20
	_, err = PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	req.OperationID += "-refund"
	req.Kind, req.Quota = "refund", 0
	_, err = PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.NoError(t, RefundSubscriptionPreConsume(req.FundingID))
	require.EqualValues(t, 50, getSubscriptionResetSub(t, sub.Id).AmountUsed)
	require.Equal(t, 50, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingPreparedProbeFailureRollsBackSupplement(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.NoError(t, err)
	_, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "rejected", "", nil, s.SendOwnerID)
	require.NoError(t, err)
	next := routingTestSubmission(req)
	next.AttemptNo = 2
	next.TerminalProbeEpoch = 1
	next.TerminalProbeLeaseID = "unowned-probe"
	req.OperationID += "-extra"
	req.Quota = 80
	_, err = PrepareRoutingFunding(req, next)
	require.Error(t, err)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	var count int64
	require.NoError(t, DB.Model(&RoutingFundingOperation{}).Where("operation_id = ?", req.OperationID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", next.SubmissionID).Count(&count).Error)
	require.Zero(t, count)
}

func TestRoutingNegativeOutcomeCrashBeforeRefundRecoversAfterOwnerLoss(t *testing.T) {
	for _, state := range []string{"not_sent", "rejected"} {
		t.Run(state, func(t *testing.T) {
			u, token, req := setupRoutingFunds(t)
			s := routingTestSubmission(req)
			_, err := PrepareRoutingFunding(req, s)
			require.NoError(t, err)
			if state == "rejected" {
				s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
				require.NoError(t, err)
			}
			s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, state, "", nil, s.SendOwnerID)
			require.NoError(t, err)
			_, err = RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
			require.ErrorIs(t, err, ErrRoutingSubmissionConflict)
			require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
			require.NoError(t, DB.Model(&RoutingSubmission{}).Where("submission_id = ?", s.SubmissionID).Update("send_lease_until", 0).Error)
			for range 2 {
				released, err := RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
				require.NoError(t, err)
				require.Equal(t, state, released.State)
			}
			require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
			require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
			var count int64
			require.NoError(t, DB.Model(&RoutingFundingOperation{}).Where("funding_id = ? AND kind = ?", req.FundingID, "refund").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestRoutingAcceptedRecoveryRestoresExactCredentialOnly(t *testing.T) {
	u, _, req := setupRoutingFunds(t)
	channel := Channel{Key: "first-key\noriginal-middle-key\nlast-key", ChannelInfo: ChannelInfo{IsMultiKey: true}}
	require.NoError(t, DB.Create(&channel).Error)
	s := routingTestSubmission(req)
	s.ChannelID = channel.Id
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.NoError(t, err)
	recovery := &RoutingTaskRecovery{Task: Task{TaskID: "fingerprint-task", UserId: u.Id, ChannelId: channel.Id, Group: "paid", Quota: 30}, CredentialFingerprint: RoutingCredentialFingerprint("original-middle-key")}
	s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "upstream-middle-key", recovery, s.SendOwnerID)
	require.NoError(t, err)
	require.NotContains(t, s.RecoveryJSON, "original-middle-key")
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("key", "other-key").Error)
	_, err = PersistAcceptedRoutingTask(s.SubmissionID)
	require.ErrorIs(t, err, ErrRoutingRecoveryCredentialUnavailable)
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("key", "last-key\noriginal-middle-key\nfirst-key").Error)
	const fail = "fingerprint_task_insert_failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			tx.AddError(errors.New("task insert unavailable"))
		}
	}))
	_, err = PersistAcceptedRoutingTask(s.SubmissionID)
	require.ErrorContains(t, err, "task insert unavailable")
	require.NoError(t, DB.Callback().Create().Remove(fail))
	local, err := PersistAcceptedRoutingTask(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "original-middle-key", local.PrivateData.Key)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("key", "replacement-key").Error)
	local, err = PersistAcceptedRoutingTask(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, "original-middle-key", local.PrivateData.Key)
}

func TestRoutingTerminalFundingMayWinBeforeAcceptedAdjustment(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
	require.NoError(t, err)
	recovery := &RoutingTaskRecovery{Task: Task{TaskID: "fast-terminal", UserId: u.Id, ChannelId: 1, Group: "paid", Quota: 50, Status: TaskStatusSuccess}}
	s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "fast-upstream", recovery, s.SendOwnerID)
	require.NoError(t, err)
	task, err := PersistAcceptedRoutingTask(s.SubmissionID)
	require.NoError(t, err)
	_, err = FinalizeTaskFunding(task, 20, false)
	require.NoError(t, err)
	require.NoError(t, CommitAcceptedRoutingTaskFunding(s.SubmissionID, 50))
	require.Equal(t, 80, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 80, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingFundingRefundKeepsOriginalDeletedKeyAccounting(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	_, err := PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&Token{}, token.Id).Error)
	req.Kind, req.Quota, req.OperationID = "refund", 0, "refund-deleted-original"
	_, err = PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	var stored Token
	require.NoError(t, DB.Unscoped().First(&stored, token.Id).Error)
	require.Equal(t, 100, stored.RemainQuota)
	require.Zero(t, stored.UsedQuota)
	require.True(t, stored.DeletedAt.Valid)
}

func TestRoutingFundingDeltaAtomicRollbackAndEventIdentity(t *testing.T) {
	u, token, _ := setupRoutingFunds(t)
	req := RoutingFundingDeltaRequest{OperationID: "frame:one", UserID: u.Id, TokenID: token.Id, Source: "wallet", Delta: 30, RequireAvailable: true}
	const fail = "delta_after_token_failure"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("injected delta key failure"))
		}
	}))
	for _, delta := range []int{30, -30} {
		req.Delta = delta
		_, err := ApplyRoutingFundingDeltaContext(context.Background(), req)
		require.ErrorContains(t, err, "injected delta key failure")
		require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
		require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	}
	require.NoError(t, DB.Callback().Update().Remove(fail))
	req.Delta = 30
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, err := ApplyRoutingFundingDeltaContext(context.Background(), req); assert.NoError(t, err) })
	}
	wg.Wait()
	require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	req.OperationID = "frame:two"
	_, err := ApplyRoutingFundingDeltaContext(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, 40, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 40, getTokenFromDB(t, token.Id).RemainQuota)
	req.OperationID = "refund:one"
	req.Delta = -20
	_, err = ApplyRoutingFundingDeltaContext(context.Background(), req)
	require.NoError(t, err)
	req.Delta = -21
	_, err = ApplyRoutingFundingDeltaContext(context.Background(), req)
	require.ErrorIs(t, err, ErrRoutingFundingConflict)
	require.Equal(t, 60, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 60, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingMidjourneyRecoveryIsRealModelAndRefundsOnce(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	s := routingTestSubmission(req)
	_, err := PrepareRoutingFunding(req, s)
	require.NoError(t, err)
	s, err = MarkRoutingSubmissionSending(s.SubmissionID, 1, s.SendOwnerID)
	require.NoError(t, err)
	recovery := &RoutingTaskRecovery{Midjourney: &Midjourney{MjId: "mj-upstream", UserId: u.Id, ChannelId: 1, Quota: 30, Action: "IMAGINE", Progress: "0%"}}
	s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "mj-upstream", recovery, s.SendOwnerID)
	require.NoError(t, err)
	const fail = "mj_insert_failure"
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(fail, func(tx *gorm.DB) {
		if tx.Statement.Table == "midjourneys" {
			tx.AddError(errors.New("injected MJ save"))
		}
	}))
	_, err = PersistAcceptedRoutingMidjourney(s.SubmissionID)
	require.ErrorContains(t, err, "injected MJ save")
	require.NoError(t, DB.Callback().Create().Remove(fail))
	task, err := PersistAcceptedRoutingMidjourney(s.SubmissionID)
	require.NoError(t, err)
	again, err := PersistAcceptedRoutingMidjourney(s.SubmissionID)
	require.NoError(t, err)
	require.Equal(t, task.Id, again.Id)
	var fakeTaskCount int64
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", "mj-upstream").Count(&fakeTaskCount).Error)
	require.Zero(t, fakeTaskCount)
	group, err := RoutingSubmissionOriginGroup(u.Id, 1, "mj-upstream")
	require.NoError(t, err)
	require.Equal(t, "paid", group)
	require.NoError(t, CommitAcceptedRoutingTaskFunding(s.SubmissionID, 30))
	previousStatus := task.Status
	task.Status = TaskStatusFailure
	won, err := task.UpdateWithUpstreamStatus(context.Background(), previousStatus)
	require.NoError(t, err)
	require.True(t, won)
	for range 2 {
		_, err = FinalizeMidjourneyFunding(task, true)
		require.NoError(t, err)
	}
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	var stored Midjourney
	require.NoError(t, DB.First(&stored, task.Id).Error)
	require.Zero(t, stored.Quota)
}

func TestRoutingTerminalFundingRejectsUnconfirmedStoredState(t *testing.T) {
	for _, kind := range []string{"task", "midjourney"} {
		t.Run(kind, func(t *testing.T) {
			u, token, req := setupRoutingFunds(t)
			s := routingTestSubmission(req)
			_, err := PrepareRoutingFunding(req, s)
			require.NoError(t, err)
			s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
			require.NoError(t, err)
			recovery := &RoutingTaskRecovery{Task: Task{TaskID: "proof-task", UserId: u.Id, ChannelId: 1, Group: "paid", Quota: 30, Status: TaskStatusInProgress}}
			if kind == "midjourney" {
				recovery = &RoutingTaskRecovery{Midjourney: &Midjourney{MjId: "proof-upstream", UserId: u.Id, ChannelId: 1, Quota: 30, Status: TaskStatusInProgress}}
			}
			s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "proof-upstream", recovery, s.SendOwnerID)
			require.NoError(t, err)
			forged := &RoutingTaskTerminalReceipt{Status: TaskStatusFailure, ObservedAt: 1}
			if kind == "midjourney" {
				task, err := PersistAcceptedRoutingMidjourney(s.SubmissionID)
				require.NoError(t, err)
				task.Status, task.RoutingTerminal = TaskStatusFailure, forged
				_, err = FinalizeMidjourneyFunding(task, true)
				require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
				task.RoutingTerminal = nil
				won, err := task.UpdateWithStatus(TaskStatusInProgress)
				require.NoError(t, err)
				require.True(t, won)
				_, err = FinalizeMidjourneyFunding(task, true)
				require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
			} else {
				task, err := PersistAcceptedRoutingTask(s.SubmissionID)
				require.NoError(t, err)
				task.Status, task.PrivateData.RoutingTerminal = TaskStatusFailure, forged
				_, err = FinalizeTaskFunding(task, 0, true)
				require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
				task.PrivateData.RoutingTerminal = nil
				won, err := task.UpdateWithStatus(TaskStatusInProgress)
				require.NoError(t, err)
				require.True(t, won)
				_, err = FinalizeTaskFunding(task, 0, true)
				require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
			}
			require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
			require.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
		})
	}
}

func TestRoutingLegacyDurableFundingRefundsStoredLocalFailure(t *testing.T) {
	u, token, req := setupRoutingFunds(t)
	task := Task{TaskID: "legacy-still-running", UserId: u.Id, ChannelId: 1, Quota: 30, Status: TaskStatusInProgress, PrivateData: TaskPrivateData{TokenId: token.Id, UpstreamTaskID: "legacy-provider-running"}}
	require.NoError(t, DB.Create(&task).Error)
	req.FundingID = fmt.Sprintf("task:%d", task.ID)
	_, err := PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	task.Status = TaskStatusFailure
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	_, err = FinalizeTaskFunding(&task, 0, true)
	require.NoError(t, err)
	op, err := FinalizeTaskFunding(&task, 0, true)
	require.NoError(t, err)
	require.True(t, op.Replay)
	require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingLegacyMidjourneyFundingRefundsStoredLocalFailure(t *testing.T) {
	u, token, _ := setupRoutingFunds(t)
	task := Midjourney{MjId: "legacy-mj", UserId: u.Id, TokenId: token.Id, ChannelId: 1, Quota: 30, Status: TaskStatusInProgress}
	require.NoError(t, DB.Create(&task).Error)
	task.Status = TaskStatusFailure
	_, err := FinalizeMidjourneyFunding(&task, true)
	require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed, "caller-only failure cannot authorize a refund")
	won, err := task.UpdateWithStatus(TaskStatusInProgress)
	require.NoError(t, err)
	require.True(t, won)
	for range 2 {
		_, err = FinalizeMidjourneyFunding(&task, true)
		require.NoError(t, err)
	}
	require.Equal(t, 130, getUserQuotaFromDB(t, u.Id))
	require.Equal(t, 130, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRoutingDurableTasksCannotStarveLegacyTimeoutCandidates(t *testing.T) {
	u, _, req := setupRoutingFunds(t)
	for i := range 100 {
		task := Task{TaskID: fmt.Sprintf("long-smart-%d", i), UserId: u.Id, Status: TaskStatusInProgress, Progress: "50%", SubmitTime: time.Now().Add(-80 * time.Hour).Unix()}
		require.NoError(t, DB.Create(&task).Error)
		kind := "task"
		if i%2 == 0 {
			kind = ""
		}
		require.NoError(t, DB.Create(&RoutingSubmission{SubmissionID: task.TaskID, RequestID: task.TaskID, AttemptNo: 1, UserID: u.Id, LocalTaskID: task.ID, LocalTaskKind: kind, State: "accepted"}).Error)
	}
	legacy := Task{TaskID: "legacy-timeout-candidate", UserId: u.Id, Status: TaskStatusInProgress, Progress: "50%", SubmitTime: time.Now().Add(-74 * time.Hour).Unix()}
	require.NoError(t, DB.Create(&legacy).Error)
	req.FundingID = fmt.Sprintf("task:%d", legacy.ID)
	_, err := PrepareRoutingFunding(req, nil)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&RoutingSubmission{SubmissionID: "other-kind", RequestID: "other-kind", AttemptNo: 1, UserID: u.Id, LocalTaskID: legacy.ID, LocalTaskKind: "midjourney", State: "accepted"}).Error)
	require.NoError(t, DB.Create(&RoutingSubmission{SubmissionID: "other-user", RequestID: "other-user", AttemptNo: 1, UserID: u.Id + 1, LocalTaskID: legacy.ID, LocalTaskKind: "task", State: "accepted"}).Error)
	selected := GetTimedOutUnfinishedTasks(time.Now().Add(-72*time.Hour).Unix(), 100)
	require.Len(t, selected, 1)
	require.Equal(t, legacy.ID, selected[0].ID)
}

func TestRoutingJournaledNonterminalProgressRemainsPollable(t *testing.T) {
	u, _, _ := setupRoutingFunds(t)
	task := Task{TaskID: "progress-journal", UserId: u.Id, Status: TaskStatusInProgress, Progress: "100%"}
	legacy := Task{TaskID: "progress-legacy", UserId: u.Id, Status: TaskStatusInProgress, Progress: "100%"}
	require.NoError(t, DB.Create(&task).Error)
	require.NoError(t, DB.Create(&legacy).Error)
	require.NoError(t, DB.Create(&RoutingSubmission{SubmissionID: "progress-task", RequestID: "progress-task", AttemptNo: 1, UserID: u.Id, LocalTaskID: task.ID, LocalTaskKind: "task", State: "accepted"}).Error)
	pending := GetAllUnFinishSyncTasks(100)
	require.Len(t, pending, 1)
	require.Equal(t, task.ID, pending[0].ID)
	require.True(t, HasUnfinishedSyncTasks())
	require.NoError(t, DB.Model(&task).Update("status", TaskStatusSuccess).Error)
	require.Empty(t, GetAllUnFinishSyncTasks(100))
	require.False(t, HasUnfinishedSyncTasks())
	mj := Midjourney{MjId: "progress-journal", UserId: u.Id, Status: TaskStatusInProgress, Progress: "100%"}
	legacyMJ := Midjourney{MjId: "progress-legacy", UserId: u.Id, Status: TaskStatusInProgress, Progress: "100%"}
	require.NoError(t, DB.Create(&mj).Error)
	require.NoError(t, DB.Create(&legacyMJ).Error)
	require.NoError(t, DB.Create(&RoutingSubmission{SubmissionID: "progress-mj", RequestID: "progress-mj", AttemptNo: 1, UserID: u.Id, LocalTaskID: int64(mj.Id), LocalTaskKind: "midjourney", State: "accepted"}).Error)
	pendingMJ := GetAllUnFinishTasks()
	require.Len(t, pendingMJ, 1)
	require.Equal(t, mj.Id, pendingMJ[0].Id)
	require.True(t, HasUnfinishedMidjourneyTasks())
	require.NoError(t, DB.Model(&mj).Update("status", TaskStatusFailure).Error)
	require.Empty(t, GetAllUnFinishTasks())
	require.False(t, HasUnfinishedMidjourneyTasks())
}
