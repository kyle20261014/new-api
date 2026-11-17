package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func routingAttemptTestRecorder(t *testing.T) (*routingMetricsRecorder, *gorm.DB, *gin.Context, *time.Time) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "attempts.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, model.MigrateRoutingMetrics(db))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r := newRoutingMetricsRecorder()
	now := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	r.now = func() time.Time { return now }
	return r, db, c, &now
}
func routingAttemptTestKey(protocol string) model.RoutingMetricKey {
	return model.RoutingMetricKey{ClientModel: "attempt-model", BusinessGroup: "default", ProtocolClass: protocol, ProviderKey: "builtin:1:v1", OperationKey: "chat"}
}

func TestRoutingMetricsAttemptOnceTTFTAndAbsoluteFlush(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("stream")
	start := *clock
	a, created, err := r.start(c, k, 1, "server-request:1", db)
	require.NoError(t, err)
	require.True(t, created)
	_, created, err = r.start(c, k, 1, "server-request:1", db)
	require.NoError(t, err)
	require.False(t, created)
	*clock = start.Add(200 * time.Millisecond)
	MarkRoutingFirstData(c)
	*clock = start.Add(500 * time.Millisecond)
	MarkRoutingFirstData(c)
	*clock = start.Add(time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "failure", RateLimited: true}))
	require.True(t, a.finished)
	require.NoError(t, r.flush(*clock))
	require.NoError(t, r.flush(*clock))
	store := model.RoutingMetricsStore{DB: db}
	fold, err := store.Fold(k, model.RoutingStageAttempt, []int{1}, start.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Eligible)
	require.EqualValues(t, 1, fold.Success)
	require.EqualValues(t, 1, fold.SpeedSamples)
	require.InDelta(t, 200, fold.SpeedTotalMS, 0.001)
	require.Zero(t, fold.Active)
	var events int64
	require.NoError(t, db.Model(&model.RoutingHealthEvent{}).Count(&events).Error)
	require.EqualValues(t, 1, events)
	// A completed minute remains cumulative after flush, including delayed finishes.
	*clock = start.Add(time.Minute)
	require.NoError(t, r.flush(*clock))
	*clock = start.Add(2 * time.Second)
	_, _, err = r.start(c, k, 1, "server-request:2", db)
	require.NoError(t, err)
	*clock = start.Add(3 * time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success", TTFT: 400 * time.Millisecond}))
	require.NoError(t, r.flush(*clock))
	fold, err = store.Fold(k, model.RoutingStageAttempt, []int{1}, start.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.EqualValues(t, 2, fold.Success)
	require.InDelta(t, 600, fold.SpeedTotalMS, 0.001)
}

func TestRoutingMetricsTransportClassificationAndNoFabricatedSpeed(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("stream")
	for i, outcome := range []string{"failure", "ignored", "failure", "failure"} {
		_, _, err := r.start(c, k, 1, fmt.Sprintf("classified:%d", i), db)
		require.NoError(t, err)
		*clock = clock.Add(time.Second)
		// Request cancellation must not override an actual upstream failure.
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
		require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: outcome}))
	}
	require.NoError(t, r.flush(*clock))
	store := model.RoutingMetricsStore{DB: db}
	fold, err := store.Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.EqualValues(t, 3, fold.Eligible)
	require.EqualValues(t, 1, fold.Ignored)
	require.Zero(t, fold.SpeedSamples)
	view, err := store.ReadHealth(k, model.RoutingStageAttempt, 0, time.Time{})
	require.NoError(t, err)
	require.Equal(t, model.RoutingHealthOpen, view.Health.State)
}

func TestRoutingMetricsAsyncSubmissionAndUnknownSpeed(t *testing.T) {
	for _, protocol := range []string{"sync", "stream", "async"} {
		t.Run(protocol, func(t *testing.T) {
			r, db, c, clock := routingAttemptTestRecorder(t)
			k := routingAttemptTestKey(protocol)
			_, _, err := r.start(c, k, 1, "protocol-attempt", db)
			require.NoError(t, err)
			*clock = clock.Add(2 * time.Second)
			require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
			require.NoError(t, r.flush(*clock))
			store := model.RoutingMetricsStore{DB: db}
			fold, err := store.Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
			require.NoError(t, err)
			require.EqualValues(t, 1, fold.Success)
			if protocol == "stream" {
				require.Zero(t, fold.SpeedSamples, "success without observed first data has unknown TTFT")
			} else {
				require.EqualValues(t, 1, fold.SpeedSamples)
				require.InDelta(t, 2000, fold.SpeedTotalMS, 0.001)
			}
			if protocol == "async" {
				terminal, err := store.Fold(k, model.RoutingStageTaskTerminal, []int{1}, clock.Add(-time.Hour), *clock)
				require.NoError(t, err)
				require.Zero(t, terminal, "accepted submission must never fabricate task success")
			}
		})
	}
}

func TestRoutingMetricsFlushFailureRetainsSnapshotAndLeaseDoesNotRevive(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("sync")
	a, _, err := r.start(c, k, 1, "retry-flush", db)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.RoutingMetricLease{}).Where("lease_id = ?", a.eventID).Update("expires_at", 1).Error)
	require.Error(t, a.renew())
	var lease model.RoutingMetricLease
	require.NoError(t, db.First(&lease, "lease_id = ?", a.eventID).Error)
	require.EqualValues(t, 1, lease.ExpiresAt)
	*clock = clock.Add(time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
	view, err := (model.RoutingMetricsStore{DB: db}).ReadHealth(k, model.RoutingStageAttempt, 100, time.Time{})
	require.NoError(t, err)
	require.Zero(t, view.SuccessActivePeak5m, "an expired lease cannot fabricate capacity")
	require.False(t, view.Congested)
	require.NoError(t, db.Migrator().DropTable(&model.RoutingMetricBucket{}))
	require.Error(t, r.flush(*clock))
	require.Len(t, r.buckets, 1)
	require.NoError(t, model.MigrateRoutingMetrics(db))
	require.NoError(t, r.flush(*clock))
	require.NoError(t, r.flush(*clock))
	fold, err := (model.RoutingMetricsStore{DB: db}).Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Success)
	// The heartbeat exits on cancellation, without manufacturing a finish outcome.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { a.heartbeat(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lease heartbeat did not stop")
	}
	legacy, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.NoError(t, StartRoutingAttempt(legacy, "sync"))
	MarkRoutingFirstData(legacy)
	require.NoError(t, FinishRoutingAttempt(legacy, RoutingAttemptResult{Outcome: "ignored"}))
}

func TestRoutingMetricsAttemptProbeRenewsAndUsesOriginalClaim(t *testing.T) {
	for _, outcome := range []string{"success", "ignored"} {
		t.Run(outcome, func(t *testing.T) {
			r, db, c, clock := routingAttemptTestRecorder(t)
			k := routingAttemptTestKey("sync")
			store := model.RoutingMetricsStore{DB: db}
			for i := 0; i < 3; i++ {
				_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: k, Stage: model.RoutingStageAttempt, EventID: fmt.Sprintf("probe-failure-%d", i), Outcome: "failure"}, time.Time{})
				require.NoError(t, err)
			}
			claim, ok, err := store.ClaimProbeExplicit(k, model.RoutingStageAttempt, "probe-original", time.Time{})
			require.NoError(t, err)
			require.True(t, ok)
			c.Set("mowan_routing_probes", []model.RoutingProbeClaim{claim})
			a, _, err := r.start(c, k, 1, "probe-attempt", db)
			require.NoError(t, err)
			// Context changes after sending cannot substitute another claim or key.
			c.Set("mowan_routing_probes", []model.RoutingProbeClaim{})
			require.NotNil(t, a.probe)
			require.Equal(t, claim.Epoch, a.probe.Epoch)
			require.NoError(t, db.Model(&model.RoutingHealth{}).Where("probe_lease_id = ?", claim.LeaseID).Update("probe_until", time.Now().Unix()+5).Error)
			require.NoError(t, a.renew())
			view, err := store.ReadHealth(k, model.RoutingStageAttempt, 1, time.Time{})
			require.NoError(t, err)
			require.GreaterOrEqual(t, view.Health.ProbeUntil, time.Now().Unix()+28)
			*clock = clock.Add(time.Second)
			require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: outcome}))
			view, err = store.ReadHealth(k, model.RoutingStageAttempt, 0, time.Time{})
			require.NoError(t, err)
			if outcome == "success" {
				require.Equal(t, model.RoutingHealthClosed, view.Health.State)
			} else {
				require.Equal(t, model.RoutingHealthOpen, view.Health.State)
				require.EqualValues(t, 30, view.Health.Backoff)
			}
		})
	}
}

func TestRoutingMetricsImmediateHealthUsesDatabaseClock(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("sync")
	// A skewed node wall clock must not classify a fresh finish as a late report.
	*clock = clock.Add(-time.Hour)
	_, _, err := r.start(c, k, 1, "skewed-clock", db)
	require.NoError(t, err)
	*clock = clock.Add(2 * time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
	var event model.RoutingHealthEvent
	require.NoError(t, db.First(&event, "event_id = ?", "skewed-clock").Error)
	require.Equal(t, event.AppliedAt, event.OccurredAt)
	require.InDelta(t, time.Now().Unix(), event.AppliedAt, 3)
	view, err := (model.RoutingMetricsStore{DB: db}).ReadHealth(k, model.RoutingStageAttempt, 0, time.Time{})
	require.NoError(t, err)
	require.EqualValues(t, 1, view.Health.EventSeq)
	require.EqualValues(t, 1, view.Eligible5m)
	require.NoError(t, r.flush(*clock))
	fold, err := (model.RoutingMetricsStore{DB: db}).Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.InDelta(t, 2000, fold.SpeedTotalMS, 0.001, "latency must still use the local elapsed duration")
}

func TestRoutingMetricsSharedFinishCapturesSuccessfulActive(t *testing.T) {
	for _, protocol := range []string{"sync", "stream", "responses_ws", "realtime", "async"} {
		t.Run(protocol, func(t *testing.T) {
			r, db, c, clock := routingAttemptTestRecorder(t)
			*clock = clock.Add(-time.Hour) // Active and short windows use DB, not this node clock.
			k := routingAttemptTestKey(protocol)
			store := model.RoutingMetricsStore{DB: db}
			for i := 0; i < 2; i++ {
				require.NoError(t, store.StartLease(model.RoutingMetricLease{RoutingMetricKey: k, Stage: model.RoutingStageAttempt, ChannelID: i + 2, LeaseID: fmt.Sprintf("other-node-%d", i)}, time.Time{}))
			}
			other := k
			other.OperationKey = "other-operation"
			require.NoError(t, store.StartLease(model.RoutingMetricLease{RoutingMetricKey: other, Stage: model.RoutingStageAttempt, ChannelID: 1, LeaseID: "other-key"}, time.Time{}))
			require.NoError(t, store.StartLease(model.RoutingMetricLease{RoutingMetricKey: k, Stage: model.RoutingStageAttempt, ChannelID: 1, LeaseID: "expired"}, time.Unix(1, 0)))
			if protocol == "async" {
				require.NoError(t, store.SaveTerminalSubmission(k, "other-stage", 1, "accepted", "", time.Time{}))
			}
			for _, outcome := range []string{"failure", "ignored", "success"} {
				_, _, err := r.start(c, k, 1, "shared-"+outcome, db)
				require.NoError(t, err)
				require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: outcome, SuccessActive: 99}))
				view, err := store.ReadHealth(k, model.RoutingStageAttempt, 2, time.Time{})
				require.NoError(t, err)
				if outcome == "success" {
					require.EqualValues(t, 3, view.SuccessActivePeak5m, "includes completing lease and cross-node peers only")
				} else {
					require.Zero(t, view.SuccessActivePeak5m, "failure and ignored cannot establish capacity")
				}
			}
			view, err := store.ReadHealth(k, model.RoutingStageAttempt, 3, time.Time{})
			require.NoError(t, err)
			require.True(t, view.Congested)
			view, err = store.ReadHealth(other, model.RoutingStageAttempt, 100, time.Time{})
			require.NoError(t, err)
			require.Zero(t, view.SuccessActivePeak5m)
			require.False(t, view.Congested)
			if protocol == "async" {
				view, err = store.ReadHealth(k, model.RoutingStageTaskTerminal, 100, time.Time{})
				require.NoError(t, err)
				require.Zero(t, view.SuccessActivePeak5m)
				require.False(t, view.Congested)
			}
		})
	}
}

func TestRoutingFinishSendRetainsActualUpstreamFailureAfterCancel(t *testing.T) {
	providerResponse := func(status int) *types.NewAPIError {
		return RelayErrorHandler(context.Background(), &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"server_error","code":"provider_busy","message":"busy"}}`))}, false)
	}
	mapped429 := providerResponse(429)
	ResetStatusCode(mapped429, `{"429":"400"}`)
	for _, tc := range []struct {
		name    string
		err     *types.NewAPIError
		cancel  bool
		failure bool
		rate429 bool
	}{
		{"response429", types.NewErrorWithStatusCode(errors.New("upstream 429"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), true, true, true},
		{"response500", types.NewOpenAIError(errors.New("upstream 500"), types.ErrorCodeBadResponseStatusCode, 500), true, true, false},
		{"structured500", providerResponse(500), true, true, false},
		{"structured429", providerResponse(429), true, true, true},
		{"mapped429", mapped429, true, true, true},
		{"unproven_custom500", types.WithOpenAIError(types.OpenAIError{Type: "server_error", Code: "provider_busy", Message: "busy"}, 500), true, false, false},
		{"handshake500", types.NewErrorWithStatusCode(fmt.Errorf("dial: %w", websocket.ErrBadHandshake), types.ErrorCodeDoRequestFailed, 500), true, true, false},
		{"cancel_only", nil, true, false, false},
		{"cancel_wrapped500", types.NewError(context.Canceled, types.ErrorCodeDoRequestFailed), true, false, false},
		{"cancel_flattened500", types.NewOpenAIError(context.Canceled, types.ErrorCodeDoRequestFailed, 500), true, false, false},
		{"cancel_body_read500", types.NewOpenAIError(context.Canceled, types.ErrorCodeBadResponseBody, 500), true, false, false},
		{"local500", types.NewError(errors.New("local billing failure"), types.ErrorCodeModelPriceError), false, false, false},
		{"local500_cancel", types.NewError(errors.New("local billing failure"), types.ErrorCodeModelPriceError), true, false, false},
		{"network500", types.NewError(errors.New("connection reset"), types.ErrorCodeDoRequestFailed), false, true, false},
		{"body500", types.NewOpenAIError(errors.New("malformed upstream body"), types.ErrorCodeBadResponseBody, 500), false, true, false},
		{"business400", types.NewErrorWithStatusCode(errors.New("bad input"), types.ErrorCodeBadResponseStatusCode, 400), true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db, c, _ := routingAttemptTestRecorder(t)
			c.Set(routingPlanKey, &RoutingPlan{})
			k := routingAttemptTestKey("sync")
			_, _, err := r.start(c, k, 1, "cancel-classification", db)
			require.NoError(t, err)
			if tc.cancel {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			FinishRoutingSend(c, tc.err, false, 0)
			view, err := (model.RoutingMetricsStore{DB: db}).ReadHealth(k, model.RoutingStageAttempt, 0, time.Time{})
			require.NoError(t, err)
			if tc.failure {
				require.EqualValues(t, 1, view.Failures5m)
				require.EqualValues(t, 1, view.Eligible5m)
			} else {
				require.Zero(t, view.Failures5m)
				require.Zero(t, view.Eligible5m)
			}
			if tc.rate429 {
				require.EqualValues(t, 1, view.RateLimited60s)
			} else {
				require.Zero(t, view.RateLimited60s)
			}
		})
	}
}

func TestRoutingUpstreamResponseFactPreservesErrorContract(t *testing.T) {
	cause := errors.New("upstream original cause")
	apiErr := types.NewErrorWithStatusCode(fmt.Errorf("response: %w", cause), types.ErrorCodeBadResponseStatusCode, 503)
	message, code, kind := apiErr.Error(), apiErr.GetErrorCode(), apiErr.GetErrorType()
	require.Same(t, apiErr, MarkRoutingUpstreamResponse(apiErr))
	require.Same(t, apiErr, MarkRoutingUpstreamResponse(apiErr))
	require.ErrorIs(t, apiErr, cause)
	require.Equal(t, message, apiErr.Error())
	require.Equal(t, code, apiErr.GetErrorCode())
	require.Equal(t, kind, apiErr.GetErrorType())
	require.Equal(t, 503, apiErr.StatusCode)
	var fact *routingUpstreamResponseError
	require.ErrorAs(t, apiErr, &fact)
	require.Equal(t, 503, fact.statusCode)
}

func TestRoutingMetricsCloseDrainsAndFlushes(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("sync")
	a, _, err := r.start(c, k, 1, "drain-on-close", db)
	require.NoError(t, err)
	r.startLoop()
	heartbeatDone := make(chan struct{})
	go func() { a.heartbeat(context.Background()); close(heartbeatDone) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.close(ctx) }()
	select {
	case <-r.loopDone:
	case <-ctx.Done():
		t.Fatal("flush loop did not stop")
	}
	select {
	case err := <-done:
		t.Fatalf("close returned before the live attempt finished: %v", err)
	default:
	}
	other, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, _, err = r.start(other, k, 1, "after-close", db)
	require.ErrorContains(t, err, "recorder closed")
	*clock = clock.Add(2 * time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("close did not drain")
	}
	select {
	case <-heartbeatDone:
	case <-ctx.Done():
		t.Fatal("lease heartbeat did not stop")
	}
	require.NoError(t, r.close(ctx), "close must be idempotent")
	r.startLoop() // A late caller cannot restart the closed flush loop.
	require.NoError(t, r.flushContext(ctx, *clock))
	fold, err := (model.RoutingMetricsStore{DB: db}).Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Success)
	require.InDelta(t, 2000, fold.SpeedTotalMS, 0.001)
	require.Zero(t, fold.Active)
}

func TestRoutingMetricsCloseCancellationAndDatabaseRetry(t *testing.T) {
	t.Run("cancelled drain stops heartbeat without fabricating an outcome", func(t *testing.T) {
		r, db, c, clock := routingAttemptTestRecorder(t)
		k := routingAttemptTestKey("sync")
		a, _, err := r.start(c, k, 1, "cancelled-close", db)
		require.NoError(t, err)
		r.startLoop()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, r.close(ctx), context.Canceled)
		select {
		case <-r.loopDone:
		case <-time.After(time.Second):
			t.Fatal("cancelled close leaked the flush loop")
		}
		done := make(chan struct{})
		go func() { a.heartbeat(context.Background()); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled close leaked a heartbeat")
		}
		var events int64
		require.NoError(t, db.Model(&model.RoutingHealthEvent{}).Count(&events).Error)
		require.Zero(t, events)
		*clock = clock.Add(time.Second)
		require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "failure"}))
		require.NoError(t, r.close(context.Background()))
		fold, err := (model.RoutingMetricsStore{DB: db}).Fold(k, model.RoutingStageAttempt, []int{1}, clock.Add(-time.Hour), *clock)
		require.NoError(t, err)
		require.EqualValues(t, 1, fold.Failure)
		require.Zero(t, fold.Success)
	})
	t.Run("failed final flush retains revision for retry", func(t *testing.T) {
		r, db, c, clock := routingAttemptTestRecorder(t)
		k := routingAttemptTestKey("sync")
		_, _, err := r.start(c, k, 1, "failed-close", db)
		require.NoError(t, err)
		*clock = clock.Add(time.Second)
		require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"}))
		require.NoError(t, db.Migrator().DropTable(&model.RoutingMetricBucket{}))
		require.Error(t, r.close(context.Background()))
		require.Len(t, r.buckets, 1)
		require.Empty(t, r.flushed)
		require.NoError(t, model.MigrateRoutingMetrics(db))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, r.flushContext(ctx, *clock), context.Canceled)
		require.Empty(t, r.flushed)
		require.NoError(t, r.close(context.Background()))
		require.NoError(t, r.close(context.Background()))
		var snapshot model.RoutingMetricBucket
		require.NoError(t, db.First(&snapshot).Error)
		require.EqualValues(t, 1, snapshot.Revision)
		require.EqualValues(t, 1, snapshot.Success)
	})
	t.Run("close before lazy start", func(t *testing.T) {
		r := newRoutingMetricsRecorder()
		require.NoError(t, r.close(context.Background()))
		r.startLoop()
		require.False(t, r.loopStarted)
		select {
		case <-r.loopDone:
		default:
			t.Fatal("unused recorder did not close")
		}
		require.NoError(t, r.close(context.Background()))
	})
}

func TestRoutingMetricsSubmissionSendLeaseUsesCapturedOwner(t *testing.T) {
	r, db, c, clock := routingAttemptTestRecorder(t)
	k := routingAttemptTestKey("async")
	submission := &model.RoutingSubmission{SubmissionID: "stable-submission", SendOwnerID: "original-owner", State: "sending"}
	c.Set("mowan_routing_submission", submission)
	transient := errors.New("temporary submission DB failure")
	calls := 0
	// The funds model owns DB-CAS coverage. This seam keeps this adapter test on
	// its private SQLite DB without mutating model.DB or creating funding rows.
	r.renewSubmissionSendLease = func(ctx context.Context, id, owner string) (bool, error) {
		calls++
		require.Equal(t, "stable-submission", id)
		require.Equal(t, "original-owner", owner)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		switch calls {
		case 1:
			return false, transient
		case 2:
			return true, nil
		case 3:
			return false, nil // accepted/unknown, owner changed, or expired: DB is authoritative
		default:
			t.Fatal("send lease renewed again after DB-CAS returned false")
			return false, nil
		}
	}
	a, _, err := r.start(c, k, 1, "owned-submission-attempt", db)
	require.NoError(t, err)
	submission.SendOwnerID = "mutated-owner"
	c.Set("mowan_routing_submission", &model.RoutingSubmission{SubmissionID: "another-submission", SendOwnerID: "another-owner", State: "accepted"})
	require.ErrorIs(t, a.renew(), transient)
	require.False(t, a.sendLeaseDone, "transient failures remain retryable")
	require.NoError(t, db.Model(&model.RoutingMetricLease{}).Where("lease_id = ?", a.eventID).Update("expires_at", 1).Error)
	require.ErrorContains(t, a.renew(), "active lease expired")
	require.Equal(t, 2, calls, "advisory metric failure must not block send-owner renewal")
	require.False(t, a.sendLeaseDone)
	require.NoError(t, db.Model(&model.RoutingMetricLease{}).Where("lease_id = ?", a.eventID).Update("expires_at", time.Now().Unix()+30).Error)
	require.NoError(t, a.renew(), "lost send ownership is not a provider failure")
	require.True(t, a.sendLeaseDone)
	require.NoError(t, a.renew())
	require.Equal(t, 3, calls)
	var events int64
	require.NoError(t, db.Model(&model.RoutingHealthEvent{}).Count(&events).Error)
	require.Zero(t, events, "lease outcomes must not fabricate provider health events")
	*clock = clock.Add(time.Second)
	require.NoError(t, FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "ignored"}))
	require.NoError(t, r.close(context.Background()))
}

func TestRoutingMetricsTerminalSendingSurvivesSlowSubmission(t *testing.T) {
	for _, metricsUnavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("metrics_unavailable_%t", metricsUnavailable), func(t *testing.T) {
			r, db, c, clock := routingAttemptTestRecorder(t)
			require.NoError(t, db.AutoMigrate(&model.RoutingSubmission{}))
			k := routingAttemptTestKey("async")
			store := model.RoutingMetricsStore{DB: db}
			for i := 0; i < 3; i++ {
				_, err := store.ApplyHealthEvent(model.RoutingHealthInput{Key: k, Stage: model.RoutingStageTaskTerminal, EventID: fmt.Sprintf("slow-open-%d", i), Outcome: "failure"}, *clock)
				require.NoError(t, err)
			}
			claim, ok, err := store.ClaimProbeExplicit(k, model.RoutingStageTaskTerminal, "slow-terminal-lease", *clock)
			require.NoError(t, err)
			require.True(t, ok)
			submission := &model.RoutingSubmission{SubmissionID: "slow-submission", RequestID: "slow-request", AttemptNo: 1, State: "sending", SendOwnerID: "slow-owner", SendLeaseUntil: clock.Unix() + 30, ClientModel: k.ClientModel, BusinessGroup: k.BusinessGroup, ProtocolClass: k.ProtocolClass, ProviderKey: k.ProviderKey, OperationKey: k.OperationKey, ChannelID: 1, TerminalProbeEpoch: claim.Epoch, TerminalProbeLeaseID: claim.LeaseID}
			require.NoError(t, db.Create(submission).Error)
			require.NoError(t, store.BindTerminalProbe(claim, submission.SubmissionID, 1, *clock))
			c.Set("mowan_routing_submission", submission)
			r.renewSubmissionSendLease = func(ctx context.Context, id, owner string) (bool, error) {
				result := db.WithContext(ctx).Model(&model.RoutingSubmission{}).Where("submission_id = ? AND send_owner_id = ? AND send_lease_until > ? AND state IN ?", id, owner, clock.Unix(), []string{"prepared", "sending"}).Update("send_lease_until", clock.Unix()+30)
				return result.RowsAffected == 1, result.Error
			}
			terminalCalls := 0
			initialRenewed := make(chan struct{}, 1)
			r.renewTerminalSendingProbe = func(ctx context.Context, id, owner string, captured model.RoutingProbeClaim) (bool, error) {
				terminalCalls++
				require.Equal(t, claim.Epoch, captured.Epoch)
				require.Equal(t, claim.LeaseID, captured.LeaseID)
				ok, err := (model.RoutingMetricsStore{DB: db.WithContext(ctx)}).RenewTerminalSendingProbe(id, owner, captured, *clock)
				select {
				case initialRenewed <- struct{}{}:
				default:
				}
				return ok, err
			}
			if metricsUnavailable {
				require.NoError(t, db.Migrator().DropTable(&model.RoutingMetricLease{}))
			}
			a, created, err := r.start(c, k, 1, "slow-attempt", db)
			require.True(t, created)
			if metricsUnavailable {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			// Durable capture cannot be replaced by mutable request context afterward.
			submission.TerminalProbeEpoch++
			submission.TerminalProbeLeaseID = "replacement"
			c.Set("mowan_routing_probes", []model.RoutingProbeClaim{})
			// Sending can start with less than 10s remaining after preparation.
			*clock = clock.Add(25 * time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { a.heartbeat(ctx); close(done) }()
			select {
			case <-initialRenewed:
			case <-time.After(3 * time.Second):
				t.Fatal("sender waited for the first 10s tick instead of renewing its prepared claim")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("sender heartbeat did not stop")
			}
			for i := 0; i < 8; i++ {
				*clock = clock.Add(10 * time.Second)
				err := a.renew()
				if metricsUnavailable {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}
			view, err := store.ReadHealth(k, model.RoutingStageTaskTerminal, 0, *clock)
			require.NoError(t, err)
			require.Equal(t, model.RoutingHealthHalfOpen, view.Health.State, "a real submission longer than 30s must retain the same terminal epoch")
			require.Equal(t, claim.Epoch, view.Health.BreakerEpoch)
			require.EqualValues(t, clock.Unix()+30, view.Health.ProbeUntil)
			require.Equal(t, 9, terminalCalls)
			err = FinishRoutingAttempt(c, RoutingAttemptResult{Outcome: "success"})
			if metricsUnavailable {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, r.close(context.Background()))
		})
	}
}
