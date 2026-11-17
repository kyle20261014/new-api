package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/google/uuid"
)

const (
	// ponytail: 128 observers per boot; add master workers or raise only after DB capacity measurement.
	routingRecoveryObserverLimit = 128
	routingRecoveryDBBudget      = 2 * time.Second
	routingRecoveryRunDuration   = 55 * time.Second
	routingRecoveryHeartbeat     = 5 * time.Second
	routingMetricsRetentionType  = "routing_metrics_retention"
)

type routingRecoveryHandler struct {
	bootID           string
	submissionCursor string
	probeCursor      string
	observers        map[string]model.RoutingProbeObserver
	beat             uint64
}

var registerRoutingRecoveryOnce sync.Once

// RegisterRoutingRecoveryTasks must run before StartSystemTaskRunner. A boot-
// scoped type lets every live master use the existing scheduler: a dead node's
// 60s system-task lock cannot block the 15s terminal-observer takeover.
func RegisterRoutingRecoveryTasks() {
	registerRoutingRecoveryOnce.Do(func() {
		RegisterSystemTaskHandler(&routingRecoveryHandler{bootID: uuid.NewString(), observers: make(map[string]model.RoutingProbeObserver)})
		RegisterSystemTaskHandler(routingMetricsRetentionHandler{})
	})
}

func (h *routingRecoveryHandler) Type() string          { return "routing_recovery:" + h.bootID }
func (*routingRecoveryHandler) Enabled() bool           { return true }
func (*routingRecoveryHandler) Interval() time.Duration { return time.Second }
func (*routingRecoveryHandler) NewPayload() any         { return nil }

func (h *routingRecoveryHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	runCtx, cancel := context.WithTimeout(ctx, routingRecoveryRunDuration)
	defer cancel()
	ticker := time.NewTicker(routingRecoveryHeartbeat)
	defer ticker.Stop()
	for {
		h.step(runCtx)
		select {
		case <-runCtx.Done():
			// Renew the probe before releasing observation ownership. The next
			// scheduler pass is at most 15s away, inside its fresh 30s lease.
			h.releaseObservers()
			if ctx.Err() != nil {
				finishRoutingRecoveryTask(task, runnerID, ctx.Err())
				return
			}
			finishRoutingRecoveryTask(task, runnerID, nil)
			return
		case <-ticker.C:
		}
	}
}

func routingRecoveryCall(ctx context.Context, fn func(context.Context, model.RoutingMetricsStore) error) error {
	if model.DB == nil {
		return model.ErrRoutingMetricsUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, routingRecoveryDBBudget)
	defer cancel()
	return fn(bounded, model.RoutingMetricsStore{DB: model.DB.WithContext(bounded)})
}

func (h *routingRecoveryHandler) step(ctx context.Context) {
	h.beat++
	if err := routingRecoveryCall(ctx, func(_ context.Context, store model.RoutingMetricsStore) error {
		heartbeatErr := h.heartbeat(store, h.beat%2 == 0)
		return errors.Join(heartbeatErr, h.claimObservers(store))
	}); err != nil {
		logger.LogWarn(ctx, common.LogText("routing recovery observer heartbeat/scan: %v", err))
	}
	if err := routingRecoveryCall(ctx, func(bounded context.Context, store model.RoutingMetricsStore) error {
		return h.recoverPage(bounded, store)
	}); err != nil {
		logger.LogWarn(ctx, common.LogText("routing recovery submission scan: %v", err))
	}
}

func (h *routingRecoveryHandler) heartbeat(store model.RoutingMetricsStore, renewProbe bool) error {
	var failures []error
	for id, observer := range h.observers {
		ok, err := store.RenewTerminalObserver(observer, renewProbe, time.Time{})
		if err != nil {
			failures = append(failures, fmt.Errorf("submission=%s renew: %w", id, err))
			continue
		}
		if !ok {
			delete(h.observers, id)
			current, _, expiryErr := store.ExpireRoutingRecoveryProbe(observer.Claim.Key, time.Time{})
			if expiryErr != nil {
				failures = append(failures, expiryErr)
			}
			logger.LogWarn(store.DB.Statement.Context, common.LogText("routing terminal observer lost or probe expired: submission=%s epoch=%d generation=%d reason=%s", id, observer.Claim.Epoch, observer.Generation, current.LastProbeExpiryReason))
			continue
		}
		if renewProbe {
			recorded, err := store.ObserveRoutingTerminal(id, h.bootID, &observer, time.Time{})
			if err != nil {
				failures = append(failures, err)
			} else if recorded {
				delete(h.observers, id)
			}
		}
	}
	return errors.Join(failures...)
}

func (h *routingRecoveryHandler) claimObservers(store model.RoutingMetricsStore) error {
	rows, err := store.RoutingRecoveryProbePage(h.probeCursor, routingRecoveryObserverLimit, time.Time{})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		h.probeCursor = ""
		return nil
	}
	for _, row := range rows {
		h.probeCursor = row.ID
		current, expired, err := store.ExpireRoutingRecoveryProbe(row.RoutingMetricKey, time.Time{})
		if err != nil {
			return err
		}
		if expired {
			logger.LogWarn(store.DB.Statement.Context, common.LogText("routing terminal probe expired: submission=%s reason=%s epoch=%d; original task and funds retained", row.ProbeSubmissionID, current.LastProbeExpiryReason, row.BreakerEpoch))
			continue
		}
		if row.ProbeSubmissionID == "" {
			continue
		}
		if len(h.observers) >= routingRecoveryObserverLimit {
			return fmt.Errorf("routing terminal observer capacity reached: limit=%d; unclaimed probes remain durable and may expire", routingRecoveryObserverLimit)
		}
		if _, exists := h.observers[row.ProbeSubmissionID]; exists {
			continue
		}
		claim := model.RoutingProbeClaim{Key: row.RoutingMetricKey, Stage: model.RoutingStageTaskTerminal, Epoch: row.BreakerEpoch, LeaseID: row.ProbeLeaseID, Until: row.ProbeUntil}
		observer, ok, err := store.ClaimTerminalObserver(claim, row.ProbeSubmissionID, h.bootID, time.Time{})
		if err != nil {
			return err
		}
		if ok {
			h.observers[row.ProbeSubmissionID] = observer
			// Acceptance may have arrived near the original probe deadline.
			// Extend immediately rather than waiting for the first 10s beat.
			if renewed, err := store.RenewTerminalObserver(observer, true, time.Time{}); err != nil {
				return err
			} else if !renewed {
				delete(h.observers, row.ProbeSubmissionID)
				return fmt.Errorf("routing terminal probe expired during observer claim: submission=%s", row.ProbeSubmissionID)
			}
		}
	}
	return nil
}

func (h *routingRecoveryHandler) recoverPage(ctx context.Context, store model.RoutingMetricsStore) error {
	rows, err := store.RoutingRecoveryPage(h.submissionCursor, model.RoutingRecoveryPageSize)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		h.submissionCursor = ""
		return nil
	}
	var failures []error
	for i := range rows {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		row := &rows[i]
		h.submissionCursor = row.SubmissionID
		if err := h.recoverSubmission(ctx, store, row); err != nil {
			failures = append(failures, fmt.Errorf("submission=%s state=%s: %w", row.SubmissionID, row.State, err))
		}
	}
	return errors.Join(failures...)
}

func (h *routingRecoveryHandler) recoverSubmission(ctx context.Context, store model.RoutingMetricsStore, row *model.RoutingSubmission) error {
	switch row.State {
	case "prepared", "sending", "not_sent", "rejected":
		_, err := model.RecoverExpiredRoutingSubmission(ctx, row.SubmissionID, row.Version)
		if errors.Is(err, model.ErrRoutingSubmissionConflict) {
			return nil // a live sender or another recovery owns the CAS
		}
		return err
	case "unknown":
		return errors.New("provider safe query unavailable; manual reconciliation required; reservation retained, no upstream create")
	case "accepted":
		var recovery model.RoutingTaskRecovery
		if row.RecoveryJSON == "" || len(row.RecoveryJSON) > 1<<20 {
			return errors.New("accepted recovery material unavailable; original reservation retained")
		}
		if err := common.UnmarshalJsonStr(row.RecoveryJSON, &recovery); err != nil {
			return fmt.Errorf("accepted recovery material: %w", err)
		}
		var quota int
		if recovery.Midjourney != nil {
			task, err := model.PersistAcceptedRoutingMidjourneyContext(ctx, row.SubmissionID)
			if err != nil {
				return err
			}
			quota = recovery.Midjourney.Quota
			if err = model.CommitAcceptedRoutingTaskFundingContext(ctx, row.SubmissionID, quota); err != nil {
				return err
			}
			if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
				if _, err = model.FinalizeMidjourneyFundingContext(ctx, task, task.Status == model.TaskStatusFailure); err != nil {
					return err
				}
			}
		} else {
			task, err := model.PersistAcceptedRoutingTaskContext(ctx, row.SubmissionID)
			if err != nil {
				return err
			}
			quota = recovery.Task.Quota
			if err = model.CommitAcceptedRoutingTaskFundingContext(ctx, row.SubmissionID, quota); err != nil {
				return err
			}
			if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
				if _, err = model.FinalizeTaskFundingContext(ctx, task, task.Quota, task.Status == model.TaskStatusFailure); err != nil {
					return err
				}
			}
		}
		var observer *model.RoutingProbeObserver
		if owned, ok := h.observers[row.SubmissionID]; ok {
			observer = &owned
		}
		recorded, err := store.ObserveRoutingTerminal(row.SubmissionID, h.bootID, observer, time.Time{})
		if recorded {
			delete(h.observers, row.SubmissionID)
		}
		return err
	}
	return nil
}

func (h *routingRecoveryHandler) releaseObservers() {
	if err := routingRecoveryCall(context.Background(), func(_ context.Context, store model.RoutingMetricsStore) error {
		var failures []error
		for id, observer := range h.observers {
			_, renewErr := store.RenewTerminalObserver(observer, true, time.Time{})
			_, releaseErr := store.ReleaseTerminalObserver(observer, time.Time{})
			if err := errors.Join(renewErr, releaseErr); err != nil {
				failures = append(failures, fmt.Errorf("submission=%s release: %w", id, err))
			}
		}
		return errors.Join(failures...)
	}); err != nil {
		logger.LogWarn(context.Background(), common.LogText("routing recovery observer handoff: %v", err))
	}
	clear(h.observers)
}

type routingMetricsRetentionHandler struct{}

func (routingMetricsRetentionHandler) Type() string            { return routingMetricsRetentionType }
func (routingMetricsRetentionHandler) Enabled() bool           { return true }
func (routingMetricsRetentionHandler) Interval() time.Duration { return time.Hour }
func (routingMetricsRetentionHandler) NewPayload() any         { return nil }
func (routingMetricsRetentionHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	err := routingRecoveryCall(ctx, func(_ context.Context, store model.RoutingMetricsStore) error {
		return store.Cleanup(time.Time{})
	})
	if err != nil {
		finishRoutingRecoveryTask(task, runnerID, fmt.Errorf("routing metrics 48h retention: %w", err))
		return
	}
	finishRoutingRecoveryTask(task, runnerID, nil)
}

func finishRoutingRecoveryTask(task *model.SystemTask, runnerID string, runErr error) {
	status, message := model.SystemTaskStatusSucceeded, ""
	if runErr != nil {
		status, message = model.SystemTaskStatusFailed, runErr.Error()
		logger.LogWarn(context.Background(), common.LogText("routing system task %s failed: %v", task.TaskID, runErr))
	}
	if err := routingRecoveryCall(context.Background(), func(_ context.Context, store model.RoutingMetricsStore) error {
		return store.FinishRoutingRecoveryTask(task.TaskID, runnerID, status, message)
	}); err != nil {
		logger.LogWarn(context.Background(), common.LogText("routing system task %s failed to save result: %v", task.TaskID, err))
	}
}

var _ ScheduledSystemTaskHandler = (*routingRecoveryHandler)(nil)
var _ ScheduledSystemTaskHandler = routingMetricsRetentionHandler{}
