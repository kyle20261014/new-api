package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func routingMetricsTestStore(t *testing.T) RoutingMetricsStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "routing.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, MigrateRoutingMetrics(db))
	require.NoError(t, MigrateRoutingMetrics(db))
	return RoutingMetricsStore{DB: db}
}
func routingMetricsTestKey() RoutingMetricKey {
	return RoutingMetricKey{ClientModel: "test-model", BusinessGroup: "test-group", ProtocolClass: "sync", ProviderKey: "builtin:1:v1", OperationKey: "chat"}
}
func routingMetricsTestNow() time.Time { return time.Unix(1800000000, 0).UTC() }

func routingTerminalSendingTest(t *testing.T, now time.Time) (RoutingMetricsStore, RoutingProbeClaim, RoutingSubmission) {
	t.Helper()
	s := routingMetricsTestStore(t)
	require.NoError(t, s.DB.AutoMigrate(&RoutingSubmission{}))
	k := routingMetricsTestKey()
	k.ProtocolClass = "async"
	for i := 0; i < 3; i++ {
		routingHealthTestEvent(t, s, k, RoutingStageTaskTerminal, fmt.Sprintf("sending-failure-%d", i), "failure", now)
	}
	c, ok, err := s.ClaimProbeExplicit(k, RoutingStageTaskTerminal, "sending-terminal-claim", now)
	require.NoError(t, err)
	require.True(t, ok)
	sub := RoutingSubmission{SubmissionID: "sending-submission", RequestID: "sending-request", AttemptNo: 1, State: "prepared", SendOwnerID: "sender-one", SendLeaseUntil: now.Unix() + 30, ClientModel: k.ClientModel, BusinessGroup: k.BusinessGroup, ProtocolClass: k.ProtocolClass, ProviderKey: k.ProviderKey, OperationKey: k.OperationKey, ChannelID: 7, TerminalProbeEpoch: c.Epoch, TerminalProbeLeaseID: c.LeaseID}
	require.NoError(t, s.DB.Create(&sub).Error)
	require.NoError(t, s.BindTerminalProbe(c, sub.SubmissionID, sub.ChannelID, now))
	return s, c, sub
}

func TestRoutingHealthTerminalSendingLeaseAndAcceptedHandoff(t *testing.T) {
	now := routingMetricsTestNow()
	s, c, sub := routingTerminalSendingTest(t, now)
	for second := 10; second <= 90; second += 10 {
		at := now.Add(time.Duration(second) * time.Second)
		// The sender lease was first renewed successfully by the durable send owner.
		require.NoError(t, s.DB.Model(&RoutingSubmission{}).Where("submission_id = ?", sub.SubmissionID).Updates(map[string]any{"state": "sending", "send_lease_until": at.Unix() + 30}).Error)
		ok, err := s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, at)
		require.NoError(t, err)
		require.True(t, ok)
		view, err := s.ReadHealth(c.Key, c.Stage, 0, at)
		require.NoError(t, err)
		require.Equal(t, RoutingHealthHalfOpen, view.Health.State)
		require.Equal(t, c.Epoch, view.Health.BreakerEpoch)
		require.EqualValues(t, at.Unix()+30, view.Health.ProbeUntil)
	}
	accepted := now.Add(119 * time.Second) // Only one second remained on the sender's probe.
	require.NoError(t, s.DB.Transaction(func(tx *gorm.DB) error {
		var row RoutingSubmission
		if err := lockForUpdate(tx).First(&row, "submission_id = ?", sub.SubmissionID).Error; err != nil {
			return err
		}
		if err := tx.Model(&row).Update("state", "accepted").Error; err != nil {
			return err
		}
		return (RoutingMetricsStore{DB: tx}).SaveTerminalSubmission(c.Key, sub.SubmissionID, sub.ChannelID, "accepted", "task-one", accepted)
	}))
	view, err := s.ReadHealth(c.Key, c.Stage, 0, accepted)
	require.NoError(t, err)
	require.EqualValues(t, accepted.Unix()+30, view.Health.ProbeUntil, "acceptance must leave a full observer handoff window")
	before := view.Health
	ok, err := s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, accepted.Add(time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	view, err = s.ReadHealth(c.Key, c.Stage, 0, accepted.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, before, view.Health, "accepted sender cannot change observer-owned health")
	observer, ok, err := s.ClaimTerminalObserver(c, sub.SubmissionID, "observer-one", accepted.Add(20*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.RenewTerminalObserver(observer, true, accepted.Add(25*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	result, err := s.ApplyHealthEvent(RoutingHealthInput{Key: c.Key, Stage: c.Stage, EventID: "slow-terminal-success", Outcome: "success", Probe: &c, SubmissionID: sub.SubmissionID, ObserverOwner: observer.Owner, ObserverGeneration: observer.Generation}, accepted.Add(26*time.Second))
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, RoutingHealthClosed, result.Health.State)
}

func TestRoutingHealthTerminalSendingFencing(t *testing.T) {
	for _, kind := range []string{"owner_lost", "unknown", "rejected", "accepted", "send_expired", "claim_epoch", "claim_lease", "claim_key", "observation_state", "observation_channel", "new_health_epoch", "new_health_epoch_expired"} {
		t.Run(kind, func(t *testing.T) {
			now := routingMetricsTestNow()
			s, c, sub := routingTerminalSendingTest(t, now)
			at := now.Add(10 * time.Second)
			switch kind {
			case "owner_lost":
				require.NoError(t, s.DB.Model(&sub).Update("send_owner_id", "successor").Error)
			case "unknown", "rejected", "accepted":
				require.NoError(t, s.DB.Model(&sub).Update("state", kind).Error)
			case "send_expired":
				require.NoError(t, s.DB.Model(&sub).Update("send_lease_until", at.Unix()).Error)
			case "claim_epoch":
				c.Epoch++
			case "claim_lease":
				c.LeaseID = "wrong-lease"
			case "claim_key":
				c.Key.ProviderKey = "different-provider"
			case "observation_state":
				require.NoError(t, s.DB.Model(&RoutingTerminalObservation{}).Where("submission_id = ?", sub.SubmissionID).Update("status", "accepted").Error)
			case "observation_channel":
				require.NoError(t, s.DB.Model(&RoutingTerminalObservation{}).Where("submission_id = ?", sub.SubmissionID).Update("channel_id", 99).Error)
			case "new_health_epoch", "new_health_epoch_expired":
				at = now.Add(31 * time.Second)
				next, ok, err := s.ClaimProbeExplicit(c.Key, c.Stage, "successor-claim", at)
				require.NoError(t, err)
				require.True(t, ok)
				require.Greater(t, next.Epoch, c.Epoch)
				if kind == "new_health_epoch_expired" {
					at = now.Add(70 * time.Second)
				}
				require.NoError(t, s.DB.Model(&sub).Update("send_lease_until", at.Unix()+30).Error)
			}
			// sub is a local GORM update target; pass the original immutable owner.
			var before RoutingHealth
			require.NoError(t, s.DB.First(&before).Error)
			ok, err := s.RenewTerminalSendingProbe(sub.SubmissionID, "sender-one", c, at)
			require.NoError(t, err)
			require.False(t, ok)
			var after RoutingHealth
			require.NoError(t, s.DB.First(&after, "id = ?", before.ID).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestRoutingHealthTerminalSendingDatabaseClockAndExpiry(t *testing.T) {
	s, c, sub := routingTerminalSendingTest(t, time.Now().UTC().Truncate(time.Second))
	ok, err := s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, time.Time{})
	require.NoError(t, err)
	require.True(t, ok)
	n, err := routingDBNow(s.DB, time.Time{})
	require.NoError(t, err)
	require.NoError(t, s.DB.Model(&sub).Update("send_lease_until", n-1).Error)
	ok, err = s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, time.Time{})
	require.NoError(t, err)
	require.False(t, ok, "expired DB-clock send lease cannot renew terminal")
	// A maximum-24h probe cannot be extended by a continuously live sender.
	now := time.Unix(n, 0)
	require.NoError(t, s.DB.Model(&sub).Update("send_lease_until", n+60).Error)
	require.NoError(t, s.DB.Model(&RoutingHealth{}).Where("id = ?", c.Key.id(c.Stage)).Update("probe_started_at", n-86400+5).Error)
	ok, err = s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, now)
	require.NoError(t, err)
	require.True(t, ok)
	view, err := s.ReadHealth(c.Key, c.Stage, 0, now)
	require.NoError(t, err)
	require.EqualValues(t, n+5, view.Health.ProbeUntil)
	ok, err = s.RenewTerminalSendingProbe(sub.SubmissionID, sub.SendOwnerID, c, now.Add(6*time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	view, err = s.ReadHealth(c.Key, c.Stage, 0, now.Add(6*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.Equal(t, "maximum_24h", view.Health.LastProbeExpiryReason)
	// Acceptance cannot restore an already expired terminal epoch.
	until := view.Health.ProbeUntil
	require.NoError(t, s.SaveTerminalSubmission(c.Key, sub.SubmissionID, sub.ChannelID, "accepted", "late-task", now.Add(7*time.Second)))
	view, err = s.ReadHealth(c.Key, c.Stage, 0, now.Add(7*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.Equal(t, until, view.Health.ProbeUntil)
}
func routingHealthTestEvent(t *testing.T, s RoutingMetricsStore, k RoutingMetricKey, stage RoutingMetricStage, id, outcome string, now time.Time) RoutingHealthResult {
	t.Helper()
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: stage, EventID: id, Outcome: outcome, Sent: true, OccurredAt: now}, now)
	require.NoError(t, err)
	return r
}
func routingHealthTestOpen(t *testing.T, s RoutingMetricsStore, k RoutingMetricKey, stage RoutingMetricStage, now time.Time) RoutingHealth {
	t.Helper()
	var h RoutingHealth
	for i := 0; i < 3; i++ {
		h = routingHealthTestEvent(t, s, k, stage, fmt.Sprintf("%s-fail-%d", stage, i), "failure", now.Add(time.Duration(i)*time.Second)).Health
	}
	require.Equal(t, RoutingHealthOpen, h.State)
	return h
}

func TestRoutingMetricsSnapshotRevisionAndLeaseExpiry(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	b := RoutingMetricBucket{RoutingMetricKey: k, Stage: RoutingStageAttempt, NodeBootID: "boot-a", ChannelID: 1, Minute: now.Unix(), Revision: 1, Eligible: 2, Success: 1, Failure: 1, Sent: 2, SpeedSamples: 1, SpeedTotalMS: 200, LastObservationAt: now.Unix()}
	applied, err := s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	require.False(t, applied)
	b.Revision = 2
	b.Success = 3
	b.Eligible = 4
	b.Sent = 4
	applied, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	require.True(t, applied)
	b.Revision = 1
	b.Success = 99
	b.Eligible = 100
	applied, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	require.False(t, applied)
	b.Revision = 1
	b.NodeBootID = "boot-b"
	b.Success = 1
	b.Eligible = 2
	_, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	b.ChannelID = 2
	b.Success = 9
	b.Eligible = 10
	_, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	b.ChannelID = 1
	b.ProviderKey = "other-provider"
	_, err = s.UpsertSnapshot(b, now)
	require.NoError(t, err)
	lease := RoutingMetricLease{RoutingMetricKey: k, Stage: RoutingStageAttempt, ChannelID: 1, LeaseID: "lease-a"}
	require.NoError(t, s.StartLease(lease, now))
	require.NoError(t, s.StartLease(lease, now))
	fold, err := s.Fold(k, RoutingStageAttempt, []int{1}, now.Add(-time.Hour), now)
	require.NoError(t, err)
	require.EqualValues(t, 6, fold.Eligible)
	require.EqualValues(t, 4, fold.Success)
	require.EqualValues(t, 1, fold.Active)
	renewed, err := s.RenewLease("lease-a", now.Add(10*time.Second))
	require.NoError(t, err)
	require.True(t, renewed)
	fold, err = s.Fold(k, RoutingStageAttempt, []int{1}, now.Add(-time.Hour), now.Add(39*time.Second))
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Active)
	fold, err = s.Fold(k, RoutingStageAttempt, []int{1}, now.Add(-time.Hour), now.Add(40*time.Second))
	require.NoError(t, err)
	require.Zero(t, fold.Active)
	renewed, err = s.RenewLease("lease-a", now.Add(40*time.Second))
	require.NoError(t, err)
	require.False(t, renewed)
	require.NoError(t, s.FinishLease("lease-a"))
	require.NoError(t, s.FinishLease("lease-a"))
	fold, err = s.Fold(k, RoutingStageAttempt, nil, now.Add(-time.Hour), now)
	require.NoError(t, err)
	require.Zero(t, fold)
}

func TestRoutingHealthApplySequenceDuplicateLateAndIgnored(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	for i, outcome := range []string{"failure", "failure", "success", "failure", "ignored", "failure"} {
		r := routingHealthTestEvent(t, s, k, RoutingStageAttempt, fmt.Sprintf("event-%d", i), outcome, now.Add(time.Duration(i)*time.Second))
		require.Equal(t, RoutingHealthClosed, r.Health.State)
	}
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: "event-5", Outcome: "failure", Sent: true}, now.Add(6*time.Second))
	require.NoError(t, err)
	require.True(t, r.Duplicate)
	require.EqualValues(t, 6, r.Health.EventSeq)
	r, err = s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: "late", Outcome: "failure", OccurredAt: now.Add(-301 * time.Second)}, now.Add(6*time.Second))
	require.NoError(t, err)
	require.True(t, r.Late)
	require.EqualValues(t, 6, r.Health.EventSeq)
	r = routingHealthTestEvent(t, s, k, RoutingStageAttempt, "last-failure", "failure", now.Add(7*time.Second))
	require.Equal(t, RoutingHealthOpen, r.Health.State)
	require.EqualValues(t, 7, r.Health.EventSeq)
	require.EqualValues(t, 30, r.Health.Backoff)
	view, err := s.ReadHealth(k, RoutingStageAttempt, 0, now.Add(8*time.Second))
	require.NoError(t, err)
	require.EqualValues(t, 6, view.Eligible5m)
	require.EqualValues(t, 5, view.Failures5m)
	// A minute aggregate never touches the health event sequence.
	_, err = s.UpsertSnapshot(RoutingMetricBucket{RoutingMetricKey: k, Stage: RoutingStageAttempt, NodeBootID: "boot", ChannelID: 1, Minute: now.Unix(), Revision: 1, Eligible: 100, Failure: 100, Sent: 100}, now)
	require.NoError(t, err)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 0, now.Add(309*time.Second))
	require.NoError(t, err)
	require.Zero(t, view.Eligible5m)
	require.EqualValues(t, 7, view.Health.EventSeq)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
}

func TestRoutingHealthConsecutiveFailureRollingWindow(t *testing.T) {
	for _, stage := range []RoutingMetricStage{RoutingStageAttempt, RoutingStageTaskTerminal} {
		t.Run(string(stage), func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				seconds []int
				success int
				want    string
			}{
				{"retain recent failures", []int{0, 50, 61, 70}, -1, RoutingHealthOpen},
				{"inclusive sixty seconds", []int{0, 30, 60}, -1, RoutingHealthOpen},
				{"expire only old failures", []int{0, 30, 61}, -1, RoutingHealthClosed},
				{"expire entire old prefix", []int{0, 10, 71, 80}, -1, RoutingHealthClosed},
				{"success breaks sequence", []int{0, 50, 60, 61, 70}, 2, RoutingHealthClosed},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := routingMetricsTestStore(t)
					now := routingMetricsTestNow()
					key := routingMetricsTestKey()
					if stage == RoutingStageTaskTerminal {
						key.ProtocolClass = "async"
					}
					var h RoutingHealth
					for i, second := range tc.seconds {
						outcome := "failure"
						if i == tc.success {
							outcome = "success"
						}
						h = routingHealthTestEvent(t, s, key, stage, fmt.Sprintf("event-%d", i), outcome, now.Add(time.Duration(second)*time.Second)).Health
						if i < len(tc.seconds)-1 {
							require.Equal(t, RoutingHealthClosed, h.State)
						}
					}
					require.Equal(t, tc.want, h.State)
					if tc.want == RoutingHealthOpen {
						require.EqualValues(t, 30, h.Backoff)
						require.Equal(t, now.Unix()+int64(tc.seconds[len(tc.seconds)-1])+30, h.CooldownUntil)
					}
				})
			}
		})
	}
}

func TestRoutingHealthHalfOpenEpochCompetitionAndExpiry(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	h := routingHealthTestOpen(t, s, k, RoutingStageAttempt, now)
	claimAt := time.Unix(h.CooldownUntil, 0)
	var wg sync.WaitGroup
	claims := make(chan RoutingProbeClaim, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, ok, err := s.ClaimProbe(k, RoutingStageAttempt, fmt.Sprintf("probe-%d", i), claimAt)
			if err != nil {
				errs <- err
			}
			if ok && err == nil {
				claims <- c
			}
		}(i)
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, claims, 1)
	old := <-claims
	view, err := s.ReadHealth(k, RoutingStageAttempt, 0, claimAt.Add(30*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.EqualValues(t, 60, view.Health.Backoff)
	c, ok, err := s.ClaimProbe(k, RoutingStageAttempt, "probe-new", time.Unix(view.Health.CooldownUntil, 0))
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, c.Epoch, old.Epoch)
	at := time.Unix(view.Health.CooldownUntil+1, 0)
	before, err := s.ReadHealth(k, RoutingStageAttempt, 0, at)
	require.NoError(t, err)
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: "old-result", Outcome: "success", Probe: &old}, at)
	require.NoError(t, err)
	require.Equal(t, RoutingHealthHalfOpen, r.Health.State)
	require.Equal(t, c.Epoch, r.Health.BreakerEpoch)
	require.Equal(t, before.Health.EventSeq, r.Health.EventSeq, "expired epoch must not mutate current health window")
	renewed, err := s.RenewProbe(old, at)
	require.NoError(t, err)
	require.False(t, renewed)
	renewed, err = s.RenewProbe(c, at.Add(10*time.Second))
	require.NoError(t, err)
	require.True(t, renewed)
	r, err = s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: "new-result", Outcome: "success", Probe: &c}, at.Add(11*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthClosed, r.Health.State)
}

func TestRoutingHealthWindowThresholds(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	for i := 0; i < 20; i++ {
		outcome := "success"
		if i%2 == 0 {
			outcome = "failure"
		}
		r := routingHealthTestEvent(t, s, k, RoutingStageAttempt, fmt.Sprintf("ratio-%d", i), outcome, now.Add(time.Duration(i)*time.Second))
		if i < 19 {
			require.Equal(t, RoutingHealthClosed, r.Health.State)
		} else {
			require.Equal(t, RoutingHealthOpen, r.Health.State)
		}
	}
	k.OperationKey = "responses"
	for _, id := range []string{"capacity-one", "capacity-two"} {
		require.NoError(t, s.StartLease(RoutingMetricLease{RoutingMetricKey: k, Stage: RoutingStageAttempt, ChannelID: 1, LeaseID: id}, now))
	}
	for i := 0; i < 5; i++ {
		outcome := "success"
		if i == 0 {
			outcome = "failure"
		}
		_, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: fmt.Sprintf("429-%d", i), Outcome: outcome, Sent: true, RateLimited: i == 0, SuccessActive: 2}, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}
	view, err := s.ReadHealth(k, RoutingStageAttempt, 0, now.Add(5*time.Second))
	require.NoError(t, err)
	require.True(t, view.Congested)
	require.EqualValues(t, 1, view.RateLimited60s)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 0, now.Add(60*time.Second))
	require.NoError(t, err)
	require.False(t, view.Congested)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 2, now.Add(60*time.Second))
	require.NoError(t, err)
	require.True(t, view.Congested)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 2, now.Add(305*time.Second))
	require.NoError(t, err)
	require.False(t, view.Congested)
}

func TestRoutingHealthExplicitProbeKeepsExclusiveLease(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	h := routingHealthTestOpen(t, s, k, RoutingStageAttempt, now)
	at := time.Unix(h.CooldownUntil-20, 0)
	view, err := s.ReadHealth(k, RoutingStageAttempt, 0, at)
	require.NoError(t, err)
	require.False(t, view.ProbeReady)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 0, time.Unix(h.CooldownUntil, 0))
	require.NoError(t, err)
	require.True(t, view.ProbeReady)
	_, ok, err := s.ClaimProbe(k, RoutingStageAttempt, "ordinary", at)
	require.NoError(t, err)
	require.False(t, ok)
	c, ok, err := s.ClaimProbeExplicit(k, RoutingStageAttempt, "explicit", at)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = s.ClaimProbeExplicit(k, RoutingStageAttempt, "other-explicit", at.Add(time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = s.ReleaseProbe(c, at.Add(2*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	view, err = s.ReadHealth(k, RoutingStageAttempt, 0, at.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.Equal(t, h.Backoff, view.Health.Backoff)
}

func TestRoutingHealthTerminalObserverTakeoverAndStages(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	k.ProtocolClass = "async"
	k.OperationKey = "video.create"
	now := routingMetricsTestNow()
	h := routingHealthTestOpen(t, s, k, RoutingStageTaskTerminal, now)
	at := time.Unix(h.CooldownUntil, 0)
	c, ok, err := s.ClaimProbe(k, RoutingStageTaskTerminal, "terminal-probe", at)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, s.DB.Transaction(func(tx *gorm.DB) error {
		return (RoutingMetricsStore{DB: tx}).BindTerminalProbe(c, "submission", 1, at)
	}))
	_, ok, err = s.ClaimTerminalObserver(c, "submission", "owner-early", at)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.DB.Transaction(func(tx *gorm.DB) error {
		return (RoutingMetricsStore{DB: tx}).SaveTerminalSubmission(k, "submission", 1, "accepted", "", at)
	}))
	require.NoError(t, s.SaveTerminalSubmission(k, "submission", 1, "accepted", "task-1", at))
	fold, err := s.Fold(k, RoutingStageTaskTerminal, []int{1}, now.Add(-time.Hour), at)
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Active)
	view, err := s.ReadHealth(k, RoutingStageTaskTerminal, 1, at)
	require.NoError(t, err)
	require.Equal(t, RoutingHealthHalfOpen, view.Health.State)
	observer, ok, err := s.ClaimTerminalObserver(c, "submission", "owner-a", at)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = s.ClaimTerminalObserver(c, "submission", "owner-b", at.Add(time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = s.RenewTerminalObserver(observer, true, at.Add(10*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	next, ok, err := s.ClaimTerminalObserver(c, "submission", "owner-b", at.Add(25*time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, next.Generation, observer.Generation)
	ok, err = s.RenewTerminalObserver(observer, true, at.Add(26*time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	input := RoutingHealthInput{Key: k, Stage: RoutingStageTaskTerminal, EventID: "terminal", SubmissionID: "submission", Outcome: "success", Probe: &c, ObserverOwner: observer.Owner, ObserverGeneration: observer.Generation}
	_, err = s.ApplyHealthEvent(input, at.Add(26*time.Second))
	require.ErrorIs(t, err, ErrRoutingMetricsConflict, "stale observer must not consume terminal event")
	input.ObserverOwner = next.Owner
	input.ObserverGeneration = next.Generation
	r, err := s.ApplyHealthEvent(input, at.Add(26*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthClosed, r.Health.State)
	fold, err = s.Fold(k, RoutingStageTaskTerminal, []int{1}, now.Add(-time.Hour), at.Add(27*time.Second))
	require.NoError(t, err)
	require.Zero(t, fold.Active)
	attempt, err := s.ReadHealth(k, RoutingStageAttempt, 0, at.Add(27*time.Second))
	require.NoError(t, err)
	require.Zero(t, attempt.Health.EventSeq)
}

func TestRoutingHealthTerminalUnknownReleaseAndLateFinish(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	k.ProtocolClass = "async"
	now := routingMetricsTestNow()
	h := routingHealthTestOpen(t, s, k, RoutingStageTaskTerminal, now)
	at := time.Unix(h.CooldownUntil, 0)
	c, ok, err := s.ClaimProbe(k, RoutingStageTaskTerminal, "probe", at)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, s.BindTerminalProbe(c, "unknown-submission", 1, at))
	require.NoError(t, s.SaveTerminalSubmission(k, "unknown-submission", 1, "unknown", "", at.Add(time.Second)))
	view, err := s.ReadHealth(k, RoutingStageTaskTerminal, 1, at.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.EqualValues(t, 30, view.Health.Backoff)
	require.EqualValues(t, 3, view.Health.EventSeq)
	require.NoError(t, s.SaveTerminalSubmission(k, "unknown-submission", 1, "accepted", "task-later", at.Add(3*time.Second)))
	_, ok, err = s.ClaimTerminalObserver(c, "unknown-submission", "late-owner", at.Add(4*time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageTaskTerminal, EventID: "very-late-terminal", SubmissionID: "unknown-submission", Outcome: "success", OccurredAt: at, Probe: &c}, at.Add(301*time.Second))
	require.NoError(t, err)
	require.True(t, r.Late)
	require.Equal(t, RoutingHealthOpen, r.Health.State)
	fold, err := s.Fold(k, RoutingStageTaskTerminal, []int{1}, now, at.Add(302*time.Second))
	require.NoError(t, err)
	require.Zero(t, fold.Active, "late true terminal must release long task active")
}

func TestRoutingMetricsDatabaseClockAndErrors(t *testing.T) {
	s := routingMetricsTestStore(t)
	n, err := routingDBNow(s.DB, time.Time{})
	require.NoError(t, err)
	require.InDelta(t, time.Now().Unix(), n, 3)
	_, err = (RoutingMetricsStore{}).ReadHealth(routingMetricsTestKey(), RoutingStageAttempt, 0, time.Now())
	require.ErrorIs(t, err, ErrRoutingMetricsUnavailable)
	require.ErrorIs(t, MigrateRoutingMetrics(nil), ErrRoutingMetricsUnavailable)
	require.NoError(t, s.DB.Migrator().DropTable(&RoutingHealth{}))
	_, err = s.ReadHealth(routingMetricsTestKey(), RoutingStageAttempt, 0, time.Now())
	require.Error(t, err)
}

func TestRoutingHealthReadOnlyProjectionAndTerminal24HourLimit(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	_, err := s.ReadHealth(k, RoutingStageAttempt, 0, now)
	require.NoError(t, err)
	var count int64
	require.NoError(t, s.DB.Model(&RoutingHealth{}).Count(&count).Error)
	require.Zero(t, count, "catalog read must not insert a health row")
	k.ProtocolClass = "async"
	h := routingHealthTestOpen(t, s, k, RoutingStageTaskTerminal, now)
	at := time.Unix(h.CooldownUntil, 0)
	c, ok, err := s.ClaimProbe(k, RoutingStageTaskTerminal, "day-long-probe", at)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, s.BindTerminalProbe(c, "day-long-submission", 1, at))
	require.NoError(t, s.SaveTerminalSubmission(k, "day-long-submission", 1, "accepted", "task-day", at))
	o, ok, err := s.ClaimTerminalObserver(c, "day-long-submission", "owner", at)
	require.NoError(t, err)
	require.True(t, ok)
	// Arrange the persisted result of uninterrupted periodic renewals near 24h.
	deadline := at.Add(24 * time.Hour)
	require.NoError(t, s.DB.Model(&RoutingHealth{}).Where("id = ?", k.id(RoutingStageTaskTerminal)).Updates(map[string]interface{}{"probe_until": deadline.Unix(), "observer_until": deadline.Unix()}).Error)
	ok, err = s.RenewTerminalObserver(o, true, deadline.Add(-time.Second))
	require.NoError(t, err)
	require.True(t, ok)
	var before RoutingHealth
	require.NoError(t, s.DB.First(&before, "id = ?", k.id(RoutingStageTaskTerminal)).Error)
	require.Equal(t, deadline.Unix(), before.ProbeUntil)
	view, err := s.ReadHealth(k, RoutingStageTaskTerminal, 1, deadline)
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.Greater(t, view.Health.BreakerEpoch, c.Epoch)
	var after RoutingHealth
	require.NoError(t, s.DB.First(&after, "id = ?", before.ID).Error)
	require.Equal(t, before, after, "expiry projection must not persist catalog writes")
	ok, err = s.RenewTerminalObserver(o, true, deadline)
	require.NoError(t, err)
	require.False(t, ok)
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageTaskTerminal, EventID: "after-day-terminal", SubmissionID: "day-long-submission", Outcome: "success", Probe: &c}, deadline.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, r.Health.State)
	require.False(t, r.Applied)
	fold, err := s.Fold(k, RoutingStageTaskTerminal, []int{1}, now, deadline.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, fold.Active)
}

func TestRoutingHealthTerminalBindingTransactionRollback(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	k.ProtocolClass = "async"
	now := routingMetricsTestNow()
	h := routingHealthTestOpen(t, s, k, RoutingStageTaskTerminal, now)
	at := time.Unix(h.CooldownUntil, 0)
	c, ok, err := s.ClaimProbe(k, RoutingStageTaskTerminal, "tx-probe", at)
	require.NoError(t, err)
	require.True(t, ok)
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := (RoutingMetricsStore{DB: tx}).BindTerminalProbe(c, "rollback-submission", 1, at); err != nil {
			return err
		}
		return ErrRoutingMetricsConflict
	})
	require.ErrorIs(t, err, ErrRoutingMetricsConflict)
	var count int64
	require.NoError(t, s.DB.Model(&RoutingTerminalObservation{}).Count(&count).Error)
	require.Zero(t, count)
	view, err := s.ReadHealth(k, RoutingStageTaskTerminal, 0, at)
	require.NoError(t, err)
	require.Empty(t, view.Health.ProbeSubmissionID)
}

func TestRoutingMetricsCleanupRetainsUnresolvedHealthAndTasks(t *testing.T) {
	s := routingMetricsTestStore(t)
	k := routingMetricsTestKey()
	now := routingMetricsTestNow()
	routingHealthTestOpen(t, s, k, RoutingStageAttempt, now)
	_, err := s.UpsertSnapshot(RoutingMetricBucket{RoutingMetricKey: k, Stage: RoutingStageAttempt, NodeBootID: "cleanup-boot", ChannelID: 1, Minute: now.Unix(), Revision: 1, Eligible: 1, Failure: 1, Sent: 1}, now)
	require.NoError(t, err)
	require.NoError(t, s.StartLease(RoutingMetricLease{RoutingMetricKey: k, Stage: RoutingStageAttempt, ChannelID: 1, LeaseID: "cleanup-lease"}, now))
	taskKey := k
	taskKey.ProtocolClass = "async"
	require.NoError(t, s.SaveTerminalSubmission(taskKey, "retained-unknown", 1, "unknown", "", now))
	later := now.Add(49 * time.Hour)
	require.NoError(t, s.Cleanup(later))
	for _, table := range []interface{}{&RoutingMetricBucket{}, &RoutingMetricLease{}, &RoutingHealthEvent{}} {
		var count int64
		require.NoError(t, s.DB.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
	view, err := s.ReadHealth(k, RoutingStageAttempt, 0, later)
	require.NoError(t, err)
	require.Equal(t, RoutingHealthOpen, view.Health.State)
	require.EqualValues(t, 3, view.Health.EventSeq)
	r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: "attempt-fail-0", Outcome: "failure", OccurredAt: now}, later)
	require.NoError(t, err)
	require.True(t, r.Late)
	require.EqualValues(t, 3, r.Health.EventSeq)
	fold, err := s.Fold(taskKey, RoutingStageTaskTerminal, []int{1}, now, later)
	require.NoError(t, err)
	require.EqualValues(t, 1, fold.Active, "metrics retention must not lose unresolved submissions")
}

func TestRoutingMetricsRealDatabaseMigration(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("TEST_MYSQL_DSN")
			var driver gorm.Dialector
			if dialect == "postgres" {
				dsn = os.Getenv("TEST_POSTGRES_DSN")
				driver = postgres.Open(dsn)
			} else {
				driver = mysql.Open(dsn)
			}
			if dsn == "" {
				t.Skip("real database DSN not configured; not run")
			}
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, MigrateRoutingMetrics(db))
			require.NoError(t, MigrateRoutingMetrics(db))
			_, err = routingDBNow(db, time.Time{})
			require.NoError(t, err)
			// Unique keys isolate real-database checks without touching package-global DB.
			s := RoutingMetricsStore{DB: db}
			k := routingMetricsTestKey()
			k.ClientModel = fmt.Sprintf("routing-metrics-test-%d", time.Now().UnixNano())
			variant := k.id(RoutingStageAttempt)
			t.Cleanup(func() {
				require.NoError(t, db.Where("variant_id = ?", variant).Delete(&RoutingMetricBucket{}).Error)
				require.NoError(t, db.Where("variant_id = ?", variant).Delete(&RoutingMetricLease{}).Error)
				require.NoError(t, db.Where("variant_id = ?", variant).Delete(&RoutingHealthEvent{}).Error)
				require.NoError(t, db.Where("id = ?", variant).Delete(&RoutingHealth{}).Error)
			})
			now := routingMetricsTestNow()
			b := RoutingMetricBucket{RoutingMetricKey: k, Stage: RoutingStageAttempt, NodeBootID: k.ClientModel, ChannelID: 1, Minute: now.Unix(), Revision: 1, Eligible: 1, Success: 1, Sent: 1}
			applied, err := s.UpsertSnapshot(b, now)
			require.NoError(t, err)
			require.True(t, applied)
			applied, err = s.UpsertSnapshot(b, now)
			require.NoError(t, err)
			require.False(t, applied)
			var h RoutingHealth
			for i := 0; i < 3; i++ {
				h = routingHealthTestEvent(t, s, k, RoutingStageAttempt, fmt.Sprintf("%s-%d", k.ClientModel, i), "failure", now.Add(time.Duration(i)*time.Second)).Health
			}
			require.Equal(t, RoutingHealthOpen, h.State)
			claim, ok, err := s.ClaimProbe(k, RoutingStageAttempt, k.ClientModel, time.Unix(h.CooldownUntil, 0))
			require.NoError(t, err)
			require.True(t, ok)
			_, ok, err = s.ClaimProbe(k, RoutingStageAttempt, k.ClientModel+"-other", time.Unix(h.CooldownUntil, 0))
			require.NoError(t, err)
			require.False(t, ok)
			r, err := s.ApplyHealthEvent(RoutingHealthInput{Key: k, Stage: RoutingStageAttempt, EventID: k.ClientModel + "-probe", Outcome: "success", Probe: &claim}, time.Unix(h.CooldownUntil+1, 0))
			require.NoError(t, err)
			require.Equal(t, RoutingHealthClosed, r.Health.State)
		})
	}
}
