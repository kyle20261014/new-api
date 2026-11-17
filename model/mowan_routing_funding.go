package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRoutingFundingConflict               = errors.New("routing_funding_operation_conflict")
	ErrRoutingFundingInsufficient           = errors.New("wallet quota insufficient")
	ErrRoutingTokenInsufficient             = errors.New("token quota insufficient")
	ErrRoutingSubmissionConflict            = errors.New("routing_submission_version_conflict")
	ErrRoutingFundingMigrationBlocked       = errors.New("routing_funding_migration_blocked")
	ErrRoutingRecoveryCredentialUnavailable = errors.New("routing_recovery_credential_unavailable")
)

type RoutingFundingSession struct {
	FundingID      string `gorm:"primaryKey;type:varchar(128)"`
	UserID         int    `gorm:"index"`
	TokenID        int
	Source         string `gorm:"type:varchar(32)"`
	SubscriptionID int
	Quota          int
	Status         string `gorm:"type:varchar(16)"`
	UpdatedAt      int64  `gorm:"autoUpdateTime"`
}

func (RoutingFundingSession) TableName() string { return "mowan_routing_funding_sessions" }

type RoutingFundingOperation struct {
	OperationID    string `gorm:"primaryKey;type:varchar(191)"`
	FundingID      string `gorm:"index;type:varchar(128)"`
	UserID         int
	TokenID        int
	Source         string `gorm:"type:varchar(32)"`
	SubscriptionID int
	Kind           string `gorm:"type:varchar(16)"`
	Quota          int
	Delta          int
	TaskID         int64
	TaskKind       string `gorm:"type:varchar(24)"`
	ModelName      string `gorm:"type:varchar(191)"`
	AllowDebt      bool
	CreatedAt      int64 `gorm:"autoCreateTime"`
	Replay         bool  `gorm:"-"`
}

func (RoutingFundingOperation) TableName() string { return "mowan_routing_funding_operations" }

type RoutingFundingRequest struct {
	FundingID, OperationID  string
	UserID, TokenID         int
	Source, ModelName, Kind string
	Quota                   int // target total, never an increment
	// AllowDebt is only for existing post-usage accounting, never before submission.
	AllowDebt bool
	TaskID    int64  // update persisted task quota in the same transaction
	TaskKind  string // empty/task or midjourney
}

type RoutingFundingDeltaRequest struct {
	OperationID                            string // empty legacy calls promise atomicity, not durable replay
	UserID, TokenID, SubscriptionID, Delta int
	Source                                 string
	RequireAvailable                       bool
}

// ApplyRoutingFundingDeltaContext is the shared post-usage/stream-frame seam.
// It does no pricing and commits wallet/subscription and key accounting together.
func ApplyRoutingFundingDeltaContext(ctx context.Context, req RoutingFundingDeltaRequest) (*RoutingFundingOperation, error) {
	if req.UserID <= 0 || req.TokenID < 0 || req.Delta < -common.MaxQuota || req.Delta > common.MaxQuota || len(req.OperationID) > 128 || (req.Source != "wallet" && req.Source != "subscription") || (req.Source == "subscription" && req.SubscriptionID <= 0) {
		return nil, errors.New("invalid funding delta")
	}
	result := RoutingFundingOperation{OperationID: req.OperationID, FundingID: req.OperationID, UserID: req.UserID, TokenID: req.TokenID, Source: req.Source, SubscriptionID: req.SubscriptionID, Kind: "delta", Quota: req.Delta, Delta: req.Delta, AllowDebt: !req.RequireAvailable}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, req.UserID).Error; err != nil {
			return err
		}
		var token Token
		if req.TokenID > 0 {
			if err := lockForUpdate(tx.Unscoped()).Where("id = ? AND user_id = ?", req.TokenID, req.UserID).First(&token).Error; err != nil {
				return err
			}
		}
		if req.OperationID != "" {
			var prior RoutingFundingOperation
			found := tx.Where("operation_id = ?", req.OperationID).Find(&prior)
			if found.Error != nil {
				return found.Error
			}
			if found.RowsAffected > 0 {
				if prior.Kind != "delta" || prior.UserID != req.UserID || prior.TokenID != req.TokenID || prior.Source != req.Source || prior.SubscriptionID != req.SubscriptionID || prior.Delta != req.Delta || prior.AllowDebt != !req.RequireAvailable {
					return ErrRoutingFundingConflict
				}
				result = prior
				result.Replay = true
				return nil
			}
		}
		if req.Source == "subscription" {
			var sub UserSubscription
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", req.SubscriptionID, req.UserID).First(&sub).Error; err != nil {
				return err
			}
			if err := PostConsumeUserSubscriptionDeltaTx(tx, req.SubscriptionID, int64(req.Delta)); err != nil {
				return err
			}
		} else if req.Delta > 0 {
			if req.RequireAvailable {
				ok, err := reserveUserQuotaTx(tx, req.UserID, req.Delta)
				if err != nil {
					return err
				}
				if !ok {
					return ErrRoutingFundingInsufficient
				}
			} else if err := tx.Model(&User{}).Where("id = ?", req.UserID).Update("quota", gorm.Expr("quota - ?", req.Delta)).Error; err != nil {
				return err
			}
		} else if req.Delta < 0 {
			if err := increaseUserQuotaTx(tx, req.UserID, -req.Delta); err != nil {
				return err
			}
		}
		if token.Id > 0 && req.Delta != 0 {
			if req.Delta > 0 && req.RequireAvailable && !token.UnlimitedQuota {
				ok, err := reserveTokenQuotaTx(tx.Unscoped(), token.Id, req.Delta)
				if err != nil {
					return err
				}
				if !ok {
					return ErrRoutingTokenInsufficient
				}
			} else if err := tx.Unscoped().Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{"remain_quota": gorm.Expr("remain_quota - ?", req.Delta), "used_quota": gorm.Expr("used_quota + ?", req.Delta), "accessed_time": common.GetTimestamp()}).Error; err != nil {
				return err
			}
		}
		if req.OperationID != "" {
			return tx.Create(&result).Error
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("funding delta operation %s: %w", req.OperationID, err)
	}
	return &result, nil
}

type RoutingSubmission struct {
	SubmissionID   string `gorm:"primaryKey;type:varchar(128)" json:"submission_id"`
	RequestID      string `gorm:"uniqueIndex:idx_routing_request_attempt;type:varchar(128)" json:"-"`
	AttemptNo      int    `gorm:"uniqueIndex:idx_routing_request_attempt" json:"-"`
	FundingID      string `gorm:"index;type:varchar(128)" json:"-"`
	OperationID    string `gorm:"type:varchar(191)" json:"-"`
	UserID         int    `gorm:"index" json:"-"`
	TokenID        int    `json:"-"`
	ClientModel    string `gorm:"type:varchar(191)" json:"-"`
	BusinessGroup  string `gorm:"type:varchar(191)" json:"-"`
	ProtocolClass  string `gorm:"type:varchar(32)" json:"-"`
	ProviderKey    string `gorm:"type:varchar(191)" json:"-"`
	OperationKey   string `gorm:"type:varchar(191)" json:"-"`
	ChannelID      int    `json:"-"`
	State          string `gorm:"index;type:varchar(16)" json:"state"`
	Version        int64  `json:"version"`
	SendOwnerID    string `gorm:"type:varchar(64)" json:"-"`
	SendLeaseUntil int64  `gorm:"index" json:"-"`
	Quota          int    `json:"reserved_quota"`
	PriceSnapshot  string `gorm:"type:text" json:"-"`
	UpstreamTaskID string `gorm:"type:varchar(191)" json:"-"`
	LocalTaskID    int64  `gorm:"index" json:"local_task_id,omitempty"`
	LocalTaskKind  string `gorm:"type:varchar(24)" json:"local_task_kind,omitempty"`
	RecoveryJSON   string `gorm:"type:text" json:"-"`
	// Terminal probe identity is persisted before sending and retained on acceptance.
	TerminalProbeEpoch   int64  `json:"-"`
	TerminalProbeLeaseID string `gorm:"type:varchar(128)" json:"-"`
	ReconcileReason      string `gorm:"type:text" json:"-"`
	ReconcileActor       int    `json:"-"`
	ReconcileVersion     int64  `json:"-"`
	ReconcileProofJSON   string `gorm:"type:text" json:"-"`
	CreatedAt            int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt            int64  `gorm:"autoUpdateTime;index" json:"updated_at"`
}

func (RoutingSubmission) TableName() string { return "mowan_routing_submissions" }

// RoutingTaskRecovery is private, bounded, credential-free material for local
// insertion only. It must never be used to call the provider's create endpoint.
// Task.Data and prompt/input are deliberately excluded.
type RoutingTaskRecovery struct {
	Task                  Task            `json:"task"`
	PrivateData           TaskPrivateData `json:"private_data"`
	Midjourney            *Midjourney     `json:"midjourney,omitempty"`
	CredentialFingerprint string          `json:"credential_fingerprint,omitempty"`
}

func RoutingCredentialFingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("routing-submission-key-v1\x00" + key))
	return hex.EncodeToString(sum[:])
}

type RoutingFundingMode struct {
	ID        int    `gorm:"primaryKey;autoIncrement:false"`
	Mode      string `gorm:"type:varchar(32)"`
	Evidence  string `gorm:"type:text"`
	CreatedAt int64  `gorm:"autoCreateTime"`
}

func (RoutingFundingMode) TableName() string { return "mowan_routing_funding_mode" }

// RoutingFundingMigrationReceipt is a deployment operator's reviewed evidence
// index. These references do not let this binary fence an old deployment.
type RoutingFundingMigrationReceipt struct {
	SchemaVersion            int     `json:"schema_version"`
	ReviewedBy               string  `json:"reviewed_by"`
	ReviewedAt               int64   `json:"reviewed_at"`
	ParticipantInventoryRef  string  `json:"participant_inventory_ref"`
	PendingSnapshotsRef      string  `json:"pending_snapshots_ref"`
	BalanceReconciliationRef string  `json:"balance_reconciliation_ref"`
	DBCommitEvidenceRef      string  `json:"db_commit_evidence_ref"`
	BackupRef                string  `json:"backup_ref"`
	PendingDrainProven       bool    `json:"pending_drain_proven"`
	OldNodesIsolated         bool    `json:"old_nodes_isolated"`
	CapacityEvidenceRef      string  `json:"capacity_evidence_ref"`
	CapacityPassed           bool    `json:"capacity_passed"`
	DeploymentID             string  `json:"deployment_id"`
	HardwareRef              string  `json:"hardware_ref"`
	DatabaseVersion          string  `json:"database_version"`
	PoolMaxOpenConns         int     `json:"pool_max_open_conns"`
	NodeCount                int     `json:"node_count"`
	CandidateCommit          string  `json:"candidate_commit"`
	WorkloadRef              string  `json:"workload_ref"`
	StableDurationSeconds    int     `json:"stable_duration_seconds"`
	StableThroughputRPS      float64 `json:"stable_throughput_rps"`
	ExpectedPeakRPS          float64 `json:"expected_peak_rps"`
}

func validateRoutingFundingReceipt(raw string) error {
	var receipt RoutingFundingMigrationReceipt
	if len(raw) > 65536 || common.UnmarshalJsonStr(raw, &receipt) != nil || receipt.SchemaVersion != 1 || receipt.ReviewedAt <= 0 || receipt.ReviewedAt > common.GetTimestamp()+60 || !receipt.PendingDrainProven || !receipt.OldNodesIsolated || !receipt.CapacityPassed {
		return ErrRoutingFundingMigrationBlocked
	}
	for _, ref := range []string{receipt.ReviewedBy, receipt.ParticipantInventoryRef, receipt.PendingSnapshotsRef, receipt.BalanceReconciliationRef, receipt.DBCommitEvidenceRef, receipt.BackupRef, receipt.CapacityEvidenceRef, receipt.DeploymentID, receipt.HardwareRef, receipt.DatabaseVersion, receipt.CandidateCommit, receipt.WorkloadRef} {
		if strings.TrimSpace(ref) == "" || len(ref) > 4096 {
			return ErrRoutingFundingMigrationBlocked
		}
	}
	if receipt.PoolMaxOpenConns <= 0 || receipt.NodeCount <= 0 || receipt.StableDurationSeconds < 1800 || receipt.StableThroughputRPS <= 0 || math.IsNaN(receipt.StableThroughputRPS) || math.IsInf(receipt.StableThroughputRPS, 0) || receipt.ExpectedPeakRPS <= 0 || math.IsNaN(receipt.ExpectedPeakRPS) || math.IsInf(receipt.ExpectedPeakRPS, 0) || receipt.ExpectedPeakRPS > receipt.StableThroughputRPS*0.70 {
		return fmt.Errorf("%w: capacity requires a 30-minute stable measurement and expected peak within 70%%", ErrRoutingFundingMigrationBlocked)
	}
	return nil
}

func MigrateRoutingFunding(db *gorm.DB) error {
	return db.AutoMigrate(&RoutingFundingMode{}, &RoutingFundingSession{}, &RoutingFundingOperation{}, &RoutingSubmission{}, &SubscriptionPreConsumeRecord{})
}

// Every deployment requires reviewed reconciliation/isolation/capacity evidence,
// including empty databases. A marker cannot fence old binaries; isolation is external.
func InitializeRoutingFundingMode(db *gorm.DB, reviewedEvidence, configuredMode, deploymentID, candidateCommit string, nodeCount int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var mode RoutingFundingMode
		err := lockForUpdate(tx).First(&mode, 1).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && mode.Mode != "sync_db_v1" {
			return ErrRoutingFundingMigrationBlocked
		}
		if reviewedEvidence == "" {
			reviewedEvidence = mode.Evidence
		}
		if err := validateRoutingFundingDeployment(tx, reviewedEvidence, configuredMode, deploymentID, candidateCommit, nodeCount); err != nil {
			return err
		}
		if mode.ID != 0 {
			return tx.Model(&mode).Update("evidence", reviewedEvidence).Error
		}
		return tx.Create(&RoutingFundingMode{ID: 1, Mode: "sync_db_v1", Evidence: reviewedEvidence}).Error
	})
}

func CheckRoutingFundingPendingBalances() error {
	for _, kind := range []int{BatchUpdateTypeUserQuota, BatchUpdateTypeTokenQuota} {
		batchUpdateLocks[kind].Lock()
		pending := len(batchUpdateStores[kind]) != 0
		batchUpdateLocks[kind].Unlock()
		if pending {
			return fmt.Errorf("%w: pending balance batch type=%d", ErrRoutingFundingMigrationBlocked, kind)
		}
	}
	return nil
}

func ValidateRoutingFundingMode(db *gorm.DB, configuredMode, deploymentID, candidateCommit string, nodeCount int) error {
	var mode RoutingFundingMode
	if err := db.First(&mode, 1).Error; err != nil {
		return fmt.Errorf("%w: %v", ErrRoutingFundingMigrationBlocked, err)
	}
	if mode.Mode != configuredMode {
		return ErrRoutingFundingMigrationBlocked
	}
	return validateRoutingFundingDeployment(db, mode.Evidence, configuredMode, deploymentID, candidateCommit, nodeCount)
}

func validateRoutingFundingDeployment(db *gorm.DB, evidence, configuredMode, deploymentID, candidateCommit string, nodeCount int) error {
	if configuredMode != "sync_db_v1" || (db.Dialector.Name() == "sqlite" && nodeCount != 1) {
		return ErrRoutingFundingMigrationBlocked
	}
	if err := CheckRoutingFundingPendingBalances(); err != nil {
		return err
	}
	if err := validateRoutingFundingReceipt(evidence); err != nil {
		return err
	}
	var receipt RoutingFundingMigrationReceipt
	if err := common.UnmarshalJsonStr(evidence, &receipt); err != nil {
		return fmt.Errorf("%w: decode deployment evidence: %v", ErrRoutingFundingMigrationBlocked, err)
	}
	pool, err := db.DB()
	if err != nil {
		return fmt.Errorf("%w: read deployment pool: %v", ErrRoutingFundingMigrationBlocked, err)
	}
	versionQuery := "SELECT version()"
	if db.Dialector.Name() == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	if err := db.Raw(versionQuery).Scan(&version).Error; err != nil {
		return fmt.Errorf("%w: read deployment database version: %v", ErrRoutingFundingMigrationBlocked, err)
	}
	if receipt.DeploymentID != deploymentID || receipt.CandidateCommit != candidateCommit || receipt.NodeCount != nodeCount || receipt.PoolMaxOpenConns != pool.Stats().MaxOpenConnections || receipt.DatabaseVersion != version {
		return fmt.Errorf("%w: deployment identity, candidate, node count, database version or pool differs from capacity evidence", ErrRoutingFundingMigrationBlocked)
	}
	return nil
}

func routingDBTime(tx *gorm.DB) (int64, error) {
	var ts int64
	query := "SELECT UNIX_TIMESTAMP()"
	switch tx.Dialector.Name() {
	case "sqlite":
		query = "SELECT strftime('%s','now')"
	case "postgres":
		query = "SELECT EXTRACT(EPOCH FROM NOW())::bigint"
	}
	err := tx.Raw(query).Scan(&ts).Error
	return ts, err
}

// PrepareRoutingFunding serializes the shared wallet before token/subscription.
// The ledger, both balances, optional Task quota, and prepared row commit together.
func PrepareRoutingFunding(req RoutingFundingRequest, submission *RoutingSubmission) (*RoutingFundingOperation, error) {
	return PrepareRoutingFundingContext(context.Background(), req, submission)
}

func PrepareRoutingFundingContext(ctx context.Context, req RoutingFundingRequest, submission *RoutingSubmission) (*RoutingFundingOperation, error) {
	if req.UserID <= 0 || req.TokenID < 0 || req.Quota < 0 || req.Quota > common.MaxQuota || len(req.FundingID) == 0 || len(req.FundingID) > 128 || len(req.OperationID) == 0 || len(req.OperationID) > 191 || (req.Source != "wallet" && req.Source != "subscription") || (req.Kind != "reserve" && req.Kind != "settle" && req.Kind != "refund" && req.Kind != "accepted") || (req.Kind == "refund" && req.Quota != 0) {
		return nil, errors.New("invalid routing funding request")
	}
	var result RoutingFundingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return prepareRoutingFundingTx(tx, req, submission, &result)
	})
	if err != nil {
		return nil, fmt.Errorf("funding operation %s (%s): %w", req.OperationID, req.Kind, err)
	}
	return &result, nil
}

func prepareRoutingFundingTx(tx *gorm.DB, req RoutingFundingRequest, submission *RoutingSubmission, result *RoutingFundingOperation) error {
	var user User
	if err := lockForUpdate(tx).First(&user, req.UserID).Error; err != nil {
		return err
	}
	var token Token
	if req.TokenID > 0 {
		tokenTx := tx
		if req.Kind != "reserve" {
			tokenTx = tx.Unscoped()
		}
		err := lockForUpdate(tokenTx).Where("id = ? AND user_id = ?", req.TokenID, req.UserID).First(&token).Error
		if err != nil && !(req.Kind != "reserve" && errors.Is(err, gorm.ErrRecordNotFound)) {
			return err
		}
	}
	var prior RoutingFundingOperation
	found := tx.Where("operation_id = ?", req.OperationID).Find(&prior)
	if found.Error != nil {
		return found.Error
	}
	if found.RowsAffected > 0 {
		if prior.FundingID != req.FundingID || prior.UserID != req.UserID || prior.TokenID != req.TokenID || prior.Source != req.Source || prior.Kind != req.Kind || prior.Quota != req.Quota || prior.TaskID != req.TaskID || prior.TaskKind != req.TaskKind || (req.Kind == "reserve" && (prior.ModelName != req.ModelName || prior.AllowDebt != req.AllowDebt)) {
			return ErrRoutingFundingConflict
		}
		*result = prior
		result.Replay = true
		return prepareRoutingSubmissionTx(tx, req, submission)
	}
	if req.TaskID > 0 && (req.Kind == "settle" || req.Kind == "refund") {
		if err := validateRoutingFundingTerminalTx(tx, req); err != nil {
			return err
		}
	}
	var fund RoutingFundingSession
	lookup := tx.Where("funding_id = ?", req.FundingID).Find(&fund)
	if lookup.Error != nil {
		return lookup.Error
	}
	fresh := lookup.RowsAffected == 0
	if fresh {
		if req.Kind != "reserve" {
			return errors.New("funding reservation not found")
		}
		fund = RoutingFundingSession{FundingID: req.FundingID, UserID: req.UserID, TokenID: req.TokenID, Source: req.Source, Status: "reserved"}
	} else if fund.UserID != req.UserID || fund.TokenID != req.TokenID || fund.Source != req.Source {
		return ErrRoutingFundingConflict
	}
	if fund.Status != "reserved" {
		return ErrRoutingFundingConflict
	}
	var unresolved int64
	query := tx.Model(&RoutingSubmission{}).Where("funding_id = ?", req.FundingID)
	if req.Kind == "reserve" || req.TaskID == 0 {
		query = query.Where("state IN ?", []string{"prepared", "sending", "unknown", "accepted"})
		if req.Kind == "reserve" && submission != nil {
			query = query.Where("submission_id <> ?", submission.SubmissionID)
		}
	} else {
		query = query.Where("state IN ?", []string{"sending", "unknown"})
	}
	if err := query.Count(&unresolved).Error; err != nil {
		return err
	}
	if unresolved > 0 {
		return errors.New("submission unresolved: reservation retained")
	}
	if req.Kind == "accepted" {
		var accepted int64
		if err := tx.Model(&RoutingSubmission{}).Where("funding_id = ? AND local_task_id = ? AND state = ?", req.FundingID, req.TaskID, "accepted").Count(&accepted).Error; err != nil {
			return err
		}
		if req.TaskID <= 0 || accepted != 1 {
			return ErrRoutingSubmissionConflict
		}
	}
	target := req.Quota
	if req.Kind == "reserve" {
		target = max(target, fund.Quota)
	}
	delta := target - fund.Quota
	if req.Kind == "reserve" && (user.Status != common.UserStatusEnabled || (req.TokenID > 0 && token.Status != common.TokenStatusEnabled)) {
		return errors.New("routing funding identity unavailable")
	}
	if req.Source == "wallet" {
		if delta > 0 {
			if req.Kind == "reserve" && !req.AllowDebt {
				ok, err := reserveUserQuotaTx(tx, req.UserID, delta)
				if err != nil {
					return err
				}
				if !ok {
					return ErrRoutingFundingInsufficient
				}
			} else if err := tx.Model(&User{}).Where("id = ?", req.UserID).Update("quota", gorm.Expr("quota - ?", delta)).Error; err != nil {
				return err
			}
		} else if delta < 0 {
			if err := increaseUserQuotaTx(tx, req.UserID, -delta); err != nil {
				return err
			}
		}
	}
	if token.Id > 0 && delta != 0 {
		if delta > 0 && req.Kind == "reserve" && !token.UnlimitedQuota {
			ok, err := reserveTokenQuotaTx(tx, token.Id, delta)
			if err != nil {
				return err
			}
			if !ok {
				return ErrRoutingTokenInsufficient
			}
		} else {
			if err := tx.Unscoped().Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{"remain_quota": gorm.Expr("remain_quota - ?", delta), "used_quota": gorm.Expr("used_quota + ?", delta), "accessed_time": common.GetTimestamp()}).Error; err != nil {
				return err
			}
		}
	}
	if req.Source == "subscription" {
		if fund.SubscriptionID == 0 && target > 0 {
			sub, err := PreConsumeUserSubscriptionTx(tx, req.FundingID, req.UserID, req.ModelName, 0, int64(target))
			if err != nil {
				return err
			}
			fund.SubscriptionID = sub.UserSubscriptionId
		} else if fund.SubscriptionID > 0 && delta != 0 {
			if err := PostConsumeUserSubscriptionDeltaTx(tx, fund.SubscriptionID, int64(delta)); err != nil {
				return err
			}
		}
		if fund.SubscriptionID > 0 {
			status := "consumed"
			if req.Kind == "settle" {
				status = "settled"
			}
			if req.Kind == "refund" {
				status = "refunded"
			}
			if err := tx.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", req.FundingID).Updates(map[string]any{"pre_consumed": target, "status": status}).Error; err != nil {
				return err
			}
		}
	}
	fund.Quota = target
	if req.Kind == "settle" {
		fund.Status = "settled"
	}
	if req.Kind == "refund" {
		fund.Status = "refunded"
	}
	if fresh {
		if err := tx.Create(&fund).Error; err != nil {
			return err
		}
	} else if err := tx.Save(&fund).Error; err != nil {
		return err
	}
	*result = RoutingFundingOperation{OperationID: req.OperationID, FundingID: req.FundingID, UserID: req.UserID, TokenID: req.TokenID, Source: req.Source, SubscriptionID: fund.SubscriptionID, Kind: req.Kind, Quota: req.Quota, Delta: delta, TaskID: req.TaskID, TaskKind: req.TaskKind, ModelName: req.ModelName, AllowDebt: req.AllowDebt}
	if err := tx.Create(result).Error; err != nil {
		return err
	}
	if req.TaskID > 0 {
		var taskModel any = &Task{}
		if req.TaskKind == "midjourney" {
			taskModel = &Midjourney{}
		}
		taskUpdate := tx.Model(taskModel).Where("id = ? AND user_id = ?", req.TaskID, req.UserID).Update("quota", target)
		if taskUpdate.Error != nil {
			return taskUpdate.Error
		}
		if taskUpdate.RowsAffected != 1 {
			var count int64
			if err := tx.Model(taskModel).Where("id = ? AND user_id = ?", req.TaskID, req.UserID).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return gorm.ErrRecordNotFound
			}
		}
	}
	return prepareRoutingSubmissionTx(tx, req, submission)
}

func routingTaskSubmissionReferences(db *gorm.DB, modelName string) *gorm.DB {
	table := db.Statement.Quote(db.NamingStrategy.TableName(modelName))
	query := db.Model(&RoutingSubmission{}).Select("1").
		Where("local_task_id = " + table + ".id AND user_id = " + table + ".user_id")
	if modelName == "Midjourney" {
		return query.Where("local_task_kind = ?", "midjourney")
	}
	return query.Where("local_task_kind IS NULL OR local_task_kind IN ?", []string{"", "task"})
}

func validateRoutingFundingTerminalTx(tx *gorm.DB, req RoutingFundingRequest) error {
	var status, upstream, group string
	var channelID, tokenID int
	var receipt *RoutingTaskTerminalReceipt
	var legacyFailure bool
	if req.TaskKind == "midjourney" {
		var task Midjourney
		if err := lockForUpdate(tx).First(&task, "id = ? AND user_id = ?", req.TaskID, req.UserID).Error; err != nil {
			return err
		}
		status, upstream, channelID, tokenID, receipt = task.Status, task.MjId, task.ChannelId, task.TokenId, task.RoutingTerminal
		legacyFailure = task.Progress == "100%" && task.FailReason != ""
	} else if req.TaskKind == "" || req.TaskKind == "task" {
		var task Task
		if err := lockForUpdate(tx).First(&task, "id = ? AND user_id = ?", req.TaskID, req.UserID).Error; err != nil {
			return err
		}
		status, upstream, group, channelID, tokenID, receipt = string(task.Status), task.GetUpstreamTaskID(), task.Group, task.ChannelId, task.PrivateData.TokenId, task.PrivateData.RoutingTerminal
	} else {
		return ErrRoutingFundingConflict
	}
	want := TaskStatusSuccess
	if req.Kind == "refund" {
		want = TaskStatusFailure
	}
	var submissions []RoutingSubmission
	if err := tx.Where("funding_id = ?", req.FundingID).Find(&submissions).Error; err != nil {
		return err
	}
	// Legacy tasks retain local-failure refunds; journaled submissions require upstream proof.
	legacyRefund := len(submissions) == 0 && req.Kind == "refund" && legacyFailure
	if !legacyRefund && (status != want || (len(submissions) > 0 && (receipt == nil || receipt.Status != want || receipt.ObservedAt <= 0))) {
		return ErrRoutingTerminalUnconfirmed
	}
	if tokenID != req.TokenID {
		return ErrRoutingFundingConflict
	}
	for _, s := range submissions {
		if s.State != "accepted" {
			continue
		}
		isMJ := s.LocalTaskKind == "midjourney"
		if s.UserID != req.UserID || s.TokenID != req.TokenID || s.LocalTaskID != req.TaskID || isMJ != (req.TaskKind == "midjourney") || s.ChannelID != channelID || s.UpstreamTaskID != upstream || (!isMJ && s.BusinessGroup != group) {
			return ErrRoutingFundingConflict
		}
	}
	return nil
}

func prepareRoutingSubmissionTx(tx *gorm.DB, req RoutingFundingRequest, s *RoutingSubmission) error {
	if s == nil {
		return nil
	}
	if req.Kind != "reserve" || s.SubmissionID == "" || len(s.SubmissionID) > 128 || s.RequestID == "" || len(s.RequestID) > 128 || s.AttemptNo < 1 || s.ChannelID <= 0 || s.ClientModel == "" || s.BusinessGroup == "" || s.ProtocolClass == "" || s.ProviderKey == "" || s.OperationKey == "" || len(s.PriceSnapshot) > 65536 {
		return errors.New("invalid prepared routing submission")
	}
	s.FundingID, s.OperationID, s.UserID, s.TokenID = req.FundingID, req.OperationID, req.UserID, req.TokenID
	s.State, s.Version, s.Quota = "prepared", 1, req.Quota
	now, err := routingDBTime(tx)
	if err != nil {
		return err
	}
	if s.SendOwnerID == "" {
		s.SendOwnerID = common.GetRandomString(32)
	}
	s.SendLeaseUntil = now + 30
	created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(s)
	if created.Error != nil {
		return created.Error
	}
	if created.RowsAffected == 1 {
		if s.TerminalProbeEpoch > 0 || s.TerminalProbeLeaseID != "" {
			return (RoutingMetricsStore{DB: tx}).BindTerminalProbe(RoutingProbeClaim{Key: s.metricKey(), Stage: RoutingStageTaskTerminal, Epoch: s.TerminalProbeEpoch, LeaseID: s.TerminalProbeLeaseID}, s.SubmissionID, s.ChannelID, time.Time{})
		}
		return nil
	}
	var stored RoutingSubmission
	if err := tx.First(&stored, "submission_id = ?", s.SubmissionID).Error; err != nil {
		return err
	}
	if stored.FundingID != s.FundingID || stored.RequestID != s.RequestID || stored.AttemptNo != s.AttemptNo || stored.ChannelID != s.ChannelID || stored.BusinessGroup != s.BusinessGroup || stored.OperationID != s.OperationID || stored.ClientModel != s.ClientModel || stored.ProtocolClass != s.ProtocolClass || stored.ProviderKey != s.ProviderKey || stored.OperationKey != s.OperationKey || stored.PriceSnapshot != s.PriceSnapshot || stored.Quota != s.Quota || stored.TerminalProbeEpoch != s.TerminalProbeEpoch || stored.TerminalProbeLeaseID != s.TerminalProbeLeaseID {
		return ErrRoutingSubmissionConflict
	}
	*s = stored
	return nil
}

func (s RoutingSubmission) metricKey() RoutingMetricKey {
	return RoutingMetricKey{ClientModel: s.ClientModel, BusinessGroup: s.BusinessGroup, ProtocolClass: s.ProtocolClass, ProviderKey: s.ProviderKey, OperationKey: s.OperationKey}
}

func saveRoutingTerminalSubmissionTx(tx *gorm.DB, s *RoutingSubmission, taskID string) error {
	return (RoutingMetricsStore{DB: tx}).SaveTerminalSubmission(s.metricKey(), s.SubmissionID, s.ChannelID, s.State, taskID, time.Time{})
}

func GetRoutingSubmission(id string) (*RoutingSubmission, error) {
	return GetRoutingSubmissionContext(context.Background(), id)
}

func GetRoutingSubmissionContext(ctx context.Context, id string) (*RoutingSubmission, error) {
	var s RoutingSubmission
	err := DB.WithContext(ctx).First(&s, "submission_id = ?", id).Error
	return &s, err
}

// RoutingSubmissionOriginGroup requires a journalled accepted identity. Missing
// historical Midjourney groups cannot be reconstructed from current channels.
func RoutingSubmissionOriginGroup(userID, channelID int, upstreamTaskID string) (string, error) {
	if userID <= 0 || channelID <= 0 || upstreamTaskID == "" {
		return "", errors.New("origin_task_group_unavailable")
	}
	var rows []RoutingSubmission
	if err := DB.Where("user_id = ? AND channel_id = ? AND upstream_task_id = ? AND state = ?", userID, channelID, upstreamTaskID, "accepted").Limit(2).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].BusinessGroup == "" {
		return "", errors.New("origin_task_group_unavailable")
	}
	return rows[0].BusinessGroup, nil
}

func MarkRoutingSubmissionSending(id string, version int64, ownerID ...string) (*RoutingSubmission, error) {
	if len(ownerID) != 1 || ownerID[0] == "" {
		return nil, ErrRoutingSubmissionConflict
	}
	s, err := GetRoutingSubmission(id)
	if err != nil {
		return nil, err
	}
	owner := ownerID[0]
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, s.UserID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return errors.New("routing funding identity unavailable")
		}
		if s.TokenID > 0 {
			var token Token
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", s.TokenID, s.UserID).First(&token).Error; err != nil {
				return err
			}
			if token.Status != common.TokenStatusEnabled {
				return errors.New("routing funding identity unavailable")
			}
		}
		var fund RoutingFundingSession
		if err := tx.First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
			return err
		}
		if fund.Status != "reserved" || fund.UserID != s.UserID || fund.TokenID != s.TokenID || fund.Quota < s.Quota {
			return ErrRoutingFundingConflict
		}
		var unresolved int64
		if err := tx.Model(&RoutingSubmission{}).Where("funding_id = ? AND submission_id <> ? AND state IN ?", s.FundingID, id, []string{"prepared", "sending", "unknown", "accepted"}).Count(&unresolved).Error; err != nil {
			return err
		}
		if unresolved > 0 {
			return ErrRoutingSubmissionConflict
		}
		now, err := routingDBTime(tx)
		if err != nil {
			return err
		}
		if owner == "" {
			return ErrRoutingSubmissionConflict
		}
		update := tx.Model(&RoutingSubmission{}).Where("submission_id = ? AND version = ? AND state = ? AND send_owner_id = ? AND send_lease_until > ?", id, version, "prepared", owner, now).Updates(map[string]any{"state": "sending", "version": version + 1, "updated_at": now, "send_lease_until": now + 30})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrRoutingSubmissionConflict
		}
		return tx.First(s, "submission_id = ?", id).Error
	})
	return s, err
}

func routingRecoveryJSON(upstreamTaskID string, recovery *RoutingTaskRecovery) (string, error) {
	if upstreamTaskID == "" || len(upstreamTaskID) > 191 || recovery == nil || recovery.PrivateData.Key != "" {
		return "", errors.New("accepted recovery material missing or unsafe")
	}
	if fingerprint := recovery.CredentialFingerprint; fingerprint != "" {
		if len(fingerprint) != 64 || strings.ToLower(fingerprint) != fingerprint {
			return "", errors.New("invalid recovery credential fingerprint")
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return "", errors.New("invalid recovery credential fingerprint")
		}
	}
	if mj := recovery.Midjourney; mj != nil {
		if recovery.Task.TaskID != "" || mj.MjId != upstreamTaskID || mj.UserId <= 0 || mj.ChannelId <= 0 || mj.Quota < 0 || mj.Quota > common.MaxQuota || mj.Prompt != "" || mj.PromptEn != "" || mj.Description != "" || mj.State != "" || mj.Properties != "" || mj.Buttons != "" {
			return "", errors.New("accepted Midjourney recovery material missing or unsafe")
		}
	} else if recovery.Task.TaskID == "" || recovery.Task.UserId <= 0 || recovery.Task.ChannelId <= 0 || recovery.Task.Quota < 0 || recovery.Task.Quota > common.MaxQuota || recovery.Task.Properties.Input != "" || len(recovery.Task.Data) > 0 {
		return "", errors.New("accepted recovery material missing or unsafe")
	}
	data, err := common.Marshal(recovery)
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", errors.New("accepted recovery material exceeds size limit")
	}
	return string(data), nil
}

func routingRecoveryMatches(s *RoutingSubmission, r *RoutingTaskRecovery) bool {
	if r == nil {
		return false
	}
	if r.Midjourney != nil {
		return r.Midjourney.UserId == s.UserID && r.Midjourney.ChannelId == s.ChannelID
	}
	return r.Task.UserId == s.UserID && r.Task.ChannelId == s.ChannelID && r.Task.Group == s.BusinessGroup
}

func RecordRoutingSubmissionOutcome(id string, version int64, state, upstreamTaskID string, recovery *RoutingTaskRecovery, ownerID ...string) (*RoutingSubmission, error) {
	return RecordRoutingSubmissionOutcomeContext(context.Background(), id, version, state, upstreamTaskID, recovery, ownerID...)
}

func RecordRoutingSubmissionOutcomeContext(ctx context.Context, id string, version int64, state, upstreamTaskID string, recovery *RoutingTaskRecovery, ownerID ...string) (*RoutingSubmission, error) {
	if len(ownerID) != 1 || ownerID[0] == "" {
		return nil, ErrRoutingSubmissionConflict
	}
	if state != "not_sent" && state != "rejected" && state != "accepted" && state != "unknown" {
		return nil, errors.New("invalid submission outcome")
	}
	var encoded string
	if state == "accepted" {
		var err error
		encoded, err = routingRecoveryJSON(upstreamTaskID, recovery)
		if err != nil {
			return nil, err
		}
	}
	var s RoutingSubmission
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		ownerMatches := s.SendOwnerID == ownerID[0]
		lateAccepted := ownerMatches && s.State == "unknown" && state == "accepted" && s.Version == version+1 && s.ReconcileActor == 0
		if !ownerMatches || (!lateAccepted && s.Version != version) || (s.State != "sending" && !(s.State == "unknown" && state == "accepted") && !(s.State == "prepared" && state == "not_sent")) {
			return ErrRoutingSubmissionConflict
		}
		if state == "accepted" && !routingRecoveryMatches(&s, recovery) {
			return errors.New("accepted recovery identity mismatch")
		}
		s.State, s.Version, s.UpstreamTaskID, s.RecoveryJSON = state, s.Version+1, upstreamTaskID, encoded
		if err := tx.Save(&s).Error; err != nil {
			return err
		}
		return saveRoutingTerminalSubmissionTx(tx, &s, "")
	})
	return &s, err
}

// RecoverRoutingSubmissionSending conservatively treats a durable sending row
// as unknown after ownership loss. It never recreates or refunds the request.
func RecoverRoutingSubmissionSending(id string, version int64) (*RoutingSubmission, error) {
	return RecoverRoutingSubmissionSendingContext(context.Background(), id, version)
}

func RecoverRoutingSubmissionSendingContext(ctx context.Context, id string, version int64) (*RoutingSubmission, error) {
	return RecoverExpiredRoutingSubmission(ctx, id, version)
}

func RenewRoutingSubmissionSendLease(ctx context.Context, id, ownerID string) (bool, error) {
	if id == "" || ownerID == "" {
		return false, ErrRoutingSubmissionConflict
	}
	renewed := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := routingDBTime(tx)
		if err != nil {
			return err
		}
		r := tx.Model(&RoutingSubmission{}).Where("submission_id = ? AND send_owner_id = ? AND send_lease_until > ? AND state IN ?", id, ownerID, now, []string{"prepared", "sending"}).Updates(map[string]any{"send_lease_until": now + 30})
		renewed = r.RowsAffected == 1
		return r.Error
	})
	return renewed, err
}

// RecoverExpiredRoutingSubmission never sends. Only a proven expired prepared
// owner may release funds; an expired sending owner remains financially unknown.
func RecoverExpiredRoutingSubmission(ctx context.Context, id string, version int64) (*RoutingSubmission, error) {
	s, err := GetRoutingSubmissionContext(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.State == "prepared" || s.State == "not_sent" || s.State == "rejected" {
		return ReleasePreparedRoutingFundingContext(ctx, id, version)
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, s.UserID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		if s.State == "unknown" && s.Version == version+1 {
			return nil
		}
		now, err := routingDBTime(tx)
		if err != nil {
			return err
		}
		if s.State != "sending" || s.Version != version || s.SendOwnerID == "" || s.SendLeaseUntil > now {
			return ErrRoutingSubmissionConflict
		}
		s.State, s.Version = "unknown", s.Version+1
		if err := tx.Save(s).Error; err != nil {
			return err
		}
		return saveRoutingTerminalSubmissionTx(tx, s, "")
	})
	return s, err
}

// ReleasePreparedRoutingFundingContext proves not-sent or a durable negative
// outcome after owner loss. Another active attempt prevents this refund.
func ReleasePreparedRoutingFundingContext(ctx context.Context, id string, version int64) (*RoutingSubmission, error) {
	s, err := GetRoutingSubmissionContext(ctx, id)
	if err != nil {
		return nil, err
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, s.UserID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		negative := s.State == "not_sent" || s.State == "rejected"
		if negative && (s.Version == version || s.Version == version+1) {
			var operation RoutingFundingOperation
			prior := tx.Where("operation_id = ? AND funding_id = ?", s.FundingID+":refund:0", s.FundingID).Find(&operation)
			if prior.Error != nil {
				return prior.Error
			}
			if prior.RowsAffected > 0 {
				return nil
			}
		}
		if (s.State != "prepared" && !negative) || s.Version != version {
			return ErrRoutingSubmissionConflict
		}
		now, err := routingDBTime(tx)
		if err != nil {
			return err
		}
		if s.SendOwnerID == "" || s.SendLeaseUntil > now {
			return ErrRoutingSubmissionConflict
		}
		var active int64
		if err := tx.Model(&RoutingSubmission{}).Where("funding_id = ? AND submission_id <> ? AND state IN ?", s.FundingID, id, []string{"prepared", "sending", "unknown", "accepted"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrRoutingSubmissionConflict
		}
		if s.State == "prepared" {
			s.State = "not_sent"
		}
		s.Version++
		if err := tx.Save(s).Error; err != nil {
			return err
		}
		if err := saveRoutingTerminalSubmissionTx(tx, s, ""); err != nil {
			return err
		}
		var fund RoutingFundingSession
		if err := tx.First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
			return err
		}
		var result RoutingFundingOperation
		return prepareRoutingFundingTx(tx, RoutingFundingRequest{FundingID: fund.FundingID, OperationID: fund.FundingID + ":refund:0", UserID: fund.UserID, TokenID: fund.TokenID, Source: fund.Source, Kind: "refund", Quota: 0}, nil, &result)
	})
	return s, err
}

// RoutingSubmissionProof indexes facts checked by the authorized operator against
// the original provider. It grants no ability to create another upstream task.
type RoutingSubmissionProof struct {
	RequestID    string `json:"request_id"`
	ChannelID    int    `json:"channel_id"`
	ProviderKey  string `json:"provider_key"`
	OperationKey string `json:"operation_key"`
	Outcome      string `json:"outcome"`
	ObservedAt   int64  `json:"observed_at"`
	EvidenceRef  string `json:"evidence_ref"`
	Confirmed    bool   `json:"confirmed"`
}

type RoutingSubmissionResolution struct {
	ExpectedVersion int64                  `json:"expected_version"`
	Actor           int                    `json:"-"`
	Reason          string                 `json:"reason"`
	Outcome         string                 `json:"outcome"`
	UpstreamTaskID  string                 `json:"upstream_task_id,omitempty"`
	Recovery        *RoutingTaskRecovery   `json:"-"`
	Proof           RoutingSubmissionProof `json:"proof"`
}

// ResolveRoutingSubmission records only a checked, already-happened fact.
// Negative resolutions and the unique refund commit together. Accepted identity
// stays independent of local task insertion, exactly like normal submission.
func ResolveRoutingSubmission(id string, req RoutingSubmissionResolution) (*RoutingSubmission, error) {
	if id == "" || req.ExpectedVersion < 1 || req.Actor <= 0 || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 4096 || (req.Outcome != "not_sent" && req.Outcome != "rejected" && req.Outcome != "accepted") || !req.Proof.Confirmed || req.Proof.Outcome != req.Outcome || req.Proof.ObservedAt <= 0 || req.Proof.ObservedAt > common.GetTimestamp()+60 || strings.TrimSpace(req.Proof.EvidenceRef) == "" || len(req.Proof.EvidenceRef) > 4096 {
		return nil, errors.New("invalid routing reconciliation proof")
	}
	proof, err := common.Marshal(req.Proof)
	if err != nil {
		return nil, err
	}
	var recovery string
	if req.Outcome == "accepted" {
		recovery, err = routingRecoveryJSON(req.UpstreamTaskID, req.Recovery)
		if err != nil {
			return nil, err
		}
	}
	s, err := GetRoutingSubmission(id)
	if err != nil {
		return nil, err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, s.UserID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		if s.ReconcileVersion == req.ExpectedVersion && s.State == req.Outcome && s.ReconcileActor == req.Actor && s.ReconcileReason == req.Reason && s.ReconcileProofJSON == string(proof) && s.UpstreamTaskID == req.UpstreamTaskID && s.RecoveryJSON == recovery {
			return nil
		}
		if (s.State != "unknown" && !(s.State == "accepted" && req.Outcome == "accepted" && s.RecoveryJSON == recovery && s.UpstreamTaskID == req.UpstreamTaskID)) || s.Version != req.ExpectedVersion {
			return ErrRoutingSubmissionConflict
		}
		if req.Proof.RequestID != s.RequestID || req.Proof.ChannelID != s.ChannelID || req.Proof.ProviderKey != s.ProviderKey || req.Proof.OperationKey != s.OperationKey || req.Proof.ObservedAt < s.CreatedAt {
			return errors.New("routing reconciliation proof identity mismatch")
		}
		if req.Outcome == "accepted" && !routingRecoveryMatches(s, req.Recovery) {
			return errors.New("accepted recovery identity mismatch")
		}
		s.State, s.Version, s.UpstreamTaskID, s.RecoveryJSON = req.Outcome, s.Version+1, req.UpstreamTaskID, recovery
		s.ReconcileActor, s.ReconcileReason, s.ReconcileVersion, s.ReconcileProofJSON = req.Actor, req.Reason, req.ExpectedVersion, string(proof)
		if err := tx.Save(s).Error; err != nil {
			return err
		}
		if err := saveRoutingTerminalSubmissionTx(tx, s, ""); err != nil {
			return err
		}
		if req.Outcome == "accepted" {
			return nil
		}
		var fund RoutingFundingSession
		if err := tx.First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
			return err
		}
		var result RoutingFundingOperation
		return prepareRoutingFundingTx(tx, RoutingFundingRequest{FundingID: fund.FundingID, OperationID: fund.FundingID + ":refund:0", UserID: fund.UserID, TokenID: fund.TokenID, Source: fund.Source, Kind: "refund", Quota: 0}, nil, &result)
	})
	return s, err
}

func PersistAcceptedRoutingTask(id string) (*Task, error) {
	return PersistAcceptedRoutingTaskContext(context.Background(), id)
}

func PersistAcceptedRoutingTaskContext(ctx context.Context, id string) (*Task, error) {
	var task Task
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s RoutingSubmission
		if err := lockForUpdate(tx).First(&s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		if s.State != "accepted" {
			return ErrRoutingSubmissionConflict
		}
		if s.LocalTaskKind == "midjourney" {
			return errors.New("submission uses Midjourney recovery")
		}
		if s.LocalTaskID > 0 {
			return tx.First(&task, s.LocalTaskID).Error
		}
		var recovery RoutingTaskRecovery
		if err := common.UnmarshalJsonStr(s.RecoveryJSON, &recovery); err != nil {
			return err
		}
		task = recovery.Task
		if recovery.Midjourney != nil {
			return errors.New("submission uses Midjourney recovery")
		}
		task.ID = 0
		task.PrivateData = recovery.PrivateData
		task.PrivateData.UpstreamTaskID = s.UpstreamTaskID
		if recovery.CredentialFingerprint != "" {
			var channel Channel
			if err := tx.First(&channel, "id = ?", s.ChannelID).Error; err != nil {
				return fmt.Errorf("%w: channel %d", ErrRoutingRecoveryCredentialUnavailable, s.ChannelID)
			}
			for _, key := range channel.GetKeys() {
				if RoutingCredentialFingerprint(key) == recovery.CredentialFingerprint {
					task.PrivateData.Key = key
					break
				}
			}
			if task.PrivateData.Key == "" {
				return fmt.Errorf("%w: channel %d", ErrRoutingRecoveryCredentialUnavailable, s.ChannelID)
			}
		}
		task.PrivateData.RoutingTerminal = nil
		if task.Status == TaskStatusSuccess || task.Status == TaskStatusFailure {
			receipt, err := NewRoutingTerminalReceipt(tx, string(task.Status))
			if err != nil {
				return err
			}
			task.PrivateData.RoutingTerminal = receipt
		}
		var fund RoutingFundingSession
		if err := tx.First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
			return err
		}
		task.PrivateData.TokenId = fund.TokenID
		task.PrivateData.BillingSource = fund.Source
		task.PrivateData.SubscriptionId = fund.SubscriptionID
		if task.PrivateData.Execution == nil {
			task.PrivateData.Execution = &TaskExecutionSnapshot{}
		}
		task.PrivateData.Execution.RequestID = s.RequestID
		task.CreatedAt, task.UpdatedAt = common.GetTimestamp(), common.GetTimestamp()
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		s.LocalTaskID, s.Version = task.ID, s.Version+1
		if err := tx.Save(&s).Error; err != nil {
			return err
		}
		return saveRoutingTerminalSubmissionTx(tx, &s, task.TaskID)
	})
	return &task, err
}

func PersistAcceptedRoutingMidjourney(id string) (*Midjourney, error) {
	return PersistAcceptedRoutingMidjourneyContext(context.Background(), id)
}

func PersistAcceptedRoutingMidjourneyContext(ctx context.Context, id string) (*Midjourney, error) {
	var task Midjourney
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s RoutingSubmission
		if err := lockForUpdate(tx).First(&s, "submission_id = ?", id).Error; err != nil {
			return err
		}
		if s.State != "accepted" || (s.LocalTaskKind != "" && s.LocalTaskKind != "midjourney") {
			return ErrRoutingSubmissionConflict
		}
		if s.LocalTaskID > 0 {
			return tx.First(&task, s.LocalTaskID).Error
		}
		var recovery RoutingTaskRecovery
		if err := common.UnmarshalJsonStr(s.RecoveryJSON, &recovery); err != nil {
			return err
		}
		if recovery.Midjourney == nil || !routingRecoveryMatches(&s, &recovery) {
			return errors.New("Midjourney recovery unavailable")
		}
		task = *recovery.Midjourney
		task.Id = 0
		task.RoutingTerminal = nil
		if task.Status == TaskStatusSuccess || task.Status == TaskStatusFailure {
			receipt, err := NewRoutingTerminalReceipt(tx, task.Status)
			if err != nil {
				return err
			}
			task.RoutingTerminal = receipt
		}
		var fund RoutingFundingSession
		if err := tx.First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
			return err
		}
		if fund.Source != "wallet" {
			return errors.New("Midjourney subscriptions unsupported")
		}
		task.TokenId, task.BillingChannelId = fund.TokenID, s.ChannelID
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		s.LocalTaskKind, s.LocalTaskID, s.Version = "midjourney", int64(task.Id), s.Version+1
		if err := tx.Save(&s).Error; err != nil {
			return err
		}
		return saveRoutingTerminalSubmissionTx(tx, &s, task.MjId)
	})
	return &task, err
}

func FinalizeMidjourneyFunding(task *Midjourney, refund bool) (*RoutingFundingOperation, error) {
	return FinalizeMidjourneyFundingContext(context.Background(), task, refund)
}

func FinalizeMidjourneyFundingContext(ctx context.Context, task *Midjourney, refund bool) (*RoutingFundingOperation, error) {
	if task == nil || task.Id <= 0 {
		return nil, errors.New("invalid Midjourney finalization")
	}
	var result RoutingFundingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, task.UserId).Error; err != nil {
			return err
		}
		var stored Midjourney
		if err := tx.First(&stored, "id = ? AND user_id = ?", task.Id, task.UserId).Error; err != nil {
			return err
		}
		var submission RoutingSubmission
		found := tx.Where("local_task_id = ? AND local_task_kind = ?", task.Id, "midjourney").Find(&submission)
		if found.Error != nil {
			return found.Error
		}
		fundID := fmt.Sprintf("midjourney:%d", task.Id)
		if found.RowsAffected > 0 {
			fundID = submission.FundingID
		}
		var fund RoutingFundingSession
		lookup := tx.Where("funding_id = ?", fundID).Find(&fund)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			fund = RoutingFundingSession{FundingID: fundID, UserID: stored.UserId, TokenID: stored.TokenId, Source: "wallet", Quota: stored.Quota, Status: "reserved"}
			if err := tx.Create(&fund).Error; err != nil {
				return err
			}
		}
		kind, target := "settle", fund.Quota
		if refund {
			kind, target = "refund", 0
		}
		return prepareRoutingFundingTx(tx, RoutingFundingRequest{FundingID: fundID, OperationID: fundID + ":terminal:" + kind, UserID: fund.UserID, TokenID: fund.TokenID, Source: "wallet", Kind: kind, Quota: target, TaskID: int64(task.Id), TaskKind: "midjourney"}, nil, &result)
	})
	if err == nil && refund {
		task.Quota = 0
	}
	return &result, err
}

// FinalizeRoutingTaskFunding recovers from duplicate terminal notifications using
// the original submission/funding identity. It returns false for legacy tasks.
func FinalizeRoutingTaskFunding(task *Task, actualQuota int, refund bool) (bool, error) {
	if task == nil || task.ID <= 0 {
		return false, nil
	}
	var s RoutingSubmission
	found := DB.Where("local_task_id = ? AND (local_task_kind = ? OR local_task_kind = ? OR local_task_kind IS NULL)", task.ID, "task", "").Find(&s)
	if found.Error != nil {
		return false, found.Error
	}
	if found.RowsAffected == 0 {
		return false, nil
	}
	_, err := FinalizeTaskFunding(task, actualQuota, refund)
	return true, err
}

// CommitAcceptedRoutingTaskFunding adjusts the submit-time price while retaining
// a reservation for the eventual task terminal settlement/refund.
func CommitAcceptedRoutingTaskFunding(submissionID string, actualQuota int) error {
	return CommitAcceptedRoutingTaskFundingContext(context.Background(), submissionID, actualQuota)
}

func CommitAcceptedRoutingTaskFundingContext(ctx context.Context, submissionID string, actualQuota int) error {
	if actualQuota < 0 || actualQuota > common.MaxQuota {
		return errors.New("accepted funding quota out of range")
	}
	s, err := GetRoutingSubmissionContext(ctx, submissionID)
	if err != nil {
		return err
	}
	if s.State != "accepted" || s.LocalTaskID <= 0 {
		return ErrRoutingSubmissionConflict
	}
	var fund RoutingFundingSession
	if err := DB.WithContext(ctx).First(&fund, "funding_id = ?", s.FundingID).Error; err != nil {
		return err
	}
	if fund.Status != "reserved" {
		kind := "settle"
		if fund.Status == "refunded" {
			kind = "refund"
		} else if fund.Status != "settled" {
			return ErrRoutingFundingConflict
		}
		var terminal RoutingFundingOperation
		taskKind := s.LocalTaskKind
		if taskKind == "task" {
			taskKind = ""
		}
		if err := DB.WithContext(ctx).Where("operation_id = ? AND funding_id = ? AND task_id = ? AND task_kind = ?", fund.FundingID+":terminal:"+kind, fund.FundingID, s.LocalTaskID, taskKind).First(&terminal).Error; err != nil {
			return err
		}
		return nil // a durable terminal operation already superseded submit-time pricing
	}
	_, err = PrepareRoutingFundingContext(ctx, RoutingFundingRequest{FundingID: fund.FundingID, OperationID: fund.FundingID + ":accepted", UserID: fund.UserID, TokenID: fund.TokenID, Source: fund.Source, Kind: "accepted", Quota: actualQuota, TaskID: s.LocalTaskID, TaskKind: s.LocalTaskKind}, nil)
	return err
}

// FinalizeTaskFunding also imports the already-debited marker of historical
// tasks, without charging it again. The first terminal operation locks the
// wallet and uses the persisted quota, not a caller's possibly stale Task copy.
func FinalizeTaskFunding(task *Task, actualQuota int, refund bool) (*RoutingFundingOperation, error) {
	return FinalizeTaskFundingContext(context.Background(), task, actualQuota, refund)
}

func FinalizeTaskFundingContext(ctx context.Context, task *Task, actualQuota int, refund bool) (*RoutingFundingOperation, error) {
	if task == nil || task.ID <= 0 || actualQuota < 0 || actualQuota > common.MaxQuota {
		return nil, errors.New("invalid task funding finalization")
	}
	kind := "settle"
	if refund {
		kind, actualQuota = "refund", 0
	}
	var result RoutingFundingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, task.UserId).Error; err != nil {
			return err
		}
		var persisted Task
		if err := tx.First(&persisted, "id = ? AND user_id = ?", task.ID, task.UserId).Error; err != nil {
			return err
		}
		var submission RoutingSubmission
		found := tx.Where("local_task_id = ? AND (local_task_kind = ? OR local_task_kind = ? OR local_task_kind IS NULL)", task.ID, "task", "").Find(&submission)
		if found.Error != nil {
			return found.Error
		}
		fundID := fmt.Sprintf("task:%d", task.ID)
		if found.RowsAffected > 0 {
			fundID = submission.FundingID
		}
		var fund RoutingFundingSession
		lookup := tx.Where("funding_id = ?", fundID).Find(&fund)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected == 0 {
			source := "wallet"
			if persisted.PrivateData.BillingSource == "subscription" && persisted.PrivateData.SubscriptionId > 0 {
				source = "subscription"
			}
			fund = RoutingFundingSession{FundingID: fundID, UserID: persisted.UserId, TokenID: persisted.PrivateData.TokenId, Source: source, SubscriptionID: persisted.PrivateData.SubscriptionId, Quota: persisted.Quota, Status: "reserved"}
			if err := tx.Create(&fund).Error; err != nil {
				return err
			}
		}
		return prepareRoutingFundingTx(tx, RoutingFundingRequest{FundingID: fundID, OperationID: fundID + ":terminal:" + kind, UserID: fund.UserID, TokenID: fund.TokenID, Source: fund.Source, Kind: kind, Quota: actualQuota, TaskID: task.ID}, nil, &result)
	})
	if err == nil {
		task.Quota = actualQuota
	}
	return &result, err
}
