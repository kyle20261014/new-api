package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// DiagnosticRun and DiagnosticStep deliberately keep credentials out of task payloads.
// Public responses are constructed separately; these rows must never be serialized.
type DiagnosticRun struct {
	ID              string `gorm:"type:varchar(64);primaryKey"`
	UserID          int    `gorm:"uniqueIndex:idx_diagnostic_submission"`
	SubmissionKey   string `gorm:"type:varchar(64);uniqueIndex:idx_diagnostic_submission"`
	SessionID       string `gorm:"type:varchar(128)"`
	UserAuthVersion int64
	SessionVersion  int64
	ModelName       string `gorm:"type:varchar(255);index"`
	GroupName       string `gorm:"type:varchar(64)"`
	Kind            string `gorm:"type:varchar(32)"`
	Creative        bool
	Version         string  `gorm:"type:varchar(128)"`
	ScopeKey        string  `gorm:"type:varchar(64);index"`
	ActiveKey       *string `gorm:"type:varchar(64);uniqueIndex"`
	State           string  `gorm:"type:varchar(32);index"`
	PlanJSON        string
	ResultJSON      string
	ConfigHash      string `gorm:"type:varchar(64)"`
	LeaseOwner      string `gorm:"type:varchar(64)"`
	LeaseUntil      int64  `gorm:"index"`
	CreatedAt       int64
	UpdatedAt       int64
	CompletedAt     int64
}

// Each submitter retains an idempotency alias even when joining another user's
// active shared run. Without this row, repeating that key after completion pays again.
type DiagnosticSubmission struct {
	UserID int    `gorm:"primaryKey;autoIncrement:false"`
	Key    string `gorm:"column:submission_key;type:varchar(64);primaryKey"`
	RunID  string `gorm:"type:varchar(64);index"`
}

func (DiagnosticSubmission) TableName() string { return "mowan_diagnostic_submissions" }

var ErrDiagnosticSubmissionConflict = errors.New("diagnostic submission key conflicts with original request")

func (DiagnosticRun) TableName() string { return "mowan_diagnostic_runs" }

type DiagnosticStep struct {
	ID              string `gorm:"type:varchar(64);primaryKey"`
	RunID           string `gorm:"type:varchar(64);uniqueIndex:idx_diagnostic_step"`
	Ordinal         int    `gorm:"uniqueIndex:idx_diagnostic_step"`
	RequestID       string `gorm:"type:varchar(64);uniqueIndex"`
	FundingID       string `gorm:"type:varchar(128);index"`
	State           string `gorm:"type:varchar(32)"`
	Answer          string
	Complete        bool
	ErrorCode       string `gorm:"type:varchar(128)"`
	SourceHash      string `gorm:"type:varchar(64)"`
	BillingState    string `gorm:"type:varchar(32)"`
	ChannelID       int
	InputTokens     *int
	OutputTokens    *int
	ReasoningTokens *int
	DurationMS      int64
	CreatedAt       int64
	UpdatedAt       int64
}

func (DiagnosticStep) TableName() string { return "mowan_diagnostic_steps" }

func MigrateModelDiagnostics(db *gorm.DB) error {
	if db == nil {
		return errors.New("diagnostics database unavailable")
	}
	return db.AutoMigrate(&DiagnosticRun{}, &DiagnosticStep{}, &DiagnosticSubmission{})
}

// CreateDiagnosticRun's unique keys arbitrate both replay and cross-user shared
// work. The losing transaction reads the winner instead of sending another call.
func CreateDiagnosticRun(run *DiagnosticRun, steps []DiagnosticStep) (*DiagnosticRun, error) {
	var selected DiagnosticRun
	create := func(tx *gorm.DB) error {
		selected = DiagnosticRun{}
		var alias DiagnosticSubmission
		err := tx.First(&alias, "user_id = ? AND submission_key = ?", run.UserID, run.SubmissionKey).Error
		if err == nil {
			if err := tx.First(&selected, "id = ?", alias.RunID).Error; err != nil {
				return err
			}
			if selected.ModelName != run.ModelName || selected.GroupName != run.GroupName || selected.Kind != run.Kind || selected.Creative != run.Creative {
				return ErrDiagnosticSubmissionConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		err = tx.First(&selected, "active_key = ?", run.ActiveKey).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(run).Error; err != nil {
				return err
			}
			if err := tx.Create(&steps).Error; err != nil {
				return err
			}
			selected = *run
		} else if err != nil {
			return err
		}
		return tx.Create(&DiagnosticSubmission{UserID: run.UserID, Key: run.SubmissionKey, RunID: selected.ID}).Error
	}
	err := DB.Transaction(create)
	if err != nil && !errors.Is(err, ErrDiagnosticSubmissionConflict) {
		// Retry a uniqueness race once against the now-visible winning rows. No
		// generation or funding happens until this transaction commits.
		err = DB.Transaction(create)
	}
	return &selected, err
}

func GetDiagnosticRun(id string) (*DiagnosticRun, error) {
	var run DiagnosticRun
	err := DB.First(&run, "id = ?", id).Error
	return &run, err
}

func GetDiagnosticSteps(id string) ([]DiagnosticStep, error) {
	steps := []DiagnosticStep{}
	err := DB.Where("run_id = ?", id).Order("ordinal asc").Find(&steps).Error
	return steps, err
}

func ClaimDiagnosticRun(id, owner string, now int64) (*DiagnosticRun, error) {
	var run DiagnosticRun
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&run, "id = ?", id).Error; err != nil {
			return err
		}
		if run.State != "queued" && !(run.State == "running" && run.LeaseUntil < now) {
			return errors.New("diagnostic run already claimed")
		}
		if run.State == "running" {
			var count int64
			if err := tx.Model(&DiagnosticStep{}).Where("run_id = ? AND state IN ?", id, []string{"funding", "sending"}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				if err := tx.Model(&DiagnosticStep{}).Where("run_id = ? AND state IN ?", id, []string{"funding", "sending"}).Update("state", "review_pending").Error; err != nil {
					return err
				}
				run.State = "review_pending"
				// Retain ActiveKey: uncertain paid work cannot silently be replaced.
				return tx.Model(&run).Updates(map[string]any{"state": run.State, "lease_owner": "", "lease_until": 0, "updated_at": now}).Error
			}
		}
		run.State, run.LeaseOwner, run.LeaseUntil = "running", owner, now+120
		return tx.Model(&run).Updates(map[string]any{"state": run.State, "lease_owner": owner, "lease_until": run.LeaseUntil, "updated_at": now}).Error
	})
	return &run, err
}

func RenewDiagnosticLease(id, owner string) bool {
	r := DB.Model(&DiagnosticRun{}).Where("id = ? AND state = ? AND lease_owner = ?", id, "running", owner).
		Updates(map[string]any{"lease_until": time.Now().Unix() + 120, "updated_at": time.Now().Unix()})
	return r.Error == nil && r.RowsAffected == 1
}

func UpdateDiagnosticStep(runID, owner, stepID string, values map[string]any) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var run DiagnosticRun
		if err := lockForUpdate(tx).First(&run, "id = ? AND state = ? AND lease_owner = ? AND lease_until > ?", runID, "running", owner, time.Now().Unix()).Error; err != nil {
			return err
		}
		values["updated_at"] = time.Now().Unix()
		r := tx.Model(&DiagnosticStep{}).Where("id = ? AND run_id = ?", stepID, runID).Updates(values)
		if r.Error == nil && r.RowsAffected != 1 {
			return errors.New("diagnostic step missing")
		}
		return r.Error
	})
}

func FinishDiagnosticRun(id, owner, state, result string) error {
	updates := map[string]any{"state": state, "result_json": result, "updated_at": time.Now().Unix(), "completed_at": time.Now().Unix(), "lease_owner": "", "lease_until": 0}
	if state != "review_pending" {
		updates["active_key"] = nil
	}
	r := DB.Model(&DiagnosticRun{}).Where("id = ? AND lease_owner = ? AND state = ?", id, owner, "running").Updates(updates)
	if r.Error == nil && r.RowsAffected != 1 {
		return errors.New("diagnostic ownership lost")
	}
	return r.Error
}
