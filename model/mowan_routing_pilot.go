package model

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const RoutingFundingPilotMode = "sync_db_pilot_v1"

type RoutingFundingPilotReceipt struct {
	SchemaVersion     int    `json:"schema_version"`
	Mode              string `json:"mode"`
	ReviewedBy        string `json:"reviewed_by"`
	ReviewedAt        int64  `json:"reviewed_at"`
	CutoffAt          int64  `json:"cutoff_at"`
	OpeningBalanceRef string `json:"opening_balance_ref"`
	DifferencesRef    string `json:"differences_ref"`
	InFlightRef       string `json:"in_flight_ref"`
	BackupRef         string `json:"backup_ref"`
	IsolationRef      string `json:"isolation_ref"`
	AccessControlRef  string `json:"access_control_ref"`
	DeploymentID      string `json:"deployment_id"`
	CandidateCommit   string `json:"candidate_commit"`
	DatabaseVersion   string `json:"database_version"`
	DatabaseName      string `json:"database_name"`
	WriterRole        string `json:"writer_role"`
	OldWriterRole     string `json:"old_writer_role"`
	PoolMaxOpenConns  int    `json:"pool_max_open_conns"`
	NodeCount         int    `json:"node_count"`
	BindIP            string `json:"bind_ip"`
}

func ParseRoutingFundingPilotReceipt(raw, deploymentID, candidateCommit string, nodeCount int) (RoutingFundingPilotReceipt, error) {
	var r RoutingFundingPilotReceipt
	if len(raw) > 65536 || common.UnmarshalJsonStr(raw, &r) != nil || r.SchemaVersion != 1 || r.Mode != RoutingFundingPilotMode || r.CutoffAt <= 0 || r.ReviewedAt < r.CutoffAt || r.ReviewedAt > common.GetTimestamp()+60 || nodeCount != 1 || r.NodeCount != 1 || r.DeploymentID != deploymentID || r.CandidateCommit != candidateCommit || r.PoolMaxOpenConns < 2 || r.OldWriterRole == r.WriterRole {
		return r, ErrRoutingFundingMigrationBlocked
	}
	for _, value := range []string{r.ReviewedBy, r.OpeningBalanceRef, r.DifferencesRef, r.InFlightRef, r.BackupRef, r.IsolationRef, r.AccessControlRef, r.DeploymentID, r.DatabaseVersion, r.DatabaseName, r.WriterRole, r.OldWriterRole} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 {
			return r, ErrRoutingFundingMigrationBlocked
		}
	}
	ip, err := netip.ParseAddr(r.BindIP)
	if err != nil || ip.Zone() != "" || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return r, ErrRoutingFundingMigrationBlocked
	}
	commit, err := hex.DecodeString(candidateCommit)
	if err != nil || len(commit) != 20 || strings.Trim(candidateCommit, "0") == "" {
		return r, ErrRoutingFundingMigrationBlocked
	}
	return r, nil
}

// Both the dedicated fence connection and the app pool must use the reviewed
// primary, database and login. The retired login cannot start new sessions.
const pilotDatabaseCheck = `SELECT version() = ? AND current_database() = ? AND current_user = ?
 AND session_user = current_user AND NOT pg_is_in_recovery()
 AND current_setting('fsync') = 'on' AND current_setting('synchronous_commit') IN ('on', 'remote_apply')
 AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND NOT rolsuper)
 AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = ? AND NOT rolcanlogin)
 AND NOT EXISTS (SELECT 1 FROM pg_stat_activity WHERE usename = ?)`

func validatePilotDatabase(db *gorm.DB, r RoutingFundingPilotReceipt) error {
	if db.Dialector.Name() != "postgres" || common.BatchUpdateEnabled {
		return ErrRoutingFundingMigrationBlocked
	}
	pool, err := db.DB()
	if err != nil || pool.Stats().MaxOpenConnections != r.PoolMaxOpenConns {
		return ErrRoutingFundingMigrationBlocked
	}
	var valid bool
	if err = db.Raw(pilotDatabaseCheck, r.DatabaseVersion, r.DatabaseName, r.WriterRole, r.OldWriterRole, r.OldWriterRole).Scan(&valid).Error; err != nil || !valid {
		return fmt.Errorf("%w: pilot database, durability or retired writer differs from reviewed evidence", ErrRoutingFundingMigrationBlocked)
	}
	return nil
}

// A fixed key per PostgreSQL database, not per deployment ID, prevents two
// cooperating candidates from starting against the same wallet database.
const pilotLockKey int64 = 0x4d4f57414e000001

type RoutingFundingPilotLock struct {
	conn *sql.Conn
	pid  int
}

func AcquireRoutingFundingPilotLock(ctx context.Context, db *gorm.DB, r RoutingFundingPilotReceipt) (*RoutingFundingPilotLock, error) {
	if err := validatePilotDatabase(db.WithContext(ctx), r); err != nil {
		return nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, ErrRoutingFundingMigrationBlocked
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, ErrRoutingFundingMigrationBlocked
	}
	l := &RoutingFundingPilotLock{conn: conn}
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1), pg_backend_pid()`, pilotLockKey).Scan(&acquired, &l.pid); err != nil || !acquired {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: pilot writer lock is unavailable", ErrRoutingFundingMigrationBlocked)
	}
	return l, nil
}

func (l *RoutingFundingPilotLock) Held(ctx context.Context) bool {
	var held bool
	err := l.conn.QueryRowContext(ctx, `SELECT pg_backend_pid() = $1 AND EXISTS (
 SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND pid = $1 AND granted
 AND objsubid = 1 AND classid::bigint = ($2::bigint >> 32) AND objid::bigint = ($2::bigint & 4294967295))`, l.pid, pilotLockKey).Scan(&held)
	return err == nil && held
}

func (l *RoutingFundingPilotLock) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, pilotLockKey)
	_ = l.conn.Close()
}
