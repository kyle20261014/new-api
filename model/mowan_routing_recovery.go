package model

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrRoutingTerminalUnconfirmed = errors.New("routing task terminal is not confirmed by upstream; reservation and observation retained")

const RoutingRecoveryPageSize = 64

// FinishRoutingRecoveryTask uses the existing system-task lock contract on the
// caller's bounded DB context. It has no payload and releases only its own lock.
func (s RoutingMetricsStore) FinishRoutingRecoveryTask(taskID, owner string, status SystemTaskStatus, errorMessage string) error {
	if s.DB == nil {
		return ErrRoutingMetricsUnavailable
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		result := tx.Model(&SystemTask{}).
			Where("task_id = ? AND status = ? AND locked_by = ?", taskID, SystemTaskStatusRunning, owner).
			Where("EXISTS (SELECT 1 FROM system_task_locks WHERE system_task_locks.task_id = system_tasks.task_id AND system_task_locks.locked_by = ? AND system_task_locks.locked_until >= ?)", owner, now).
			Updates(map[string]interface{}{"status": status, "active_key": nil, "result": "", "error": errorMessage, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrSystemTaskLockLost
		}
		return tx.Where("task_id = ? AND locked_by = ?", taskID, owner).Delete(&SystemTaskLock{}).Error
	})
}

// RoutingTaskTerminalReceipt is server-owned provenance. Only a parsed upstream
// terminal response or durable accepted immediate result may create this marker;
// local timeout/query failures must leave it unset.
type RoutingTaskTerminalReceipt struct {
	Status     string `json:"status"`
	ObservedAt int64  `json:"observed_at"`
}

func (r *RoutingTaskTerminalReceipt) Scan(value interface{}) error {
	return common.Unmarshal(jsonScanBytes(value), r)
}

func (r RoutingTaskTerminalReceipt) Value() (driver.Value, error) {
	b, err := common.Marshal(r)
	return string(b), err
}

func NewRoutingTerminalReceipt(tx *gorm.DB, status string) (*RoutingTaskTerminalReceipt, error) {
	if status != TaskStatusSuccess && status != TaskStatusFailure {
		return nil, errors.New("routing receipt requires a real upstream terminal status")
	}
	n, err := routingDBNow(tx, time.Time{})
	if err != nil {
		return nil, err
	}
	return &RoutingTaskTerminalReceipt{Status: status, ObservedAt: n}, nil
}

// UpdateWithUpstreamStatus is reserved for a parsed provider terminal response.
// The status winner and its receipt commit together; local failures use the
// ordinary UpdateWithStatus and cannot manufacture terminal health evidence.
func (t *Task) UpdateWithUpstreamStatus(ctx context.Context, fromStatus TaskStatus) (bool, error) {
	return t.updateRoutingStatus(ctx, fromStatus, true, false)
}

// UpdateWithStatusAndQuota preserves the legacy timeout's explicit quota-marker
// clearing. Ordinary polling must not overwrite quota owned by funding journals.
func (t *Task) UpdateWithStatusAndQuota(ctx context.Context, fromStatus TaskStatus) (bool, error) {
	return t.updateRoutingStatus(ctx, fromStatus, false, true)
}

func (t *Task) updateRoutingStatus(ctx context.Context, fromStatus TaskStatus, upstream, writeQuota bool) (bool, error) {
	if DB == nil {
		return false, ErrRoutingMetricsUnavailable
	}
	if t == nil || t.ID <= 0 {
		return false, errors.New("routing task CAS requires a persisted task")
	}
	won := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored Task
		if err := lockForUpdate(tx).Select("id", "private_data").First(&stored, "id = ? AND status = ?", t.ID, fromStatus).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		copy := *t
		copy.PrivateData.BillingSource = stored.PrivateData.BillingSource
		copy.PrivateData.SubscriptionId = stored.PrivateData.SubscriptionId
		copy.PrivateData.TokenId = stored.PrivateData.TokenId
		copy.PrivateData.RoutingTerminal = stored.PrivateData.RoutingTerminal
		if upstream {
			receipt, err := NewRoutingTerminalReceipt(tx, string(t.Status))
			if err != nil {
				return err
			}
			copy.PrivateData.RoutingTerminal = receipt
		}
		query := tx.Model(&copy).Where("status = ?", fromStatus).Select("*")
		if !writeQuota {
			query = query.Omit("quota")
		}
		result := query.Updates(&copy)
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		if won {
			t.PrivateData = copy.PrivateData
		}
		return nil
	})
	return won && err == nil, err
}

// RoutingRecoveryPage uses a stable keyset, including unknown rows without a
// local task. The caller keeps the cursor across bounded system-task runs.
func (s RoutingMetricsStore) RoutingRecoveryPage(after string, limit int) ([]RoutingSubmission, error) {
	if s.DB == nil {
		return nil, ErrRoutingMetricsUnavailable
	}
	if limit < 1 || limit > RoutingRecoveryPageSize {
		return nil, errors.New("routing recovery: invalid page size")
	}
	var rows []RoutingSubmission
	active := s.DB.Model(&RoutingTerminalObservation{}).Select("submission_id").Where("status IN ?", []string{"accepted", "unknown"})
	reserved := s.DB.Model(&RoutingFundingSession{}).Select("funding_id").Where("status = ?", "reserved")
	err := s.DB.Where("submission_id > ? AND (state IN ? OR (state IN ? AND funding_id IN (?)) OR (state = ? AND submission_id IN (?)))", after,
		[]string{"prepared", "sending", "unknown"}, []string{"accepted", "not_sent", "rejected"}, reserved, "accepted", active).
		Order("submission_id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// RoutingRecoveryProbePage prioritizes probes eligible for observer takeover.
// Expired probes are included so their epoch is invalidated and alarmed even
// when no new user request reads health.
func (s RoutingMetricsStore) RoutingRecoveryProbePage(after string, limit int, now time.Time) ([]RoutingHealth, error) {
	if limit < 1 || limit > 128 {
		return nil, errors.New("routing recovery: invalid observer page size")
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return nil, err
	}
	var rows []RoutingHealth
	err = s.DB.Where("id > ? AND stage = ? AND state = ? AND (observer_owner = ? OR observer_until <= ? OR probe_until <= ? OR probe_started_at <= ?)", after, RoutingStageTaskTerminal, RoutingHealthHalfOpen, "", n, n, n-86400).
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s RoutingMetricsStore) ExpireRoutingRecoveryProbe(k RoutingMetricKey, now time.Time) (RoutingHealth, bool, error) {
	expired := false
	h, err := s.mutateHealth(k, RoutingStageTaskTerminal, now, func(_ *gorm.DB, h *RoutingHealth, n int64) error {
		epoch := h.BreakerEpoch
		h.expireProbe(n)
		expired = epoch != h.BreakerEpoch
		return nil
	})
	return h, expired, err
}

func routingTaskSeconds(n int64) int64 {
	// Midjourney timestamps use milliseconds; ordinary Task uses seconds.
	if n > 100000000000 {
		return n / 1000
	}
	return n
}

// ObserveRoutingTerminal reads the real typed task and commits the terminal
// dedup event, health transition and long-term bucket in one primary-DB
// transaction. A crash cannot leave a consumed event without its metric.
func (s RoutingMetricsStore) ObserveRoutingTerminal(submissionID, nodeBootID string, observer *RoutingProbeObserver, now time.Time) (bool, error) {
	if s.DB == nil {
		return false, ErrRoutingMetricsUnavailable
	}
	if submissionID == "" || nodeBootID == "" || len(nodeBootID) > 128 {
		return false, errors.New("routing recovery: invalid terminal identity")
	}
	recorded := false
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var submission RoutingSubmission
		if err := tx.First(&submission, "submission_id = ?", submissionID).Error; err != nil {
			return err
		}
		if submission.State != "accepted" || submission.LocalTaskID <= 0 {
			return nil
		}
		var observation RoutingTerminalObservation
		if err := tx.First(&observation, "submission_id = ?", submissionID).Error; err != nil {
			return err
		}
		if observation.Status == "terminal" {
			return nil
		}
		var status string
		var receipt *RoutingTaskTerminalReceipt
		var submit, finish int64
		switch submission.LocalTaskKind {
		case "midjourney":
			var task Midjourney
			if err := tx.First(&task, "id = ? AND user_id = ? AND channel_id = ? AND mj_id = ?", submission.LocalTaskID, submission.UserID, submission.ChannelID, submission.UpstreamTaskID).Error; err != nil {
				return err
			}
			if task.MjId != observation.TaskID {
				return ErrRoutingMetricsConflict
			}
			status, submit, finish, receipt = task.Status, task.SubmitTime, task.FinishTime, task.RoutingTerminal
		case "", "task":
			var task Task
			if err := tx.First(&task, "id = ? AND user_id = ? AND channel_id = ?", submission.LocalTaskID, submission.UserID, submission.ChannelID).Error; err != nil {
				return err
			}
			if task.Group != submission.BusinessGroup || task.TaskID != observation.TaskID || task.GetUpstreamTaskID() != submission.UpstreamTaskID {
				return ErrRoutingMetricsConflict
			}
			status, submit, finish, receipt = string(task.Status), task.SubmitTime, task.FinishTime, task.PrivateData.RoutingTerminal
		default:
			return fmt.Errorf("routing recovery: unsupported local task kind %q", submission.LocalTaskKind)
		}
		if status != TaskStatusSuccess && status != TaskStatusFailure {
			return nil
		}
		if receipt == nil || receipt.Status != status || receipt.ObservedAt <= 0 {
			return ErrRoutingTerminalUnconfirmed
		}
		n, err := routingDBNow(tx, now)
		if err != nil {
			return err
		}
		finish, submit = routingTaskSeconds(finish), routingTaskSeconds(submit)
		if finish <= 0 {
			finish = receipt.ObservedAt
		}
		if submit <= 0 {
			submit = submission.CreatedAt
		}
		if finish > n+60 || submit > finish {
			return ErrRoutingTerminalUnconfirmed
		}
		outcome := "success"
		if status == TaskStatusFailure {
			outcome = "failure"
		}
		input := RoutingHealthInput{EventID: routingMetricID("terminal", submissionID), Key: submission.metricKey(), Stage: RoutingStageTaskTerminal, OccurredAt: time.Unix(receipt.ObservedAt, 0), Outcome: outcome, Sent: true, SubmissionID: submissionID}
		if observation.ProbeLeaseID != "" {
			input.Probe = &RoutingProbeClaim{Key: input.Key, Stage: input.Stage, Epoch: observation.BreakerEpoch, LeaseID: observation.ProbeLeaseID}
		}
		if observer != nil {
			if observer.SubmissionID != submissionID || input.Probe == nil || observer.Claim.Key != input.Key || observer.Claim.Epoch != input.Probe.Epoch || observer.Claim.LeaseID != input.Probe.LeaseID {
				return ErrRoutingMetricsConflict
			}
			input.ObserverOwner, input.ObserverGeneration = observer.Owner, observer.Generation
		}
		if outcome == "success" {
			if err := tx.Model(&RoutingTerminalObservation{}).Where("variant_id = ? AND status IN ?", observation.VariantID, []string{"accepted", "unknown"}).Count(&input.SuccessActive).Error; err != nil {
				return err
			}
		}
		result, err := (RoutingMetricsStore{DB: tx}).ApplyHealthEvent(input, now)
		if err != nil || result.Duplicate {
			return err
		}
		bucket := RoutingMetricBucket{RoutingMetricKey: input.Key, Stage: input.Stage, VariantID: observation.VariantID, NodeBootID: nodeBootID, ChannelID: submission.ChannelID, Minute: receipt.ObservedAt / 60 * 60}
		bucket.ID = routingMetricID(nodeBootID, bucket.VariantID, fmt.Sprint(bucket.ChannelID), fmt.Sprint(bucket.Minute))
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&bucket).Error; err != nil {
			return err
		}
		changes := map[string]interface{}{"revision": gorm.Expr("revision + 1"), "eligible": gorm.Expr("eligible + 1"), "sent": gorm.Expr("sent + 1"), "updated_at": n,
			"last_observation_at": gorm.Expr("CASE WHEN last_observation_at < ? THEN ? ELSE last_observation_at END", receipt.ObservedAt, receipt.ObservedAt)}
		changes[outcome] = gorm.Expr(outcome + " + 1")
		if outcome == "success" && submit > 0 {
			changes["speed_samples"] = gorm.Expr("speed_samples + 1")
			changes["speed_total_ms"] = gorm.Expr("speed_total_ms + ?", float64(finish-submit)*1000)
		}
		if err := tx.Model(&RoutingMetricBucket{}).Where("id = ?", bucket.ID).Updates(changes).Error; err != nil {
			return err
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("routing terminal submission=%s: %w", strings.TrimSpace(submissionID), err)
	}
	return recorded, nil
}
