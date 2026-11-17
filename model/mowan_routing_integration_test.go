package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These are independent connection pools in one process, not a multi-process or
// capacity test. Global-DB entry points run sequentially; only tx APIs compete.
func TestRoutingIntegrationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := filepath.Join(t.TempDir(), "routing-integration.db")
			if dialect != "sqlite" {
				env := "TEST_MYSQL_DSN"
				if dialect == "postgres" {
					env = "TEST_POSTGRES_DSN"
				}
				dsn = strings.TrimSpace(os.Getenv(env))
				if dsn == "" {
					t.Skip(env + " is not configured; real database verification blocked")
				}
				if os.Getenv("TEST_ROUTING_DB_FIXTURE") != "1" {
					t.Skip("TEST_ROUTING_DB_FIXTURE=1 must explicitly confirm a non-production, empty, rebuildable test database")
				}
			}
			open := func() *gorm.DB {
				var driver gorm.Dialector = sqlite.Open(dsn)
				if dialect == "mysql" {
					driver = mysql.Open(dsn)
				} else if dialect == "postgres" {
					driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
				}
				db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
				require.NoError(t, err, "open %s test fixture (DSN omitted)", dialect)
				pool, err := db.DB()
				require.NoError(t, err)
				pool.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				if dialect == "sqlite" {
					require.NoError(t, db.Exec("PRAGMA busy_timeout = 5000").Error)
				}
				return db
			}
			db, peer := open(), open()
			pool, err := db.DB()
			require.NoError(t, err)
			peerPool, err := peer.DB()
			require.NoError(t, err)
			require.NotSame(t, pool, peerPool)
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("actual %s version: %s; two independent pools, one process", dialect, version)

			// Refuse an occupied fixture before any migration or destructive cleanup.
			owned := []any{&User{}, &Token{}, &Task{}, &TokenRouting{}, &RoutingFundingMode{}, &RoutingFundingSession{}, &RoutingFundingOperation{}, &RoutingSubmission{}, &SubscriptionPreConsumeRecord{}, &RoutingMetricBucket{}, &RoutingMetricLease{}, &RoutingHealth{}, &RoutingHealthEvent{}, &RoutingTerminalObservation{}, &SubscriptionPlan{}, &UserSubscription{}}
			for _, table := range owned {
				require.False(t, db.Migrator().HasTable(table), "fixture must not contain %T; no tables were modified", table)
			}
			t.Cleanup(func() {
				for i := len(owned) - 1; i >= 0; i-- {
					require.NoError(t, db.Migrator().DropTable(owned[i]))
				}
			})
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
			})
			resetBatchUpdateTestState(t)

			migrate := func() {
				t.Helper()
				require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))
				require.NoError(t, MigrateTokenRouting(db))
				require.NoError(t, MigrateRoutingFunding(db))
				require.NoError(t, MigrateRoutingMetrics(db))
			}
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &Task{}))
			t.Run("migration_failure_blocks_mode", func(t *testing.T) {
				require.NoError(t, db.Exec("CREATE VIEW mowan_routing_funding_mode AS SELECT 1 AS id").Error)
				t.Cleanup(func() { require.NoError(t, db.Exec("DROP VIEW mowan_routing_funding_mode").Error) })
				err := MigrateRoutingFunding(db)
				require.Error(t, err, "an incompatible schema must not be ignored")
				t.Logf("actual %s migration rejected: %v", dialect, err)
				require.ErrorIs(t, InitializeRoutingFundingMode(db, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
				require.ErrorIs(t, ValidateRoutingFundingMode(peer, "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
			})
			t.Run("fresh_schema_and_repeat_start", func(t *testing.T) {
				for attempt := range 2 {
					migrate()
					if attempt == 0 {
						require.ErrorIs(t, InitializeRoutingFundingMode(db, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked, "empty schema is not deployment evidence")
						require.NoError(t, db.Create(&RoutingFundingMode{ID: 1, Mode: "sync_db_v1"}).Error)
						require.ErrorIs(t, InitializeRoutingFundingMode(db, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked, "a bare marker is not deployment evidence")
						require.ErrorIs(t, ValidateRoutingFundingMode(peer, "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked)
					}
					receipt := syntheticRoutingMigrationReceipt(t, db)
					data, err := common.Marshal(receipt)
					require.NoError(t, err)
					require.NoError(t, InitializeRoutingFundingMode(db, string(data), "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, receipt.NodeCount))
					for _, mutate := range []func(*RoutingFundingMigrationReceipt){
						func(r *RoutingFundingMigrationReceipt) { r.DeploymentID = "wrong-deployment" },
						func(r *RoutingFundingMigrationReceipt) { r.DatabaseVersion = "wrong-version" },
						func(r *RoutingFundingMigrationReceipt) { r.PoolMaxOpenConns++ },
					} {
						invalid := receipt
						mutate(&invalid)
						replacement, err := common.Marshal(invalid)
						require.NoError(t, err)
						require.ErrorIs(t, InitializeRoutingFundingMode(db, string(replacement), "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, receipt.NodeCount), ErrRoutingFundingMigrationBlocked)
						var stored RoutingFundingMode
						require.NoError(t, peer.First(&stored, 1).Error)
						require.Equal(t, string(data), stored.Evidence, "rejected replacement must retain the reviewed evidence")
					}
					require.NoError(t, InitializeRoutingFundingMode(db, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1))
					require.NoError(t, ValidateRoutingFundingMode(peer, "sync_db_v1", receipt.DeploymentID, receipt.CandidateCommit, receipt.NodeCount))
					for _, table := range owned {
						require.True(t, peer.Migrator().HasTable(table), "missing real schema for %T", table)
					}
				}
			})
			// Only extension tables created in this confirmed empty fixture are
			// removed, to exercise installation over a real pre-extension Token.
			for i := len(owned) - 1; i >= 3; i-- {
				require.NoError(t, db.Migrator().DropTable(owned[i]))
			}
			u := createReserveTestUser(t, 100)
			token := createReserveTestToken(t, 100)
			token.UserId, token.Group, token.AutoGroups, token.CrossGroupRetry = u.Id, "auto", `["legacy","paid"]`, true
			require.NoError(t, db.Save(&token).Error)
			for range 2 {
				migrate()
				row, err := GetTokenRouting(peer, token.Id)
				require.NoError(t, err)
				require.Nil(t, row, "migration must not opt an existing Key into smart routing")
				reloaded := getTokenFromDB(t, token.Id)
				require.Equal(t, token.Group, reloaded.Group)
				require.Equal(t, token.AutoGroups, reloaded.AutoGroups)
				require.True(t, reloaded.CrossGroupRetry)
			}
			require.ErrorIs(t, InitializeRoutingFundingMode(db, "", "sync_db_v1", "synthetic-test-only", "0000000000000000000000000000000000000000", 1), ErrRoutingFundingMigrationBlocked, "existing balances require reviewed migration evidence")
			t.Run("two_connection_configuration_cas", func(t *testing.T) {
				policy := smartrouting.DefaultPolicy()
				write := func(handle *gorm.DB, expected int64) error {
					copyToken := token
					return handle.Transaction(func(tx *gorm.DB) error {
						_, err := SaveTokenRoutingTx(tx, &copyToken, &policy, expected, nil, false, true, false)
						return err
					})
				}
				require.NoError(t, write(db, 0))
				start := make(chan struct{})
				errs := make(chan error, 2)
				var wg sync.WaitGroup
				for _, handle := range []*gorm.DB{db, peer} {
					wg.Add(1)
					go func(handle *gorm.DB) { defer wg.Done(); <-start; errs <- write(handle, 1) }(handle)
				}
				close(start)
				wg.Wait()
				close(errs)
				wins := 0
				for err := range errs {
					if err == nil {
						wins++
					} else if dialect == "sqlite" && (strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked")) {
						t.Logf("SQLite rejected competing writer: %v", err)
					} else {
						require.ErrorIs(t, err, ErrRoutingVersionConflict)
					}
				}
				require.Equal(t, 1, wins)
				require.ErrorIs(t, write(peer, 1), ErrRoutingVersionConflict)
				row, err := GetTokenRouting(peer, token.Id)
				require.NoError(t, err)
				require.EqualValues(t, 2, row.Version)
			})

			newFunding := func() (User, Token, RoutingFundingRequest, *RoutingSubmission) {
				t.Helper()
				u := createReserveTestUser(t, 100)
				key := createReserveTestToken(t, 100)
				key.UserId = u.Id
				require.NoError(t, DB.Model(&key).Update("user_id", u.Id).Error)
				req := RoutingFundingRequest{FundingID: "integration-" + common.GetRandomString(12), OperationID: "integration-op-" + common.GetRandomString(12), UserID: u.Id, TokenID: key.Id, Source: "wallet", Kind: "reserve", Quota: 30}
				return u, key, req, routingTestSubmission(req)
			}
			count := func(table any, query string, value any, expected int64) {
				t.Helper()
				var n int64
				require.NoError(t, peer.Model(table).Where(query, value).Count(&n).Error)
				require.Equal(t, expected, n)
			}
			t.Run("prepared_atomic_rollback_and_cross_connection_replay", func(t *testing.T) {
				u, key, req, s := newFunding()
				const hook = "routing_integration_prepared_failure"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == s.TableName() {
						tx.AddError(errors.New("injected prepared insert failure"))
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Create().Remove(hook) })
				_, err := PrepareRoutingFunding(req, s)
				require.ErrorContains(t, err, "injected prepared insert failure")
				require.NoError(t, db.Callback().Create().Remove(hook))
				require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 100, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 0)
				count(&RoutingFundingSession{}, "funding_id = ?", req.FundingID, 0)
				count(&RoutingSubmission{}, "funding_id = ?", req.FundingID, 0)
				_, err = PrepareRoutingFunding(req, s)
				require.NoError(t, err)
				var replay RoutingFundingOperation
				require.NoError(t, peer.Transaction(func(tx *gorm.DB) error { return prepareRoutingFundingTx(tx, req, s, &replay) }))
				require.True(t, replay.Replay)
				require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 70, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 1)
				duplicate := *s
				duplicate.SubmissionID += "-duplicate"
				require.Error(t, peer.Create(&duplicate).Error, "request/attempt must be unique across connections")
				count(&RoutingSubmission{}, "funding_id = ?", req.FundingID, 1)
			})
			t.Run("accepted_identity_local_recovery_and_actual_quota_delta", func(t *testing.T) {
				u, key, req, s := newFunding()
				_, err := PrepareRoutingFunding(req, s)
				require.NoError(t, err)
				s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
				require.NoError(t, err)
				recovery := &RoutingTaskRecovery{Task: Task{TaskID: "local-" + req.FundingID, UserId: u.Id, ChannelId: s.ChannelID, Group: s.BusinessGroup, Quota: 50, Status: TaskStatusSubmitted}}
				s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "provider-"+req.FundingID, recovery, s.SendOwnerID)
				require.NoError(t, err)
				const hook = "routing_integration_task_failure"
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "tasks" {
						tx.AddError(errors.New("injected local task failure"))
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Create().Remove(hook) })
				_, err = PersistAcceptedRoutingTask(s.SubmissionID)
				require.ErrorContains(t, err, "injected local task failure")
				require.NoError(t, db.Callback().Create().Remove(hook))
				var persisted RoutingSubmission
				require.NoError(t, peer.First(&persisted, "submission_id = ?", s.SubmissionID).Error)
				require.Equal(t, "accepted", persisted.State)
				require.Equal(t, s.UpstreamTaskID, persisted.UpstreamTaskID)
				require.NotEmpty(t, persisted.RecoveryJSON)
				require.Zero(t, persisted.LocalTaskID)
				// Simulate a different local owner using the second pool, sequentially.
				DB = peer
				defer func() { DB = db }()
				task, err := PersistAcceptedRoutingTask(s.SubmissionID)
				require.NoError(t, err)
				again, err := PersistAcceptedRoutingTask(s.SubmissionID)
				require.NoError(t, err)
				require.Equal(t, task.ID, again.ID)
				require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
				for range 2 {
					require.NoError(t, CommitAcceptedRoutingTaskFunding(s.SubmissionID, 50))
				}
				require.Equal(t, 50, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 50, getTokenFromDB(t, key.Id).RemainQuota)
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
				require.Equal(t, 80, getTokenFromDB(t, key.Id).RemainQuota)
				var storedTask Task
				require.NoError(t, db.First(&storedTask, task.ID).Error)
				require.Equal(t, 20, storedTask.Quota)
				count(&Task{}, "task_id = ?", task.TaskID, 1)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 3)
			})
			t.Run("unknown_holds_funds_and_reconciliation_is_atomic_idempotent", func(t *testing.T) {
				u, key, req, s := newFunding()
				_, err := PrepareRoutingFunding(req, s)
				require.NoError(t, err)
				s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
				require.NoError(t, err)
				require.NoError(t, db.Model(s).Update("send_lease_until", 0).Error)
				s, err = RecoverExpiredRoutingSubmission(context.Background(), s.SubmissionID, s.Version)
				require.NoError(t, err)
				require.Equal(t, "unknown", s.State)
				nextReq := req
				nextReq.OperationID += "-retry"
				next := routingTestSubmission(req)
				next.AttemptNo = 2
				_, err = PrepareRoutingFunding(nextReq, next)
				require.ErrorContains(t, err, "reservation retained")
				nextReq.Kind, nextReq.Quota = "refund", 0
				_, err = PrepareRoutingFunding(nextReq, nil)
				require.ErrorContains(t, err, "reservation retained")
				count(&RoutingSubmission{}, "funding_id = ?", req.FundingID, 1)
				resolution := RoutingSubmissionResolution{ExpectedVersion: s.Version, Actor: u.Id, Reason: "provider confirms no create", Outcome: "not_sent", Proof: RoutingSubmissionProof{RequestID: s.RequestID, ChannelID: s.ChannelID, ProviderKey: s.ProviderKey, OperationKey: s.OperationKey, Outcome: "not_sent", ObservedAt: common.GetTimestamp(), EvidenceRef: "integration-provider-audit", Confirmed: true}}
				const hook = "routing_integration_refund_failure"
				require.NoError(t, db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						tx.AddError(errors.New("injected reconcile key failure"))
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Update().Remove(hook) })
				_, err = ResolveRoutingSubmission(s.SubmissionID, resolution)
				require.ErrorContains(t, err, "injected reconcile key failure")
				require.NoError(t, db.Callback().Update().Remove(hook))
				var stored RoutingSubmission
				require.NoError(t, peer.First(&stored, "submission_id = ?", s.SubmissionID).Error)
				require.Equal(t, "unknown", stored.State)
				require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 70, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 1)
				for range 2 {
					_, err = ResolveRoutingSubmission(s.SubmissionID, resolution)
					require.NoError(t, err)
				}
				require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 100, getTokenFromDB(t, key.Id).RemainQuota)
				require.NoError(t, peer.First(&stored, "submission_id = ?", s.SubmissionID).Error)
				require.Equal(t, "not_sent", stored.State)
				require.Equal(t, resolution.Actor, stored.ReconcileActor)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 2)
			})
			t.Run("terminal_proof_blocks_local_failure_before_atomic_refund", func(t *testing.T) {
				u, key, req, s := newFunding()
				_, err := PrepareRoutingFunding(req, s)
				require.NoError(t, err)
				s, err = MarkRoutingSubmissionSending(s.SubmissionID, s.Version, s.SendOwnerID)
				require.NoError(t, err)
				recovery := &RoutingTaskRecovery{Task: Task{TaskID: "proof-" + req.FundingID, UserId: u.Id, ChannelId: s.ChannelID, Group: s.BusinessGroup, Quota: req.Quota, Status: TaskStatusSubmitted}}
				s, err = RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", "provider-"+req.FundingID, recovery, s.SendOwnerID)
				require.NoError(t, err)
				task, err := PersistAcceptedRoutingTask(s.SubmissionID)
				require.NoError(t, err)
				task.Status = TaskStatusFailure
				won, err := task.UpdateWithStatus(TaskStatusSubmitted)
				require.NoError(t, err)
				require.True(t, won)
				_, err = FinalizeTaskFunding(task, 0, true)
				require.ErrorIs(t, err, ErrRoutingTerminalUnconfirmed)
				require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 70, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 1)
				won, err = task.UpdateWithUpstreamStatus(context.Background(), TaskStatusFailure)
				require.NoError(t, err)
				require.True(t, won)
				const hook = "routing_integration_proven_refund_failure"
				require.NoError(t, db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						tx.AddError(errors.New("injected proven refund key failure"))
					}
				}))
				t.Cleanup(func() { _ = db.Callback().Update().Remove(hook) })
				_, err = FinalizeTaskFunding(task, 0, true)
				require.ErrorContains(t, err, "injected proven refund key failure")
				require.NoError(t, db.Callback().Update().Remove(hook))
				require.Equal(t, 70, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 70, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 1)
				for range 2 {
					_, err = FinalizeTaskFunding(task, 0, true)
					require.NoError(t, err)
				}
				require.Equal(t, 100, getUserQuotaFromDB(t, u.Id))
				require.Equal(t, 100, getTokenFromDB(t, key.Id).RemainQuota)
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 2)
			})
			t.Run("subscription_supplement_rollback_settlement_and_refund", func(t *testing.T) {
				u, key, req, _ := newFunding()
				require.NoError(t, db.Model(&key).Update("remain_quota", 200).Error)
				plan := SubscriptionPlan{Title: "routing integration", TotalAmount: 100, QuotaResetPeriod: SubscriptionResetNever}
				require.NoError(t, db.Create(&plan).Error)
				InvalidateSubscriptionPlanCache(plan.Id)
				t.Cleanup(func() { InvalidateSubscriptionPlanCache(plan.Id) })
				sub := UserSubscription{UserId: u.Id, PlanId: plan.Id, AmountTotal: 100, Status: "active", EndTime: common.GetTimestamp() + 3600}
				require.NoError(t, db.Create(&sub).Error)
				req.Source = "subscription"
				_, err := PrepareRoutingFunding(req, nil)
				require.NoError(t, err)
				check := func(used int64, tokenRemain int, reserved int64, status string) {
					t.Helper()
					var saved UserSubscription
					require.NoError(t, peer.First(&saved, sub.Id).Error)
					require.Equal(t, used, saved.AmountUsed)
					var record SubscriptionPreConsumeRecord
					require.NoError(t, peer.First(&record, "request_id = ?", req.FundingID).Error)
					require.Equal(t, reserved, record.PreConsumed)
					require.Equal(t, status, record.Status)
					require.Equal(t, tokenRemain, getTokenFromDB(t, key.Id).RemainQuota)
					require.Equal(t, 100, getUserQuotaFromDB(t, u.Id), "subscription cannot debit the wallet")
				}
				check(30, 170, 30, "consumed")
				req.OperationID += "-insufficient"
				req.Quota = 120 // Key can pay; the subscription cannot. Its failure must undo the Key debit.
				_, err = PrepareRoutingFunding(req, nil)
				require.ErrorContains(t, err, "subscription used exceeds total")
				check(30, 170, 30, "consumed")
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 1)
				var fund RoutingFundingSession
				require.NoError(t, peer.First(&fund, "funding_id = ?", req.FundingID).Error)
				require.Equal(t, 30, fund.Quota)
				req.OperationID += "-supplement"
				req.Quota = 80
				_, err = PrepareRoutingFunding(req, nil)
				require.NoError(t, err)
				check(80, 120, 80, "consumed")
				req.Kind, req.Quota, req.OperationID = "settle", 50, req.OperationID+"-settle"
				for range 2 {
					_, err = PrepareRoutingFunding(req, nil)
					require.NoError(t, err)
				}
				check(50, 150, 50, "settled")
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 3)
				req.FundingID += "-refundable"
				req.Kind, req.Quota, req.OperationID = "reserve", 20, req.OperationID+"-reserve"
				_, err = PrepareRoutingFunding(req, nil)
				require.NoError(t, err)
				check(70, 130, 20, "consumed")
				req.Kind, req.Quota, req.OperationID = "refund", 0, req.OperationID+"-refund"
				for range 2 {
					_, err = PrepareRoutingFunding(req, nil)
					require.NoError(t, err)
				}
				check(50, 150, 0, "refunded")
				count(&RoutingFundingOperation{}, "funding_id = ?", req.FundingID, 2)
			})
		})
	}
}
