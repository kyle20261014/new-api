package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func diagnosticStoreTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "diagnostic.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; require.NoError(t, sql.Close()) })
	require.NoError(t, MigrateModelDiagnostics(db))
	require.NoError(t, MigrateModelDiagnostics(db))
	return db
}

func diagnosticRunFixture(id, key string, user int) (*DiagnosticRun, []DiagnosticStep) {
	scope := "shared-test-scope"
	run := &DiagnosticRun{ID: id, UserID: user, SubmissionKey: key, ModelName: "chat-test", GroupName: "default", Kind: "drawing", ScopeKey: scope, ActiveKey: &scope, State: "queued", PlanJSON: "{}"}
	stepID := id + "-step"
	return run, []DiagnosticStep{{ID: stepID, RunID: id, State: "queued", RequestID: stepID, FundingID: "billing:" + stepID}}
}

func TestDiagnosticSubmissionAliasesSurviveSharedCompletion(t *testing.T) {
	db := diagnosticStoreTest(t)
	a, steps := diagnosticRunFixture("original", "first-key", 1)
	first, err := CreateDiagnosticRun(a, steps)
	require.NoError(t, err)
	b, more := diagnosticRunFixture("not-created", "second-key", 2)
	joined, err := CreateDiagnosticRun(b, more)
	require.NoError(t, err)
	require.Equal(t, first.ID, joined.ID)
	var count int64
	require.NoError(t, db.Model(&DiagnosticStep{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	claimed, err := ClaimDiagnosticRun(first.ID, "owner", time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "running", claimed.State)
	require.NoError(t, FinishDiagnosticRun(first.ID, "owner", "completed", "{}"))
	// The joiner's same key still resolves to the original report after ActiveKey clears.
	again, err := CreateDiagnosticRun(b, more)
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	b.Kind = "intelligence"
	_, err = CreateDiagnosticRun(b, more)
	require.ErrorIs(t, err, ErrDiagnosticSubmissionConflict)
}

func TestDiagnosticLeaseRecoveryNeverResendsUnknownSentStep(t *testing.T) {
	db := diagnosticStoreTest(t)
	run, steps := diagnosticRunFixture("restart", "restart-key", 1)
	_, err := CreateDiagnosticRun(run, steps)
	require.NoError(t, err)
	claimed, err := ClaimDiagnosticRun(run.ID, "first-owner", time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, UpdateDiagnosticStep(run.ID, "first-owner", steps[0].ID, map[string]any{"state": "prepared"}))
	// Expired preflight work is safe to reclaim, with its original funding identity.
	require.NoError(t, db.Model(&DiagnosticRun{}).Where("id = ?", run.ID).Update("lease_until", 1).Error)
	claimed, err = ClaimDiagnosticRun(run.ID, "second-owner", time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "running", claimed.State)
	require.Error(t, UpdateDiagnosticStep(run.ID, "first-owner", steps[0].ID, map[string]any{"state": "sending"}))
	require.NoError(t, UpdateDiagnosticStep(run.ID, "second-owner", steps[0].ID, map[string]any{"state": "sending"}))
	require.NoError(t, db.Model(&DiagnosticRun{}).Where("id = ?", run.ID).Update("lease_until", 1).Error)
	claimed, err = ClaimDiagnosticRun(run.ID, "third-owner", time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "review_pending", claimed.State)
	require.NotNil(t, claimed.ActiveKey)
	saved, err := GetDiagnosticSteps(run.ID)
	require.NoError(t, err)
	require.Equal(t, "review_pending", saved[0].State)
	require.Equal(t, "billing:"+steps[0].ID, saved[0].FundingID)
	require.False(t, RenewDiagnosticLease(run.ID, "second-owner"))
}

func TestDiagnosticRealDatabaseMigration(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			variable := "TEST_MYSQL_DSN"
			if dialect == "postgres" {
				variable = "TEST_POSTGRES_DSN"
			}
			dsn := os.Getenv(variable)
			if dsn == "" {
				t.Skip("real database DSN not configured; not run")
			}
			var driver gorm.Dialector = mysql.Open(dsn)
			if dialect == "postgres" {
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sql, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sql.Close()) })
			for _, table := range []any{&DiagnosticRun{}, &DiagnosticStep{}, &DiagnosticSubmission{}} {
				if db.Migrator().HasTable(table) {
					t.Skip("refusing to modify an existing diagnostics database")
				}
			}
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&DiagnosticSubmission{}, &DiagnosticStep{}, &DiagnosticRun{}))
			})
			require.NoError(t, MigrateModelDiagnostics(db))
			require.NoError(t, MigrateModelDiagnostics(db))
			run, steps := diagnosticRunFixture("external-migration", "external-key", 1)
			require.NoError(t, db.Create(run).Error)
			require.NoError(t, db.Create(&steps).Error)
			require.Error(t, db.Create(&DiagnosticStep{ID: "duplicate", RequestID: steps[0].RequestID}).Error, "the persisted request identity must remain unique")
		})
	}
}

func TestDiagnosticFundingLeaseRecoveryIsNotReplayable(t *testing.T) {
	db := diagnosticStoreTest(t)
	run, steps := diagnosticRunFixture("funding-restart", "funding-key", 1)
	_, err := CreateDiagnosticRun(run, steps)
	require.NoError(t, err)
	_, err = ClaimDiagnosticRun(run.ID, "before-crash", time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, UpdateDiagnosticStep(run.ID, "before-crash", steps[0].ID, map[string]any{"state": "funding"}))
	require.NoError(t, db.Model(&DiagnosticRun{}).Where("id = ?", run.ID).Update("lease_until", 1).Error)
	recovered, err := ClaimDiagnosticRun(run.ID, "replacement", time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "review_pending", recovered.State, "reserve/refund may have run even before transport send")
	require.NotNil(t, recovered.ActiveKey)
	require.Error(t, UpdateDiagnosticStep(run.ID, "before-crash", steps[0].ID, map[string]any{"state": "sending"}))
	_, err = ClaimDiagnosticRun(run.ID, "another-replacement", time.Now().Unix())
	require.Error(t, err)
}
