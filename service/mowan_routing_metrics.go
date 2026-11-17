package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const routingAttemptTraceKey = "mowan_routing_attempt_observation"

// The transport reports an actual upstream outcome. Local permission, budget,
// validation and cancellation failures are ignored, not provider failures.
type RoutingAttemptResult struct {
	Outcome       string
	RateLimited   bool
	Latency       time.Duration
	TTFT          time.Duration
	Probe         *model.RoutingProbeClaim
	SuccessActive int64
}

type routingAttemptTrace struct {
	mu       sync.Mutex
	current  string
	attempts map[string]*routingAttemptObservation
}
type routingAttemptObservation struct {
	mu                sync.Mutex
	key               model.RoutingMetricKey
	channelID         int
	eventID           string
	started           time.Time
	firstData         time.Duration
	hasFirstData      bool
	finished          bool
	startErr          error
	finishErr         error
	stop              chan struct{}
	db                *gorm.DB
	recorder          *routingMetricsRecorder
	probe             *model.RoutingProbeClaim
	submissionID      string
	sendOwnerID       string
	sendLeaseDone     bool
	terminalProbe     *model.RoutingProbeClaim
	terminalLeaseDone bool
}
type routingSnapshotKey struct {
	db        *gorm.DB
	key       model.RoutingMetricKey
	channelID int
	minute    int64
}
type routingMetricsRecorder struct {
	mu     sync.Mutex
	bootID string
	// ponytail: retain 48h cumulative buckets in memory; use a local spool if
	// observed variant cardinality exceeds the process memory budget.
	buckets                   map[routingSnapshotKey]model.RoutingMetricBucket
	flushed                   map[routingSnapshotKey]int64
	now                       func() time.Time
	loopCtx                   context.Context
	stopLoop                  context.CancelFunc
	loopDone                  chan struct{}
	loopStarted               bool
	closed                    bool
	active                    int
	drained                   chan struct{}
	heartbeatStop             chan struct{}
	heartbeatStopOnce         sync.Once
	renewSubmissionSendLease  func(context.Context, string, string) (bool, error)
	renewTerminalSendingProbe func(context.Context, string, string, model.RoutingProbeClaim) (bool, error)
}

var routingTraceCreation sync.Mutex
var routingAttemptRecorder = newRoutingMetricsRecorder()

func newRoutingMetricsRecorder() *routingMetricsRecorder {
	ctx, cancel := context.WithCancel(context.Background())
	return &routingMetricsRecorder{bootID: uuid.NewString(), buckets: make(map[routingSnapshotKey]model.RoutingMetricBucket), flushed: make(map[routingSnapshotKey]int64), now: time.Now, loopCtx: ctx, stopLoop: cancel, loopDone: make(chan struct{}), drained: make(chan struct{}), heartbeatStop: make(chan struct{}), renewSubmissionSendLease: model.RenewRoutingSubmissionSendLease, renewTerminalSendingProbe: model.RenewRoutingTerminalSendingProbe}
}
func routingTrace(c *gin.Context, create bool) *routingAttemptTrace {
	if c == nil {
		return nil
	}
	routingTraceCreation.Lock()
	defer routingTraceCreation.Unlock()
	if value, ok := c.Get(routingAttemptTraceKey); ok {
		if trace, ok := value.(*routingAttemptTrace); ok {
			return trace
		}
	}
	if !create {
		return nil
	}
	trace := &routingAttemptTrace{attempts: make(map[string]*routingAttemptObservation)}
	c.Set(routingAttemptTraceKey, trace)
	return trace
}
func routingCurrentObservation(c *gin.Context) *routingAttemptObservation {
	trace := routingTrace(c, false)
	if trace == nil {
		return nil
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.attempts[trace.current]
}
func routingMetricsCall(db *gorm.DB, fn func(model.RoutingMetricsStore) error) error {
	return routingMetricsCallContext(context.Background(), db, fn)
}
func routingMetricsCallContext(parent context.Context, db *gorm.DB, fn func(model.RoutingMetricsStore) error) error {
	if db == nil {
		return model.ErrRoutingMetricsUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	return fn(model.RoutingMetricsStore{DB: db.WithContext(ctx)})
}

// StartRoutingAttempt must be called after BeginRoutingSend returns successfully,
// immediately before the transport sends. It does not change send eligibility.
func StartRoutingAttempt(c *gin.Context, protocolClass string) error {
	if !IsSmartRouting(c) {
		return nil
	}
	p := RoutingPlanFor(c)
	p.mu.Lock()
	clientModel, group, channelID := p.Model, p.Selected.Group, p.Selected.ChannelID
	p.mu.Unlock()
	requestID, attempt, _ := RoutingAttemptIdentity(c)
	if requestID == "" || attempt <= 0 || !c.GetBool("mowan_routing_send_claimed") {
		return errors.New("routing metrics: attempt has no send claim")
	}
	if model.DB == nil {
		return model.ErrRoutingMetricsUnavailable
	}
	var channel model.Channel
	if err := routingMetricsCall(model.DB, func(s model.RoutingMetricsStore) error {
		return s.DB.Select("id", "type").First(&channel, "id = ?", channelID).Error
	}); err != nil {
		return fmt.Errorf("routing metrics selected channel: %w", err)
	}
	key, err := RoutingObservationKey(c, clientModel, group, &channel, protocolClass)
	if err != nil {
		return err
	}
	a, created, err := routingAttemptRecorder.start(c, key, channelID, fmt.Sprintf("attempt:%s:%d", requestID, attempt), model.DB)
	if created {
		routingAttemptRecorder.startLoop()
		if err == nil || a.submissionID != "" {
			go a.heartbeat(c.Request.Context())
		}
	}
	return err
}

func (r *routingMetricsRecorder) start(c *gin.Context, key model.RoutingMetricKey, channelID int, eventID string, db *gorm.DB) (*routingAttemptObservation, bool, error) {
	trace := routingTrace(c, true)
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if a := trace.attempts[eventID]; a != nil {
		return a, false, a.startErr
	}
	if previous := trace.attempts[trace.current]; previous != nil {
		previous.mu.Lock()
		finished := previous.finished
		previous.mu.Unlock()
		if !finished {
			return nil, false, errors.New("routing metrics: previous attempt not finished")
		}
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, errors.New("routing metrics: recorder closed")
	}
	r.active++
	r.mu.Unlock()
	a := &routingAttemptObservation{key: key, channelID: channelID, eventID: eventID, started: r.now(), stop: make(chan struct{}), db: db, recorder: r}
	if submission := CurrentRoutingSubmission(c); submission != nil && submission.SubmissionID != "" && submission.SendOwnerID != "" {
		a.submissionID, a.sendOwnerID = submission.SubmissionID, submission.SendOwnerID
		if submission.TerminalProbeEpoch > 0 && submission.TerminalProbeLeaseID != "" {
			a.terminalProbe = &model.RoutingProbeClaim{Key: key, Stage: model.RoutingStageTaskTerminal, Epoch: submission.TerminalProbeEpoch, LeaseID: submission.TerminalProbeLeaseID}
		}
	}
	if value, ok := c.Get("mowan_routing_probes"); ok {
		if claims, ok := value.([]model.RoutingProbeClaim); ok {
			for _, claim := range claims {
				if claim.Stage == model.RoutingStageAttempt && claim.Key == key {
					copy := claim
					a.probe = &copy
					break
				}
			}
		}
	}
	trace.current = eventID
	trace.attempts[eventID] = a
	a.startErr = routingMetricsCall(db, func(s model.RoutingMetricsStore) error {
		return s.StartLease(model.RoutingMetricLease{RoutingMetricKey: key, Stage: model.RoutingStageAttempt, ChannelID: channelID, LeaseID: eventID}, time.Time{})
	})
	return a, true, a.startErr
}

// MarkRoutingFirstData is called only for genuine upstream token/content data.
// A header, SSE comment, heartbeat or downstream write is not a first token.
func MarkRoutingFirstData(c *gin.Context) {
	if a := routingCurrentObservation(c); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.finished && !a.hasFirstData {
			a.firstData = a.recorder.now().Sub(a.started)
			a.hasFirstData = true
		}
	}
}

// FinishRoutingAttempt is monotonic per server request/attempt. Async accepted
// here measures submission only; Task completion must use task_terminal events.
func FinishRoutingAttempt(c *gin.Context, result RoutingAttemptResult) error {
	a := routingCurrentObservation(c)
	if a == nil {
		return nil
	}
	return a.finish(result)
}
func (a *routingAttemptObservation) finish(result RoutingAttemptResult) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return a.finishErr
	}
	if result.Probe == nil {
		result.Probe = a.probe
	}
	if (result.Outcome != "success" && result.Outcome != "failure" && result.Outcome != "ignored") || result.Latency < 0 || result.TTFT < 0 || result.SuccessActive < 0 || (result.RateLimited && result.Outcome != "failure") {
		return errors.New("routing metrics: invalid transport outcome")
	}
	now := a.recorder.now()
	latency := result.Latency
	if latency == 0 {
		latency = now.Sub(a.started)
	}
	if latency < 0 {
		latency = 0
	}
	ttft, hasTTFT := result.TTFT, result.TTFT > 0
	if !hasTTFT {
		ttft, hasTTFT = a.firstData, a.hasFirstData
	}
	a.finished = true
	defer a.recorder.finishObservation()
	close(a.stop)
	a.recorder.record(a, result, now, latency, ttft, hasTTFT)
	// Health is applied immediately, independently of the 5s minute flush.
	healthErr := routingMetricsCall(a.db, func(s model.RoutingMetricsStore) error {
		// Immediate reports use the DB clock for occurrence and application; the
		// node clock is only for elapsed durations and advisory minute snapshots.
		_, err := s.ApplyHealthEvent(model.RoutingHealthInput{Key: a.key, Stage: model.RoutingStageAttempt, EventID: a.eventID, Outcome: result.Outcome, Sent: true, RateLimited: result.RateLimited, SuccessActive: result.SuccessActive, Probe: result.Probe}, time.Time{})
		return err
	})
	leaseErr := routingMetricsCall(a.db, func(s model.RoutingMetricsStore) error { return s.FinishLease(a.eventID) })
	if result.Outcome == "ignored" && result.Probe != nil {
		leaseErr = errors.Join(leaseErr, routingMetricsCall(a.db, func(s model.RoutingMetricsStore) error {
			_, err := s.ReleaseProbe(*result.Probe, time.Time{})
			return err
		}))
	}
	a.finishErr = errors.Join(healthErr, leaseErr)
	return a.finishErr
}
func (r *routingMetricsRecorder) record(a *routingAttemptObservation, result RoutingAttemptResult, now time.Time, latency, ttft time.Duration, hasTTFT bool) {
	key := routingSnapshotKey{db: a.db, key: a.key, channelID: a.channelID, minute: now.Unix() / 60 * 60}
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.buckets[key]
	b.RoutingMetricKey = a.key
	b.Stage = model.RoutingStageAttempt
	b.ChannelID = a.channelID
	b.NodeBootID = r.bootID
	b.Minute = key.minute
	b.Revision++
	b.Sent++
	switch result.Outcome {
	case "success":
		b.Success++
		b.Eligible++
	case "failure":
		b.Failure++
		b.Eligible++
	case "ignored":
		b.Ignored++
	}
	if result.RateLimited {
		b.RateLimited++
	}
	if result.Outcome == "success" {
		speed, known := latency, true
		if a.key.ProtocolClass == "stream" {
			speed, known = ttft, hasTTFT
		}
		if known && speed >= 0 {
			b.SpeedSamples++
			b.SpeedTotalMS += float64(speed) / float64(time.Millisecond)
		}
	}
	if result.Outcome != "ignored" {
		b.LastObservationAt = now.Unix()
	}
	r.buckets[key] = b
}
func (a *routingAttemptObservation) renew() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return nil
	}
	// Send ownership is independent of the advisory active/probe leases. A
	// metrics failure must not prevent renewing a still-live submission owner.
	var sendErr, terminalErr error
	if a.submissionID != "" && !a.sendLeaseDone {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		renewed, err := a.recorder.renewSubmissionSendLease(ctx, a.submissionID, a.sendOwnerID)
		cancel()
		if err != nil {
			sendErr = fmt.Errorf("routing submission send lease submission_id=%s: %w", a.submissionID, err)
		} else if !renewed {
			a.sendLeaseDone = true
			common.SysLog(fmt.Sprintf("routing submission send lease no longer renewable submission_id=%s; stop renewal", a.submissionID))
		} else if a.terminalProbe != nil && !a.terminalLeaseDone {
			// Renew independently of the attempt lease and flush. The model locks
			// submission before health and fences owner/state/epoch in one tx.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			valid, err := a.recorder.renewTerminalSendingProbe(ctx, a.submissionID, a.sendOwnerID, *a.terminalProbe)
			cancel()
			if err != nil {
				terminalErr = fmt.Errorf("routing terminal sending probe submission_id=%s epoch=%d: %w", a.submissionID, a.terminalProbe.Epoch, err)
			} else if !valid {
				a.terminalLeaseDone = true
				common.SysLog(fmt.Sprintf("routing terminal sending probe no longer renewable submission_id=%s epoch=%d; stop sender renewal", a.submissionID, a.terminalProbe.Epoch))
			}
		}
	}
	metricErr := routingMetricsCall(a.db, func(s model.RoutingMetricsStore) error {
		renewed, err := s.RenewLease(a.eventID, time.Time{})
		if err == nil && !renewed {
			return errors.New("routing metrics: active lease expired")
		}
		if err == nil && a.probe != nil {
			var valid bool
			valid, err = s.RenewProbe(*a.probe, time.Time{})
			if err == nil && !valid {
				return errors.New("routing metrics: attempt probe lease expired")
			}
		}
		return err
	})
	return errors.Join(sendErr, terminalErr, metricErr)
}
func (a *routingAttemptObservation) heartbeat(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-a.stop:
		return
	case <-a.recorder.heartbeatStop:
		return
	default:
	}
	// Preparation may have consumed most of the original terminal lease. Renew
	// at send start rather than waiting another full 10s for the first tick.
	if a.terminalProbe != nil {
		if err := a.renew(); err != nil {
			common.SysError(fmt.Sprintf("routing terminal sender initial lease %s: %v", a.eventID, err))
		}
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-ctx.Done():
			return
		case <-a.recorder.heartbeatStop:
			return
		case <-ticker.C:
			if err := a.renew(); err != nil {
				common.SysError(fmt.Sprintf("routing metrics lease %s: %v", a.eventID, err))
				a.mu.Lock()
				retrySendLease := a.submissionID != "" && !a.sendLeaseDone && !a.finished
				a.mu.Unlock()
				if !retrySendLease {
					return
				}
			}
		}
	}
}

// FlushRoutingMetrics persists pending absolute snapshots without stopping
// observation. Database/context failures retain the pending revisions for retry.
func FlushRoutingMetrics(ctx context.Context) error {
	return routingAttemptRecorder.flushContext(ctx, routingAttemptRecorder.now())
}

// CloseRoutingMetrics stops admission and the flush loop, waits for started
// attempts, then flushes. Stop incoming requests first and close the DB afterward.
// A failed/cancelled close can be retried with a fresh context.
func CloseRoutingMetrics(ctx context.Context) error {
	return routingAttemptRecorder.close(ctx)
}

func (r *routingMetricsRecorder) startLoop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.loopStarted {
		return
	}
	r.loopStarted = true
	go r.run()
}
func (r *routingMetricsRecorder) finishObservation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.closed && r.active == 0 {
		close(r.drained)
	}
}
func (r *routingMetricsRecorder) close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("routing metrics: context required")
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.stopLoop()
		if !r.loopStarted {
			close(r.loopDone)
		}
		if r.active == 0 {
			close(r.drained)
		}
	}
	r.mu.Unlock()
	defer r.heartbeatStopOnce.Do(func() { close(r.heartbeatStop) })
	for _, done := range []<-chan struct{}{r.loopDone, r.drained} {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.flushContext(ctx, r.now())
}
func (r *routingMetricsRecorder) run() {
	defer close(r.loopDone)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.loopCtx.Done():
			return
		case <-ticker.C:
			if err := r.flushContext(r.loopCtx, r.now()); err != nil && r.loopCtx.Err() == nil {
				common.SysError(fmt.Sprintf("routing metrics flush: %v", err))
			}
		}
	}
}
func (r *routingMetricsRecorder) flush(now time.Time) error {
	return r.flushContext(context.Background(), now)
}
func (r *routingMetricsRecorder) flushContext(ctx context.Context, now time.Time) error {
	if ctx == nil {
		return errors.New("routing metrics: context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	pending := make(map[routingSnapshotKey]model.RoutingMetricBucket, len(r.buckets))
	for key, b := range r.buckets {
		if key.minute < now.Unix()-48*3600 {
			delete(r.buckets, key)
			delete(r.flushed, key)
			continue
		}
		if r.flushed[key] < b.Revision {
			pending[key] = b
		}
	}
	r.mu.Unlock()
	var failures []error
	for key, b := range pending {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		err := routingMetricsCallContext(ctx, key.db, func(s model.RoutingMetricsStore) error { _, err := s.UpsertSnapshot(b, time.Time{}); return err })
		if err != nil {
			failures = append(failures, fmt.Errorf("routing metric snapshot model=%s group=%s minute=%d revision=%d: %w", key.key.ClientModel, key.key.BusinessGroup, key.minute, b.Revision, err))
			continue
		}
		// Retain cumulative totals for 48h, even after successful flush: a delayed
		// finish must never recreate an old minute with revision/counts reset.
		r.mu.Lock()
		if r.flushed[key] < b.Revision {
			r.flushed[key] = b.Revision
		}
		r.mu.Unlock()
	}
	return errors.Join(failures...)
}
