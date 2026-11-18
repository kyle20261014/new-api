package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func testPilotReceipt() RoutingFundingPilotReceipt {
	return RoutingFundingPilotReceipt{SchemaVersion: 1, Mode: RoutingFundingPilotMode, ReviewedBy: "synthetic", ReviewedAt: common.GetTimestamp(), CutoffAt: common.GetTimestamp() - 1, OpeningBalanceRef: "test/opening", DifferencesRef: "test/differences", InFlightRef: "test/in-flight", BackupRef: "test/backup", IsolationRef: "test/isolation", AccessControlRef: "test/access", DeploymentID: "synthetic-pilot", CandidateCommit: "1234567890abcdef1234567890abcdef12345678", DatabaseVersion: "synthetic-version", DatabaseName: "synthetic-db", WriterRole: "synthetic-writer", OldWriterRole: "synthetic-old", PoolMaxOpenConns: 4, NodeCount: 1, BindIP: "127.0.0.1"}
}

func TestRoutingFundingPilotReceipt(t *testing.T) {
	r := testPilotReceipt()
	marshal := func(r RoutingFundingPilotReceipt) string {
		data, err := common.Marshal(r)
		require.NoError(t, err)
		return string(data)
	}
	_, err := ParseRoutingFundingPilotReceipt(marshal(r), r.DeploymentID, r.CandidateCommit, 1)
	require.NoError(t, err)
	legacy, err := common.Marshal(struct {
		RoutingFundingPilotReceipt
		MaxInFlight int `json:"max_in_flight"`
	}{r, 4})
	require.NoError(t, err)
	_, err = ParseRoutingFundingPilotReceipt(string(legacy), r.DeploymentID, r.CandidateCommit, 1)
	require.NoError(t, err, "legacy HTTP limit must not block receipt compatibility")
	for name, mutate := range map[string]func(*RoutingFundingPilotReceipt){
		"no opening balance":  func(r *RoutingFundingPilotReceipt) { r.OpeningBalanceRef = "" },
		"no differences":      func(r *RoutingFundingPilotReceipt) { r.DifferencesRef = "" },
		"no in-flight review": func(r *RoutingFundingPilotReceipt) { r.InFlightRef = "" },
		"no backup":           func(r *RoutingFundingPilotReceipt) { r.BackupRef = "" },
		"no isolation":        func(r *RoutingFundingPilotReceipt) { r.IsolationRef = "" },
		"no access review":    func(r *RoutingFundingPilotReceipt) { r.AccessControlRef = "" },
		"wildcard":            func(r *RoutingFundingPilotReceipt) { r.BindIP = "0.0.0.0" },
		"public interface":    func(r *RoutingFundingPilotReceipt) { r.BindIP = "8.8.8.8" },
		"wrong commit":        func(r *RoutingFundingPilotReceipt) { r.CandidateCommit = "abc" },
		"multiple writers":    func(r *RoutingFundingPilotReceipt) { r.NodeCount = 2 },
		"invalid pool":        func(r *RoutingFundingPilotReceipt) { r.PoolMaxOpenConns = 1 },
		"same DB login":       func(r *RoutingFundingPilotReceipt) { r.OldWriterRole = r.WriterRole },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			mutate(&bad)
			_, err := ParseRoutingFundingPilotReceipt(marshal(bad), r.DeploymentID, r.CandidateCommit, 1)
			require.ErrorIs(t, err, ErrRoutingFundingMigrationBlocked)
		})
	}
	require.ErrorIs(t, validateRoutingFundingReceipt(marshal(r)), ErrRoutingFundingMigrationBlocked, "a pilot receipt is not strict capacity evidence")
}

// Requires a disposable PostgreSQL database owned by a non-superuser; the
// retired synthetic login must exist as NOLOGIN and have no active sessions.
func TestRoutingFundingPilotPostgres(t *testing.T) {
	dsn, retired := os.Getenv("TEST_ROUTING_PILOT_DSN"), os.Getenv("TEST_ROUTING_PILOT_OLD_ROLE")
	if dsn == "" || retired == "" {
		t.Skip("disposable PostgreSQL pilot fixture not configured")
	}
	open := func() *gorm.DB {
		t.Helper()
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(4)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		return db
	}
	db, peer := open(), open()
	r := testPilotReceipt()
	r.OldWriterRole = retired
	require.NoError(t, db.Raw("SELECT version()").Scan(&r.DatabaseVersion).Error)
	require.NoError(t, db.Raw("SELECT current_database()").Scan(&r.DatabaseName).Error)
	require.NoError(t, db.Raw("SELECT current_user").Scan(&r.WriterRole).Error)
	stillLive := r
	stillLive.OldWriterRole = "postgres"
	_, err := AcquireRoutingFundingPilotLock(t.Context(), db, stillLive)
	require.ErrorIs(t, err, ErrRoutingFundingMigrationBlocked)
	lock, err := AcquireRoutingFundingPilotLock(t.Context(), db, r)
	require.NoError(t, err)
	t.Cleanup(lock.Close)
	require.True(t, lock.Held(t.Context()))
	_, err = AcquireRoutingFundingPilotLock(t.Context(), peer, r)
	require.ErrorIs(t, err, ErrRoutingFundingMigrationBlocked)
	require.NoError(t, MigrateRoutingFunding(db))
	evidence, err := common.Marshal(r)
	require.NoError(t, err)
	require.NoError(t, InitializeRoutingFundingMode(db, string(evidence), r.Mode, r.DeploymentID, r.CandidateCommit, 1))
	require.NoError(t, ValidateRoutingFundingMode(peer, r.Mode, r.DeploymentID, r.CandidateCommit, 1))
	require.ErrorIs(t, ValidateRoutingFundingMode(peer, "sync_db_v1", r.DeploymentID, r.CandidateCommit, 1), ErrRoutingFundingMigrationBlocked)
	require.ErrorIs(t, ValidateRoutingFundingMode(peer, r.Mode, "wrong-deployment", r.CandidateCommit, 1), ErrRoutingFundingMigrationBlocked)
	_, err = lock.conn.ExecContext(t.Context(), `SELECT pg_advisory_unlock($1)`, pilotLockKey)
	require.NoError(t, err)
	require.False(t, lock.Held(t.Context()))
	replacement, err := AcquireRoutingFundingPilotLock(t.Context(), peer, r)
	require.NoError(t, err)
	replacement.Close()
}
