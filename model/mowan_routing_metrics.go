package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RoutingMetricStage string

const (
	RoutingStageAttempt      RoutingMetricStage = "attempt"
	RoutingStageTaskTerminal RoutingMetricStage = "task_terminal"
	RoutingHealthClosed                         = "closed"
	RoutingHealthOpen                           = "open"
	RoutingHealthHalfOpen                       = "half_open"
)

var (
	ErrRoutingMetricsUnavailable = errors.New("routing metrics database unavailable")
	ErrRoutingMetricsConflict    = errors.New("routing metrics version or lease conflict")
)

// RoutingMetricKey is the immutable server-resolved variant, independent of stage.
type RoutingMetricKey struct {
	ClientModel   string `gorm:"type:varchar(255);not null"`
	BusinessGroup string `gorm:"type:varchar(255);not null"`
	ProtocolClass string `gorm:"type:varchar(32);not null"`
	ProviderKey   string `gorm:"type:varchar(255);not null"`
	OperationKey  string `gorm:"type:varchar(255);not null"`
}

func (k RoutingMetricKey) validate(stage RoutingMetricStage) error {
	if k.ClientModel == "" || k.BusinessGroup == "" || k.ProviderKey == "" || k.OperationKey == "" || len(k.ClientModel) > 255 || len(k.BusinessGroup) > 255 || len(k.ProviderKey) > 255 || len(k.OperationKey) > 255 {
		return errors.New("routing metrics: invalid variant key")
	}
	switch k.ProtocolClass {
	case "sync", "stream", "async", "responses_ws", "realtime":
	default:
		return errors.New("routing metrics: invalid protocol class")
	}
	if stage != RoutingStageAttempt && stage != RoutingStageTaskTerminal {
		return errors.New("routing metrics: invalid stage")
	}
	if stage == RoutingStageTaskTerminal && k.ProtocolClass != "async" {
		return errors.New("routing metrics: terminal stage requires async protocol")
	}
	return nil
}

func routingMetricID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:", len(p))
		_, _ = h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (k RoutingMetricKey) id(stage RoutingMetricStage) string {
	return routingMetricID(k.ClientModel, k.BusinessGroup, k.ProtocolClass, k.ProviderKey, k.OperationKey, string(stage))
}

// Hash primary keys avoid MySQL 5.7 composite-index byte limits; full dimensions are retained.
type RoutingMetricBucket struct {
	ID                string `gorm:"type:varchar(64);primaryKey"`
	RoutingMetricKey  `gorm:"embedded"`
	Stage             RoutingMetricStage `gorm:"type:varchar(32);not null"`
	VariantID         string             `gorm:"type:varchar(64);not null;index:idx_routing_bucket_fold,priority:1"`
	NodeBootID        string             `gorm:"type:varchar(128);not null"`
	ChannelID         int                `gorm:"not null;index:idx_routing_bucket_fold,priority:2"`
	Minute            int64              `gorm:"not null;index:idx_routing_bucket_fold,priority:3;index"`
	Revision          int64              `gorm:"not null"`
	Eligible          int64
	Success           int64
	Failure           int64
	Ignored           int64
	Sent              int64
	RateLimited       int64
	SpeedSamples      int64
	SpeedTotalMS      float64
	LastObservationAt int64
	UpdatedAt         int64 `gorm:"autoUpdateTime:false"`
}

func (RoutingMetricBucket) TableName() string { return "mowan_routing_metric_buckets" }

type RoutingMetricLease struct {
	LeaseID          string `gorm:"type:varchar(128);primaryKey"`
	RoutingMetricKey `gorm:"embedded"`
	Stage            RoutingMetricStage `gorm:"type:varchar(32);not null"`
	VariantID        string             `gorm:"type:varchar(64);not null;index:idx_routing_lease_active,priority:1"`
	ChannelID        int                `gorm:"not null;index:idx_routing_lease_active,priority:2"`
	ExpiresAt        int64              `gorm:"not null;index:idx_routing_lease_active,priority:3;index"`
}

func (RoutingMetricLease) TableName() string { return "mowan_routing_leases" }

type RoutingHealth struct {
	ID                    string `gorm:"type:varchar(64);primaryKey"`
	RoutingMetricKey      `gorm:"embedded"`
	Stage                 RoutingMetricStage `gorm:"type:varchar(32);not null"`
	Version               int64              `gorm:"not null"`
	EventSeq              int64
	LastAppliedAt         int64
	FailureTail           string `gorm:"type:text"`
	SecondSlots           string `gorm:"type:text"`
	State                 string `gorm:"type:varchar(16);not null"`
	CooldownUntil         int64
	Backoff               int64
	BreakerEpoch          int64
	ProbeLeaseID          string `gorm:"type:varchar(128)"`
	ProbeUntil            int64
	ProbeStartedAt        int64
	LastProbeExpiredAt    int64
	LastProbeExpiryReason string `gorm:"type:varchar(32)"`
	LastTriggerAt         int64
	LastEventAt           int64  `gorm:"index"`
	ObserverOwner         string `gorm:"type:varchar(128)"`
	ObserverGeneration    int64
	ObserverUntil         int64
	ProbeSubmissionID     string `gorm:"type:varchar(128);index"`
}

func (RoutingHealth) TableName() string { return "mowan_routing_health" }

type RoutingHealthEvent struct {
	EventID    string `gorm:"type:varchar(128);primaryKey"`
	VariantID  string `gorm:"type:varchar(64);not null;index"`
	EventSeq   int64
	OccurredAt int64
	AppliedAt  int64 `gorm:"index"`
}

func (RoutingHealthEvent) TableName() string { return "mowan_routing_health_events" }

// RoutingTerminalObservation is written in the accepted submission transaction. It also
// deduplicates long-running active across the submission and its eventual local Task.
type RoutingTerminalObservation struct {
	SubmissionID     string `gorm:"type:varchar(128);primaryKey"`
	RoutingMetricKey `gorm:"embedded"`
	VariantID        string `gorm:"type:varchar(64);not null;index:idx_routing_terminal_active,priority:1"`
	ChannelID        int
	TaskID           string `gorm:"type:varchar(128);index"`
	Status           string `gorm:"type:varchar(16);not null;index:idx_routing_terminal_active,priority:2"`
	BreakerEpoch     int64
	ProbeLeaseID     string `gorm:"type:varchar(128)"`
	AcceptedAt       int64
	TerminalAt       int64
	UpdatedAt        int64 `gorm:"autoUpdateTime:false"`
}

func (RoutingTerminalObservation) TableName() string { return "mowan_routing_terminal_observations" }

// Use an existing transaction as DB to atomically bind to prepared/accepted facts.
// A zero now reads the primary DB clock; explicit now is for deterministic tests.
type RoutingMetricsStore struct{ DB *gorm.DB }

func MigrateRoutingMetrics(db *gorm.DB) error {
	if db == nil {
		return ErrRoutingMetricsUnavailable
	}
	return db.AutoMigrate(&RoutingMetricBucket{}, &RoutingMetricLease{}, &RoutingHealth{}, &RoutingHealthEvent{}, &RoutingTerminalObservation{})
}

func routingDBNow(db *gorm.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, ErrRoutingMetricsUnavailable
	}
	if !now.IsZero() {
		return now.Unix(), nil
	}
	var query string
	switch db.Dialector.Name() {
	case "sqlite":
		query = "SELECT CAST(strftime('%s', 'now') AS INTEGER)"
	case "mysql":
		query = "SELECT UNIX_TIMESTAMP()"
	case "postgres":
		query = "SELECT CAST(FLOOR(EXTRACT(EPOCH FROM clock_timestamp())) AS BIGINT)"
	default:
		return 0, fmt.Errorf("routing metrics database clock: unsupported dialect %s", db.Dialector.Name())
	}
	var n int64
	if err := db.Raw(query).Row().Scan(&n); err != nil {
		return 0, fmt.Errorf("routing metrics database clock: %w", err)
	}
	return n, nil
}

func (s RoutingMetricsStore) UpsertSnapshot(b RoutingMetricBucket, now time.Time) (bool, error) {
	if err := b.RoutingMetricKey.validate(b.Stage); err != nil {
		return false, err
	}
	if b.NodeBootID == "" || len(b.NodeBootID) > 128 || b.ChannelID <= 0 || b.Minute%60 != 0 || b.Revision < 1 || b.Eligible < 0 || b.Success < 0 || b.Failure < 0 || b.Ignored < 0 || b.Sent < 0 || b.RateLimited < 0 || b.SpeedSamples < 0 || b.SpeedTotalMS < 0 || b.Success+b.Failure != b.Eligible || b.RateLimited > b.Sent || b.SpeedSamples > b.Success || b.SpeedTotalMS != b.SpeedTotalMS || b.SpeedTotalMS > 1e300 {
		return false, errors.New("routing metrics: invalid absolute minute snapshot")
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return false, err
	}
	b.VariantID = b.RoutingMetricKey.id(b.Stage)
	b.ID = routingMetricID(b.NodeBootID, b.VariantID, strconv.Itoa(b.ChannelID), strconv.FormatInt(b.Minute, 10))
	b.UpdatedAt = n
	applied := false
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// Insert the neutral row first, then use a portable conditional update. MySQL
		// does not support OnConflict.Where for revision checks.
		seed := b
		seed.Revision = 0
		r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed)
		if r.Error != nil {
			return r.Error
		}
		r = tx.Model(&RoutingMetricBucket{}).Where("id = ? AND revision < ?", b.ID, b.Revision).Select("*").Updates(&b)
		applied = r.RowsAffected > 0
		return r.Error
	})
	return applied, err
}

type RoutingMetricFold struct {
	Eligible          int64
	Success           int64
	Failure           int64
	Ignored           int64
	Sent              int64
	RateLimited       int64
	SpeedSamples      int64
	SpeedTotalMS      float64
	LastObservationAt int64
	Active            int64
}

// legalChannelIDs must be freshly obtained from the caller's hard authorization filters.
func (s RoutingMetricsStore) Fold(k RoutingMetricKey, stage RoutingMetricStage, legalChannelIDs []int, since time.Time, now time.Time) (RoutingMetricFold, error) {
	var out RoutingMetricFold
	if err := k.validate(stage); err != nil {
		return out, err
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return out, err
	}
	if len(legalChannelIDs) == 0 {
		return out, nil
	}
	var rows []RoutingMetricBucket
	err = s.DB.Where("variant_id = ? AND channel_id IN ? AND minute >= ? AND minute <= ?", k.id(stage), legalChannelIDs, since.Unix()/60*60, n/60*60).Find(&rows).Error
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Eligible += r.Eligible
		out.Success += r.Success
		out.Failure += r.Failure
		out.Ignored += r.Ignored
		out.Sent += r.Sent
		out.RateLimited += r.RateLimited
		out.SpeedSamples += r.SpeedSamples
		out.SpeedTotalMS += r.SpeedTotalMS
		if r.LastObservationAt > out.LastObservationAt {
			out.LastObservationAt = r.LastObservationAt
		}
	}
	out.Active, err = s.countActive(k, stage, legalChannelIDs, n)
	return out, err
}

// CountActive reads shared leases/observations; zero now uses the primary DB clock.
func (s RoutingMetricsStore) CountActive(k RoutingMetricKey, stage RoutingMetricStage, now time.Time) (int64, error) {
	if err := k.validate(stage); err != nil {
		return 0, err
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return 0, err
	}
	return s.countActive(k, stage, nil, n)
}

func (s RoutingMetricsStore) countActive(k RoutingMetricKey, stage RoutingMetricStage, channelIDs []int, n int64) (int64, error) {
	query := s.DB.Where("variant_id = ?", k.id(stage))
	if channelIDs != nil {
		query = query.Where("channel_id IN ?", channelIDs)
	}
	if stage == RoutingStageTaskTerminal {
		query = query.Model(&RoutingTerminalObservation{}).Where("status IN ?", []string{"accepted", "unknown"})
	} else {
		query = query.Model(&RoutingMetricLease{}).Where("expires_at > ?", n)
	}
	var active int64
	err := query.Count(&active).Error
	return active, err
}

func (s RoutingMetricsStore) StartLease(l RoutingMetricLease, now time.Time) error {
	if err := l.RoutingMetricKey.validate(l.Stage); err != nil {
		return err
	}
	if l.LeaseID == "" || len(l.LeaseID) > 128 || l.ChannelID <= 0 || l.Stage != RoutingStageAttempt {
		return errors.New("routing metrics: invalid attempt lease")
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return err
	}
	l.VariantID = l.RoutingMetricKey.id(l.Stage)
	l.ExpiresAt = n + 30
	r := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&l)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		var old RoutingMetricLease
		if err = s.DB.First(&old, "lease_id = ?", l.LeaseID).Error; err != nil {
			return err
		}
		if old.VariantID != l.VariantID || old.ChannelID != l.ChannelID {
			return ErrRoutingMetricsConflict
		}
	}
	return nil
}
func (s RoutingMetricsStore) RenewLease(id string, now time.Time) (bool, error) {
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return false, err
	}
	r := s.DB.Model(&RoutingMetricLease{}).Where("lease_id = ? AND expires_at > ?", id, n).Update("expires_at", n+30)
	return r.RowsAffected > 0, r.Error
}
func (s RoutingMetricsStore) FinishLease(id string) error {
	if s.DB == nil {
		return ErrRoutingMetricsUnavailable
	}
	return s.DB.Delete(&RoutingMetricLease{}, "lease_id = ?", id).Error
}

type routingSecondSlot struct {
	Second            int64 `json:"s"`
	Eligible          int64 `json:"n"`
	Failures          int64 `json:"f"`
	Sent              int64 `json:"d"`
	RateLimited       int64 `json:"r"`
	SuccessActivePeak int64 `json:"p"`
}
type routingHealthWindow struct {
	Tail  []int64
	Slots [300]routingSecondSlot
}

func (h *RoutingHealth) window() (routingHealthWindow, error) {
	var w routingHealthWindow
	if h.FailureTail != "" {
		if err := common.UnmarshalJsonStr(h.FailureTail, &w.Tail); err != nil {
			return w, fmt.Errorf("routing health %s failure tail: %w", h.ID, err)
		}
	}
	if h.SecondSlots != "" {
		if err := common.UnmarshalJsonStr(h.SecondSlots, &w.Slots); err != nil {
			return w, fmt.Errorf("routing health %s second slots: %w", h.ID, err)
		}
	}
	return w, nil
}
func (h *RoutingHealth) saveWindow(w routingHealthWindow) error {
	b, err := common.Marshal(w.Tail)
	if err != nil {
		return err
	}
	h.FailureTail = string(b)
	b, err = common.Marshal(w.Slots)
	if err != nil {
		return err
	}
	h.SecondSlots = string(b)
	return nil
}

// Each health mutation serializes on one variant row; version CAS also protects
// SQLite, whose row locks are deliberately omitted by lockForUpdate.
func (s RoutingMetricsStore) mutateHealth(k RoutingMetricKey, stage RoutingMetricStage, now time.Time, fn func(*gorm.DB, *RoutingHealth, int64) error) (RoutingHealth, error) {
	var h RoutingHealth
	if err := k.validate(stage); err != nil {
		return h, err
	}
	if s.DB == nil {
		return h, ErrRoutingMetricsUnavailable
	}
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		seed := RoutingHealth{ID: k.id(stage), RoutingMetricKey: k, Stage: stage, State: RoutingHealthClosed, Version: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&h, "id = ?", seed.ID).Error; err != nil {
			return err
		}
		n, err := routingDBNow(tx, now)
		if err != nil {
			return err
		}
		if n < h.LastAppliedAt {
			n = h.LastAppliedAt
		}
		version := h.Version
		original := h
		if err = fn(tx, &h, n); err != nil {
			return err
		}
		if h == original {
			return nil
		}
		h.Version++
		h.LastAppliedAt = n
		r := tx.Model(&RoutingHealth{}).Where("id = ? AND version = ?", h.ID, version).Select("*").Updates(&h)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrRoutingMetricsConflict
		}
		return nil
	})
	return h, err
}
func (h *RoutingHealth) open(now int64, escalate bool) {
	if escalate {
		if h.Backoff == 0 || now-h.LastTriggerAt >= 300 {
			h.Backoff = 30
		} else {
			h.Backoff *= 2
			if h.Backoff > 120 {
				h.Backoff = 120
			}
		}
	} else if h.Backoff == 0 {
		h.Backoff = 30
	}
	h.State = RoutingHealthOpen
	h.CooldownUntil = now + h.Backoff
	h.LastTriggerAt = now
	h.BreakerEpoch++
	h.ProbeLeaseID = ""
	h.ProbeUntil = 0
	h.ProbeStartedAt = 0
	h.ObserverOwner = ""
	h.ObserverUntil = 0
	h.ProbeSubmissionID = ""
}
func (h *RoutingHealth) expireProbe(now int64) {
	if h.State != RoutingHealthHalfOpen {
		return
	}
	expires := h.ProbeUntil
	if limit := h.ProbeStartedAt + 86400; limit < expires {
		expires = limit
	}
	if expires <= now {
		reason := "lease_expired"
		if expires == h.ProbeStartedAt+86400 {
			reason = "maximum_24h"
		}
		h.open(expires, true)
		h.LastProbeExpiredAt = expires
		h.LastProbeExpiryReason = reason
	}
}

type RoutingProbeClaim struct {
	Key     RoutingMetricKey
	Stage   RoutingMetricStage
	Epoch   int64
	LeaseID string
	Until   int64
}

func (c RoutingProbeClaim) matches(h *RoutingHealth) bool {
	return c.Key.id(c.Stage) == h.ID && h.State == RoutingHealthHalfOpen && c.Epoch == h.BreakerEpoch && c.LeaseID != "" && c.LeaseID == h.ProbeLeaseID
}

type RoutingHealthInput struct {
	EventID    string
	Key        RoutingMetricKey
	Stage      RoutingMetricStage
	OccurredAt time.Time
	// Outcome is success, failure or ignored; only genuine upstream failures qualify.
	Outcome            string
	Sent               bool
	RateLimited        bool
	SuccessActive      int64
	Probe              *RoutingProbeClaim
	SubmissionID       string
	ObserverOwner      string
	ObserverGeneration int64
}
type RoutingHealthResult struct {
	Health    RoutingHealth
	Applied   bool
	Duplicate bool
	Late      bool
}

func (s RoutingMetricsStore) ApplyHealthEvent(e RoutingHealthInput, now time.Time) (RoutingHealthResult, error) {
	var out RoutingHealthResult
	if e.EventID == "" || len(e.EventID) > 128 || (e.Outcome != "success" && e.Outcome != "failure" && e.Outcome != "ignored") || e.SuccessActive < 0 || (e.RateLimited && (!e.Sent || e.Outcome != "failure")) {
		return out, errors.New("routing health: invalid event")
	}
	if e.Probe != nil && (e.Probe.Key != e.Key || e.Probe.Stage != e.Stage) {
		return out, ErrRoutingMetricsConflict
	}
	h, err := s.mutateHealth(e.Key, e.Stage, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
		// An obsolete observer cannot consume the event ID before its successor.
		if e.Probe != nil && e.Stage == RoutingStageTaskTerminal && e.Probe.matches(h) && h.ProbeUntil > n {
			if e.SubmissionID != h.ProbeSubmissionID || e.ObserverOwner == "" || e.ObserverOwner != h.ObserverOwner || e.ObserverGeneration != h.ObserverGeneration || h.ObserverUntil <= n {
				return ErrRoutingMetricsConflict
			}
		}
		occurred := e.OccurredAt.Unix()
		if e.OccurredAt.IsZero() {
			occurred = n
		}
		record := RoutingHealthEvent{EventID: e.EventID, VariantID: h.ID, OccurredAt: occurred, AppliedAt: n}
		r := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			var existing RoutingHealthEvent
			if err := tx.First(&existing, "event_id = ?", e.EventID).Error; err != nil {
				return err
			}
			if existing.VariantID != h.ID {
				return ErrRoutingMetricsConflict
			}
			out.Duplicate = true
			return nil
		}
		if e.Stage == RoutingStageTaskTerminal && e.SubmissionID != "" && e.Outcome != "ignored" {
			r = tx.Model(&RoutingTerminalObservation{}).Where("submission_id = ? AND variant_id = ? AND status IN ?", e.SubmissionID, h.ID, []string{"accepted", "unknown"}).Updates(map[string]interface{}{"status": "terminal", "terminal_at": n, "updated_at": n})
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected == 0 {
				return ErrRoutingMetricsConflict
			}
		}
		if n-occurred > 300 {
			out.Late = true
			return nil
		}
		h.expireProbe(n)
		// Obsolete epochs are recorded for deduplication and long-term metrics only.
		if e.Probe != nil && !e.Probe.matches(h) {
			return nil
		}
		w, err := h.window()
		if err != nil {
			return err
		}
		if e.Stage == RoutingStageAttempt && e.Outcome == "success" {
			// Serialize the sample with health application, before shared finish
			// releases this attempt's lease. Never trust caller-supplied capacity.
			e.SuccessActive, err = (RoutingMetricsStore{DB: tx}).countActive(e.Key, e.Stage, nil, n)
			if err != nil {
				return fmt.Errorf("routing health success active event_id=%s: %w", e.EventID, err)
			}
		}
		h.EventSeq++
		h.LastEventAt = n
		if err = tx.Model(&RoutingHealthEvent{}).Where("event_id = ?", e.EventID).Update("event_seq", h.EventSeq).Error; err != nil {
			return err
		}
		i := n % 300
		slot := &w.Slots[i]
		if slot.Second != n {
			*slot = routingSecondSlot{Second: n}
		}
		if e.Sent {
			slot.Sent++
		}
		if e.RateLimited {
			slot.RateLimited++
		}
		for len(w.Tail) > 0 && n-w.Tail[0] > 60 {
			w.Tail = w.Tail[1:]
		}
		if e.Outcome != "ignored" {
			slot.Eligible++
		}
		switch e.Outcome {
		case "success":
			w.Tail = nil
			if e.SuccessActive > slot.SuccessActivePeak {
				slot.SuccessActivePeak = e.SuccessActive
			}
		case "failure":
			slot.Failures++
			w.Tail = append(w.Tail, n)
			if len(w.Tail) > 3 {
				w.Tail = w.Tail[len(w.Tail)-3:]
			}
		}
		if e.Probe != nil {
			switch e.Outcome {
			case "success":
				h.State = RoutingHealthClosed
				h.CooldownUntil = 0
				h.ProbeLeaseID = ""
				h.ProbeUntil = 0
				h.ProbeStartedAt = 0
				h.ObserverOwner = ""
				h.ObserverUntil = 0
				h.ProbeSubmissionID = ""
				w.Tail = nil
			case "failure":
				h.open(n, true)
			}
		} else if h.State == RoutingHealthClosed {
			var eligible, failures int64
			for _, v := range w.Slots {
				if n-v.Second >= 0 && n-v.Second < 300 {
					eligible += v.Eligible
					failures += v.Failures
				}
			}
			if len(w.Tail) >= 3 || (eligible >= 20 && failures*2 >= eligible) {
				h.open(n, true)
			}
		}
		out.Applied = true
		return h.saveWindow(w)
	})
	out.Health = h
	return out, err
}

type RoutingHealthView struct {
	Health              RoutingHealth
	ProbeReady          bool
	Eligible5m          int64
	Failures5m          int64
	Sent60s             int64
	RateLimited60s      int64
	SuccessActivePeak5m int64
	Congested           bool
}

func (s RoutingMetricsStore) ReadHealth(k RoutingMetricKey, stage RoutingMetricStage, active int64, now time.Time) (RoutingHealthView, error) {
	var out RoutingHealthView
	if err := k.validate(stage); err != nil {
		return out, err
	}
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return out, err
	}
	h := RoutingHealth{ID: k.id(stage), RoutingMetricKey: k, Stage: stage, State: RoutingHealthClosed}
	err = s.DB.First(&h, "id = ?", h.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		out.Health = h
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if n < h.LastAppliedAt {
		n = h.LastAppliedAt
	}
	// Read-only catalog projection. ClaimProbe persists expiration under lock/CAS.
	h.expireProbe(n)
	out.ProbeReady = h.State == RoutingHealthOpen && n >= h.CooldownUntil
	w, err := h.window()
	if err != nil {
		return out, err
	}
	for _, v := range w.Slots {
		age := n - v.Second
		if age >= 0 && age < 300 {
			out.Eligible5m += v.Eligible
			out.Failures5m += v.Failures
			if v.SuccessActivePeak > out.SuccessActivePeak5m {
				out.SuccessActivePeak5m = v.SuccessActivePeak
			}
		}
		if age >= 0 && age < 60 {
			out.Sent60s += v.Sent
			out.RateLimited60s += v.RateLimited
		}
	}
	out.Congested = (out.Sent60s >= 5 && out.RateLimited60s*5 >= out.Sent60s) || (out.SuccessActivePeak5m >= 2 && active >= out.SuccessActivePeak5m)
	out.Health = h
	return out, nil
}

func (s RoutingMetricsStore) ClaimProbe(k RoutingMetricKey, stage RoutingMetricStage, leaseID string, now time.Time) (RoutingProbeClaim, bool, error) {
	return s.claimProbe(k, stage, leaseID, false, now)
}

// ClaimProbeExplicit is only for a custom model's already-authorized
// selected group's try_soft_offline_groups preference. It skips cooldown, never another owner's live lease.
func (s RoutingMetricsStore) ClaimProbeExplicit(k RoutingMetricKey, stage RoutingMetricStage, leaseID string, now time.Time) (RoutingProbeClaim, bool, error) {
	return s.claimProbe(k, stage, leaseID, true, now)
}

func (s RoutingMetricsStore) claimProbe(k RoutingMetricKey, stage RoutingMetricStage, leaseID string, explicit bool, now time.Time) (RoutingProbeClaim, bool, error) {
	var claim RoutingProbeClaim
	if leaseID == "" || len(leaseID) > 128 {
		return claim, false, errors.New("routing health: invalid probe lease ID")
	}
	claimed := false
	_, err := s.mutateHealth(k, stage, now, func(_ *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if h.State != RoutingHealthOpen || (!explicit && h.CooldownUntil > n) {
			return nil
		}
		h.State = RoutingHealthHalfOpen
		h.BreakerEpoch++
		h.ProbeLeaseID = leaseID
		h.ProbeUntil = n + 30
		h.ProbeStartedAt = n
		h.ObserverOwner = ""
		h.ObserverUntil = 0
		h.ProbeSubmissionID = ""
		claim = RoutingProbeClaim{Key: k, Stage: stage, Epoch: h.BreakerEpoch, LeaseID: leaseID, Until: h.ProbeUntil}
		claimed = true
		return nil
	})
	return claim, claimed, err
}
func (s RoutingMetricsStore) RenewProbe(c RoutingProbeClaim, now time.Time) (bool, error) {
	if c.Stage != RoutingStageAttempt {
		return false, errors.New("routing health: terminal probe renewal requires accepted observer")
	}
	renewed := false
	_, err := s.mutateHealth(c.Key, c.Stage, now, func(_ *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if c.matches(h) {
			h.ProbeUntil = n + 30
			renewed = true
		}
		return nil
	})
	return renewed, err
}

// RenewRoutingTerminalSendingProbe uses the primary DB clock. Sending owns the
// terminal probe until acceptance; afterward only the accepted observer may renew.
func RenewRoutingTerminalSendingProbe(ctx context.Context, id, owner string, c RoutingProbeClaim) (bool, error) {
	if ctx == nil {
		return false, errors.New("routing health: context required")
	}
	if DB == nil {
		return false, ErrRoutingMetricsUnavailable
	}
	return (RoutingMetricsStore{DB: DB.WithContext(ctx)}).RenewTerminalSendingProbe(id, owner, c, time.Time{})
}

func (s RoutingMetricsStore) RenewTerminalSendingProbe(id, owner string, c RoutingProbeClaim, now time.Time) (bool, error) {
	if s.DB == nil {
		return false, ErrRoutingMetricsUnavailable
	}
	if id == "" || len(id) > 128 || owner == "" || len(owner) > 64 || c.Stage != RoutingStageTaskTerminal || c.LeaseID == "" || len(c.LeaseID) > 128 || c.Epoch <= 0 {
		return false, ErrRoutingMetricsConflict
	}
	renewed := false
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// Funding preparation/outcome takes submission before health; keep that
		// order here so a sender cannot deadlock the accepted handoff transaction.
		var submission RoutingSubmission
		if err := lockForUpdate(tx).First(&submission, "submission_id = ?", id).Error; err != nil {
			return err
		}
		n, err := routingDBNow(tx, now)
		if err != nil {
			return err
		}
		key := RoutingMetricKey{ClientModel: submission.ClientModel, BusinessGroup: submission.BusinessGroup, ProtocolClass: submission.ProtocolClass, ProviderKey: submission.ProviderKey, OperationKey: submission.OperationKey}
		if (submission.State != "prepared" && submission.State != "sending") || submission.SendOwnerID != owner || submission.SendLeaseUntil <= n || key != c.Key || submission.TerminalProbeEpoch != c.Epoch || submission.TerminalProbeLeaseID != c.LeaseID {
			return nil
		}
		_, err = (RoutingMetricsStore{DB: tx}).mutateHealth(c.Key, c.Stage, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
			if !c.matches(h) || h.ProbeSubmissionID != id || submission.SendLeaseUntil <= n {
				return nil
			}
			var count int64
			if err := tx.Model(&RoutingTerminalObservation{}).Where("submission_id = ? AND variant_id = ? AND channel_id = ? AND status = ? AND breaker_epoch = ? AND probe_lease_id = ?", id, h.ID, submission.ChannelID, "prepared", c.Epoch, c.LeaseID).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return nil
			}
			// Fence before expiration too: an obsolete sender cannot expire or
			// escalate a successor epoch, even if that successor's lease is stale.
			h.expireProbe(n)
			if !c.matches(h) {
				return nil
			}
			h.ProbeUntil = n + 30
			if h.ProbeUntil > h.ProbeStartedAt+86400 {
				h.ProbeUntil = h.ProbeStartedAt + 86400
			}
			renewed = true
			return nil
		})
		return err
	})
	return renewed, err
}

// ReleaseProbe covers not_sent/rejected/unknown: no terminal sample, preserve backoff.
func (s RoutingMetricsStore) ReleaseProbe(c RoutingProbeClaim, now time.Time) (bool, error) {
	released := false
	_, err := s.mutateHealth(c.Key, c.Stage, now, func(_ *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if c.matches(h) {
			h.open(n, false)
			released = true
		}
		return nil
	})
	return released, err
}

// BindTerminalProbe must run inside the submission's prepared transaction. A later
// accepted transaction changes this same durable observation via SaveTerminalSubmission.
func (s RoutingMetricsStore) BindTerminalProbe(c RoutingProbeClaim, submissionID string, channelID int, now time.Time) error {
	if c.Stage != RoutingStageTaskTerminal || submissionID == "" || len(submissionID) > 128 || channelID <= 0 {
		return errors.New("routing health: invalid terminal probe binding")
	}
	_, err := s.mutateHealth(c.Key, c.Stage, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if !c.matches(h) || (h.ProbeSubmissionID != "" && h.ProbeSubmissionID != submissionID) {
			return ErrRoutingMetricsConflict
		}
		row := RoutingTerminalObservation{SubmissionID: submissionID, RoutingMetricKey: c.Key, VariantID: h.ID, ChannelID: channelID, Status: "prepared", BreakerEpoch: c.Epoch, ProbeLeaseID: c.LeaseID, UpdatedAt: n}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var existing RoutingTerminalObservation
		if err := lockForUpdate(tx).First(&existing, "submission_id = ?", submissionID).Error; err != nil {
			return err
		}
		if existing.VariantID != row.VariantID || existing.ChannelID != channelID || existing.BreakerEpoch != c.Epoch || existing.ProbeLeaseID != c.LeaseID {
			return ErrRoutingMetricsConflict
		}
		h.ProbeSubmissionID = submissionID
		return nil
	})
	return err
}

// SaveTerminalSubmission records accepted/unknown active even without a probe. Call it
// in the transaction durably saving that submission fact; TaskID may initially be empty.
func (s RoutingMetricsStore) SaveTerminalSubmission(k RoutingMetricKey, submissionID string, channelID int, status, taskID string, now time.Time) error {
	if err := k.validate(RoutingStageTaskTerminal); err != nil {
		return err
	}
	if submissionID == "" || len(submissionID) > 128 || len(taskID) > 128 || channelID <= 0 || (status != "accepted" && status != "unknown" && status != "rejected" && status != "not_sent") {
		return errors.New("routing health: invalid terminal submission fact")
	}
	_, err := s.mutateHealth(k, RoutingStageTaskTerminal, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
		row := RoutingTerminalObservation{SubmissionID: submissionID, RoutingMetricKey: k, VariantID: k.id(RoutingStageTaskTerminal), ChannelID: channelID, Status: status, TaskID: taskID, UpdatedAt: n}
		if status == "accepted" {
			row.AcceptedAt = n
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var old RoutingTerminalObservation
		if err := lockForUpdate(tx).First(&old, "submission_id = ?", submissionID).Error; err != nil {
			return err
		}
		if old.VariantID != row.VariantID || old.ChannelID != channelID || (old.TaskID != "" && taskID != "" && old.TaskID != taskID) {
			return ErrRoutingMetricsConflict
		}
		if old.Status == "terminal" {
			return nil
		}
		if old.Status == "accepted" && status != "accepted" {
			return ErrRoutingMetricsConflict
		}
		changes := map[string]interface{}{"status": status, "updated_at": n}
		if taskID != "" {
			changes["task_id"] = taskID
		}
		if status == "accepted" && old.AcceptedAt == 0 {
			changes["accepted_at"] = n
			// Preserve a full lease window for the sender-to-observer handoff.
			// Expired/obsolete epochs remain expired and cannot be resurrected.
			if h.State == RoutingHealthHalfOpen && h.ProbeSubmissionID == submissionID && h.BreakerEpoch == old.BreakerEpoch && h.ProbeLeaseID == old.ProbeLeaseID && h.ProbeUntil > n && h.ProbeStartedAt+86400 > n {
				h.ProbeUntil = n + 30
				if h.ProbeUntil > h.ProbeStartedAt+86400 {
					h.ProbeUntil = h.ProbeStartedAt + 86400
				}
			}
		}
		if err := tx.Model(&RoutingTerminalObservation{}).Where("submission_id = ?", submissionID).Updates(changes).Error; err != nil {
			return err
		}
		if status != "accepted" && h.State == RoutingHealthHalfOpen && h.ProbeSubmissionID == submissionID && h.BreakerEpoch == old.BreakerEpoch && h.ProbeLeaseID == old.ProbeLeaseID {
			h.open(n, false)
		}
		return nil
	})
	return err
}

type RoutingProbeObserver struct {
	Claim        RoutingProbeClaim
	SubmissionID string
	Owner        string
	Generation   int64
	Until        int64
}

func (s RoutingMetricsStore) ClaimTerminalObserver(c RoutingProbeClaim, submissionID, owner string, now time.Time) (RoutingProbeObserver, bool, error) {
	var observer RoutingProbeObserver
	if c.Stage != RoutingStageTaskTerminal || owner == "" || len(owner) > 128 {
		return observer, false, errors.New("routing health: invalid observer")
	}
	claimed := false
	_, err := s.mutateHealth(c.Key, c.Stage, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if !c.matches(h) || h.ProbeSubmissionID != submissionID || (h.ObserverOwner != "" && h.ObserverUntil > n) {
			return nil
		}
		var row RoutingTerminalObservation
		if err := tx.First(&row, "submission_id = ? AND variant_id = ?", submissionID, h.ID).Error; err != nil {
			return err
		}
		if row.Status != "accepted" || row.BreakerEpoch != c.Epoch || row.ProbeLeaseID != c.LeaseID {
			return nil
		}
		h.ObserverGeneration++
		h.ObserverOwner = owner
		h.ObserverUntil = n + 15
		observer = RoutingProbeObserver{Claim: c, SubmissionID: submissionID, Owner: owner, Generation: h.ObserverGeneration, Until: h.ObserverUntil}
		claimed = true
		return nil
	})
	return observer, claimed, err
}
func (o RoutingProbeObserver) matches(h *RoutingHealth, n int64) bool {
	return o.Claim.matches(h) && o.SubmissionID == h.ProbeSubmissionID && o.Owner != "" && o.Owner == h.ObserverOwner && o.Generation == h.ObserverGeneration && h.ObserverUntil > n
}

// Heartbeat every 5s. Probe extension every 10s is optional on each heartbeat;
// either operation checks the durable accepted and nonterminal submission fact.
func (s RoutingMetricsStore) RenewTerminalObserver(o RoutingProbeObserver, renewProbe bool, now time.Time) (bool, error) {
	renewed := false
	_, err := s.mutateHealth(o.Claim.Key, o.Claim.Stage, now, func(tx *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if !o.matches(h, n) {
			return nil
		}
		var count int64
		if err := tx.Model(&RoutingTerminalObservation{}).Where("submission_id = ? AND variant_id = ? AND status = ? AND breaker_epoch = ? AND probe_lease_id = ?", o.SubmissionID, h.ID, "accepted", o.Claim.Epoch, o.Claim.LeaseID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return nil
		}
		h.ObserverUntil = n + 15
		if renewProbe {
			h.ProbeUntil = n + 30
			if h.ProbeUntil > h.ProbeStartedAt+86400 {
				h.ProbeUntil = h.ProbeStartedAt + 86400
			}
		}
		renewed = true
		return nil
	})
	return renewed, err
}
func (s RoutingMetricsStore) ReleaseTerminalObserver(o RoutingProbeObserver, now time.Time) (bool, error) {
	released := false
	_, err := s.mutateHealth(o.Claim.Key, o.Claim.Stage, now, func(_ *gorm.DB, h *RoutingHealth, n int64) error {
		h.expireProbe(n)
		if o.matches(h, n) {
			h.ObserverOwner = ""
			h.ObserverUntil = 0
			released = true
		}
		return nil
	})
	return released, err
}

// Cleanup never deletes unresolved open/half-open health or accepted/unknown tasks.
func (s RoutingMetricsStore) Cleanup(now time.Time) error {
	n, err := routingDBNow(s.DB, now)
	if err != nil {
		return err
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("minute < ?", n-48*3600).Delete(&RoutingMetricBucket{}).Error; err != nil {
			return err
		}
		if err := tx.Where("applied_at < ?", n-48*3600).Delete(&RoutingHealthEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("updated_at < ? AND status IN ?", n-48*3600, []string{"terminal", "rejected", "not_sent"}).Delete(&RoutingTerminalObservation{}).Error; err != nil {
			return err
		}
		return tx.Where("expires_at <= ?", n).Delete(&RoutingMetricLease{}).Error
	})
}
