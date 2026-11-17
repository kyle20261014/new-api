package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in funding-layer load only. Never emits a reviewed migration receipt or
// claims full gateway capacity: HTTP, pricing, upstream and observers are absent.
// Example: TEST_ROUTING_CAPACITY=1 GOWORK=off go test ./model -run '^TestRoutingFundingCapacity$' -count=1 -v -timeout 2m
// ROUTING_CAPACITY_*: DIALECT, TARGET, HARDWARE, DURATION, CONCURRENCY, POOL,
// WALLETS (1=hot wallet), MODEL (label only), UPSTREAM_DELAY, EXPECTED_PEAK_RPS,
// TX_TIMEOUT, REPORT. External DBs additionally need TEST_ROUTING_DB_FIXTURE=1
// and TEST_MYSQL_DSN / TEST_POSTGRES_DSN pointing to an EMPTY disposable database.
func TestRoutingFundingCapacity(t *testing.T) {
	if os.Getenv("TEST_ROUTING_CAPACITY") != "1" {
		t.Skip("opt-in disposable-database funding load; not a deployment capacity approval")
	}
	cfg, err := routingCapacityConfigFromEnv()
	require.NoError(t, err)
	before, err := routingCapacityCandidate()
	require.NoError(t, err)
	dsn := filepath.Join(t.TempDir(), "funding-capacity.db")
	var driver gorm.Dialector = sqlite.Open(dsn)
	if cfg.Dialect != "sqlite" {
		require.Equal(t, "1", os.Getenv("TEST_ROUTING_DB_FIXTURE"), "explicit disposable fixture acknowledgement required")
		if cfg.Dialect == "mysql" {
			dsn = os.Getenv("TEST_MYSQL_DSN")
			driver = mysql.Open(dsn)
		} else {
			dsn = os.Getenv("TEST_POSTGRES_DSN")
			driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}
		require.NotEmpty(t, dsn, "test DSN required (never printed)")
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.True(t, err == nil, "could not open disposable database; DSN/error omitted to protect credentials")
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	pool.SetMaxOpenConns(cfg.Pool)
	pool.SetMaxIdleConns(cfg.Pool)
	// Check the ENTIRE namespace before creating anything, never truncate or drop.
	require.NoError(t, routingCapacityEmptyDatabase(db))
	oldDB, oldType := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseType(cfg.Dialect))
	initCol()
	t.Cleanup(func() { DB = oldDB; common.SetMainDatabaseType(oldType); initCol() })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
	require.NoError(t, MigrateRoutingFunding(db))
	const initialQuota = 1_000_000_000
	users, tokens := make([]User, cfg.Wallets), make([]Token, cfg.Wallets)
	for i := range users {
		users[i] = createReserveTestUser(t, initialQuota)
		tokens[i] = Token{UserId: users[i].Id, Key: "capacity-" + common.GetRandomString(24), Name: "capacity-only", Status: common.TokenStatusEnabled, RemainQuota: initialQuota, ExpiredTime: -1}
		require.NoError(t, db.Create(&tokens[i]).Error)
	}
	versionSQL := "SELECT version()"
	if cfg.Dialect == "sqlite" {
		versionSQL = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)

	stats := routingCapacityStats{Stages: map[string]*routingCapacityStage{}, ErrorKinds: map[string]int64{}, Windows: map[int64]*routingCapacityWindow{}}
	poolBefore := pool.Stats()
	started := time.Now()
	stop := started.Add(cfg.Duration)
	var sequence atomic.Int64
	var completed atomic.Int64
	credits := make([]atomic.Int64, cfg.Wallets)
	expectedDebit := make([]atomic.Int64, cfg.Wallets)
	samplingDone, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		last := poolBefore
		sample := func(final bool) {
			now := pool.Stats()
			second := int64(time.Since(started) / time.Second)
			if !final {
				second = max(second-1, 0)
			}
			stats.Lock()
			w := stats.window(second)
			w.Sampled = true
			w.InFlight = sequence.Load() - completed.Load()
			w.PoolWaitCount += now.WaitCount - last.WaitCount
			w.PoolWaitMS += float64(now.WaitDuration-last.WaitDuration) / float64(time.Millisecond)
			w.PoolInUse = now.InUse
			stats.Unlock()
			last = now
		}
		for {
			select {
			case <-ticker.C:
				sample(false)
			case <-samplingDone:
				sample(true)
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for range cfg.Concurrency {
		wg.Go(func() {
			for time.Now().Before(stop) {
				id := sequence.Add(1)
				account := int((id - 1) % int64(cfg.Wallets))
				begin := time.Now()
				credit, err := routingCapacityCycle(cfg, id, users[account].Id, tokens[account].Id, func(stage string, f func(context.Context) error) error {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.TxTimeout)
					defer cancel()
					at := time.Now()
					err := f(ctx)
					stats.record(stage, time.Since(at), err, -1)
					return err
				})
				credits[account].Add(int64(credit))
				if err == nil && id%5 != 0 {
					expectedDebit[account].Add(20)
				}
				stats.record("cycle", time.Since(begin), err, int64(time.Since(started)/time.Second))
				completed.Add(1)
			}
		})
	}
	wg.Wait() // in-flight cycles finish; no cancellation between debit and settlement
	close(samplingDone)
	<-sampled
	elapsed := time.Since(started)
	poolAfter := pool.Stats()
	var pending int64
	require.NoError(t, db.Model(&RoutingFundingSession{}).Where("status = ?", "reserved").Count(&pending).Error)
	var differences []string
	var balances []map[string]any
	for i, u := range users {
		var sum struct{ Delta int64 }
		require.NoError(t, db.Model(&RoutingFundingOperation{}).Select("COALESCE(SUM(delta), 0) AS delta").Where("user_id = ?", u.Id).Scan(&sum).Error)
		var actual User
		var key Token
		require.NoError(t, db.First(&actual, u.Id).Error)
		require.NoError(t, db.First(&key, tokens[i].Id).Error)
		balances = append(balances, map[string]any{"wallet_index": i, "initial_quota": initialQuota, "ledger_delta": sum.Delta, "successful_cycle_expected_delta": expectedDebit[i].Load(), "fixture_wallet_credits": credits[i].Load(), "wallet_quota": actual.Quota, "key_remaining": key.RemainQuota, "key_used": key.UsedQuota})
		if sum.Delta != expectedDebit[i].Load() || int64(actual.Quota) != initialQuota-sum.Delta+credits[i].Load() || int64(key.RemainQuota) != initialQuota-sum.Delta || int64(key.UsedQuota) != sum.Delta {
			differences = append(differences, fmt.Sprintf("wallet_index=%d ledger_delta=%d wallet=%d key_remaining=%d key_used=%d", i, sum.Delta, actual.Quota, key.RemainQuota, key.UsedQuota))
		}
	}
	after, err := routingCapacityCandidate()
	require.NoError(t, err)
	cycles := stats.Stages["cycle"]
	successRPS := float64(cycles.Count-cycles.Errors) / elapsed.Seconds()
	localStable := elapsed >= cfg.Duration && cycles.Errors == 0 && pending == 0 && len(differences) == 0 && before == after
	diagnostic70 := successRPS * .70
	stages := map[string]any{}
	for name, stage := range stats.Stages {
		stages[name] = stage.report()
	}
	windows := make([]routingCapacityWindow, 0, len(stats.Windows))
	for second, w := range stats.Windows {
		w.Second = second
		windows = append(windows, *w)
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Second < windows[j].Second })
	report := map[string]any{
		"schema": "routing.funding-capacity-diagnostic.v1", "scope": "funding_layer_only", "reviewed": false, "capacity_passed": false,
		"a21_status": "blocked_no_deployment_gateway_capacity_evidence", "target": cfg.Target, "hardware_ref_operator_supplied": cfg.Hardware,
		"runtime":          map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0)},
		"database":         map[string]any{"dialect": cfg.Dialect, "version": version, "pool_max_open": cfg.Pool, "actual_process_count": 1, "actual_node_count": 1, "fixture": "empty_disposable_retained_external_or_temporary_sqlite"},
		"candidate_before": before, "candidate_after": after, "candidate_unchanged": before == after,
		"workload":       map[string]any{"model_label_only": cfg.Model, "concurrency": cfg.Concurrency, "wallets": cfg.Wallets, "mode": "closed_loop_no_unbounded_queue", "simulated_upstream_delay_ms": cfg.UpstreamDelay.Milliseconds(), "tx_timeout_ms": cfg.TxTimeout.Milliseconds(), "definition": "odd=shared legacy delta reserve40 then settle20/refund0; even=smart funding reserve30 supplement40 then settle20/refund0; every fifth cycle refunds; integer quota units, wallet+finite key; simulated upstream wait outside transactions; successful paid cycles credit wallet20 using locked shared increaseUserQuotaTx (fixture credit, not payment API)", "pricing": "synthetic fixed quota, no model invocation or pricing engine"},
		"started_at_utc": started.UTC().Format(time.RFC3339Nano), "requested_duration_seconds": cfg.Duration.Seconds(), "actual_duration_seconds": elapsed.Seconds(),
		"success": cycles.Count - cycles.Errors, "errors": cycles.Errors, "error_rate": float64(cycles.Errors) / float64(cycles.Count), "error_kinds": stats.ErrorKinds,
		"successful_cycles_per_second": successRPS, "stage_latency_ms": stages, "one_second_completion_windows": windows,
		"pool_wait":           map[string]any{"count": poolAfter.WaitCount - poolBefore.WaitCount, "duration_ms": float64(poolAfter.WaitDuration-poolBefore.WaitDuration) / float64(time.Millisecond), "final_in_use": poolAfter.InUse},
		"balance_differences": differences, "balance_checks": balances, "unresolved_smart_reservations": pending, "local_invariants_stable": localStable,
		"thirty_minutes_observed": elapsed >= 30*time.Minute, "maximum_stable_throughput_found": false,
		"seventy_percent_diagnostic_only": map[string]any{"observed_rps_times_0_70": diagnostic70, "expected_peak_rps": cfg.ExpectedPeak, "peak_within_diagnostic": cfg.ExpectedPeak <= diagnostic70, "deployment_capacity_passed": false},
		"not_measured":                    []string{"gateway HTTP/auth/routing/pricing/limits", "actual model or upstream work", "long stream/async callbacks/subscription/payment topup", "observation writes and mixed gateway baseline", "deployment multi-node topology/hardware verification", "DB server CPU/IO and lock waits", "open-loop arrival backlog (bounded closed-loop in-flight only)", "staircase maximum stable 30min throughput and protocol latency SLO"},
		"retries":                         0, "retry_policy": "none; failures remain failures, no hidden retries", "latency_quantization": "ceil milliseconds, 60001ms overflow bucket",
	}
	data, err := common.Marshal(report)
	require.NoError(t, err)
	out, err := os.OpenFile(cfg.Report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err, "report must be a new path; never overwrite evidence")
	_, writeErr := out.Write(append(data, '\n'))
	closeErr := out.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
	t.Logf("funding-only report=%s cycles=%d errors=%d success_rps=%.3f local_invariants=%t reviewed=false A21=blocked", cfg.Report, cycles.Count, cycles.Errors, successRPS, localStable)
	require.Zero(t, cycles.Errors, "see redacted error categories in report")
	require.Zero(t, pending)
	require.Empty(t, differences)
}

type routingCapacityConfig struct {
	Dialect, Target, Hardware, Model, Report string
	Duration, UpstreamDelay, TxTimeout       time.Duration
	Concurrency, Wallets, Pool               int
	ExpectedPeak                             float64
}

func routingCapacityEmptyDatabase(db *gorm.DB) error {
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return err
	}
	if len(tables) != 0 {
		return fmt.Errorf("capacity fixture must be empty; no tables were modified")
	}
	return nil
}

func routingCapacityConfigFromEnv() (c routingCapacityConfig, err error) {
	get := func(k, fallback string) string {
		if v := strings.TrimSpace(os.Getenv("ROUTING_CAPACITY_" + k)); v != "" {
			return v
		}
		return fallback
	}
	c.Dialect, c.Target, c.Hardware, c.Model, c.Report = get("DIALECT", "sqlite"), get("TARGET", "local-disposable-check"), get("HARDWARE", "unverified-local-host"), get("MODEL", "synthetic-funding-model"), get("REPORT", "")
	if c.Dialect != "sqlite" && c.Dialect != "mysql" && c.Dialect != "postgres" {
		return c, fmt.Errorf("ROUTING_CAPACITY_DIALECT must be sqlite/mysql/postgres")
	}
	for _, p := range []struct {
		key, fallback string
		target        *time.Duration
		min, max      time.Duration
	}{{"DURATION", "3s", &c.Duration, time.Second, 2 * time.Hour}, {"UPSTREAM_DELAY", "10ms", &c.UpstreamDelay, 0, time.Minute}, {"TX_TIMEOUT", "5s", &c.TxTimeout, time.Millisecond, time.Minute}} {
		*p.target, err = time.ParseDuration(get(p.key, p.fallback))
		if err != nil || *p.target < p.min || *p.target > p.max {
			return c, fmt.Errorf("invalid ROUTING_CAPACITY_%s duration", p.key)
		}
	}
	for _, p := range []struct {
		key, fallback string
		target        *int
		max           int
	}{{"CONCURRENCY", "1", &c.Concurrency, 1024}, {"WALLETS", "1", &c.Wallets, 1024}, {"POOL", "1", &c.Pool, 512}} {
		*p.target, err = strconv.Atoi(get(p.key, p.fallback))
		if err != nil || *p.target < 1 || *p.target > p.max {
			return c, fmt.Errorf("invalid ROUTING_CAPACITY_%s integer", p.key)
		}
	}
	c.ExpectedPeak, err = strconv.ParseFloat(get("EXPECTED_PEAK_RPS", "1"), 64)
	if err != nil || math.IsNaN(c.ExpectedPeak) || math.IsInf(c.ExpectedPeak, 0) || c.ExpectedPeak <= 0 {
		return c, fmt.Errorf("invalid ROUTING_CAPACITY_EXPECTED_PEAK_RPS")
	}
	if c.Dialect == "sqlite" && c.Pool != 1 {
		return c, fmt.Errorf("SQLite diagnostic requires a single writer pool")
	}
	if c.Report == "" || len(c.Model) > 191 {
		return c, fmt.Errorf("new ROUTING_CAPACITY_REPORT path required; MODEL <=191 bytes")
	}
	if _, err := os.Lstat(c.Report); !os.IsNotExist(err) {
		return c, fmt.Errorf("ROUTING_CAPACITY_REPORT must not exist and must be accessible")
	}
	return c, nil
}

func routingCapacityCycle(c routingCapacityConfig, id int64, userID, tokenID int, measure func(string, func(context.Context) error) error) (int, error) {
	prefix := fmt.Sprintf("capacity:%d", id)
	refund := id%5 == 0
	actual := 20
	if refund {
		actual = 0
	}
	if id%2 == 1 {
		apply := func(stage string, delta int) error {
			return measure(stage, func(ctx context.Context) error {
				_, err := ApplyRoutingFundingDeltaContext(ctx, RoutingFundingDeltaRequest{OperationID: prefix + ":" + stage, UserID: userID, TokenID: tokenID, Source: "wallet", Delta: delta, RequireAvailable: true})
				return err
			})
		}
		if err := apply("legacy_reserve", 40); err != nil {
			return 0, err
		}
		time.Sleep(c.UpstreamDelay) // no DB transaction/lock is held here
		stage := "legacy_settle"
		if refund {
			stage = "legacy_refund"
		}
		if err := apply(stage, actual-40); err != nil {
			return 0, err
		}
	} else {
		apply := func(stage, kind string, quota int) error {
			return measure(stage, func(ctx context.Context) error {
				_, err := PrepareRoutingFundingContext(ctx, RoutingFundingRequest{FundingID: prefix, OperationID: prefix + ":" + stage, UserID: userID, TokenID: tokenID, Source: "wallet", ModelName: c.Model, Kind: kind, Quota: quota}, nil)
				return err
			})
		}
		if err := apply("smart_reserve", "reserve", 30); err != nil {
			return 0, err
		}
		if err := apply("smart_supplement", "reserve", 40); err != nil {
			return 0, err
		}
		time.Sleep(c.UpstreamDelay)
		if refund {
			if err := apply("smart_refund", "refund", 0); err != nil {
				return 0, err
			}
		} else if err := apply("smart_settle", "settle", actual); err != nil {
			return 0, err
		}
	}
	if actual == 0 {
		return 0, nil
	}
	err := measure("wallet_topup", func(ctx context.Context) error {
		return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
				return err
			}
			return increaseUserQuotaTx(tx, userID, actual)
		})
	})
	if err != nil {
		return 0, err
	}
	return actual, nil
}

type routingCapacityStage struct {
	Count, Errors int64
	Histogram     [60002]int64
	MaxMS         float64
}

func (s *routingCapacityStage) report() map[string]any {
	q := func(f float64) int {
		n := int64(math.Ceil(float64(s.Count) * f))
		var total int64
		for i, count := range s.Histogram {
			total += count
			if total >= n {
				return i
			}
		}
		return 0
	}
	return map[string]any{"count": s.Count, "errors": s.Errors, "p50": q(.5), "p95": q(.95), "p99": q(.99), "max": s.MaxMS}
}

type routingCapacityWindow struct {
	Second        int64   `json:"second"`
	Success       int64   `json:"success"`
	Errors        int64   `json:"errors"`
	Sampled       bool    `json:"pool_sampled"`
	InFlight      int64   `json:"in_flight_at_sample"`
	PoolWaitCount int64   `json:"pool_wait_count"`
	PoolWaitMS    float64 `json:"pool_wait_ms"`
	PoolInUse     int     `json:"pool_in_use"`
}
type routingCapacityStats struct {
	sync.Mutex
	Stages     map[string]*routingCapacityStage
	ErrorKinds map[string]int64
	Windows    map[int64]*routingCapacityWindow
}

func (s *routingCapacityStats) record(stage string, d time.Duration, err error, second int64) {
	s.Lock()
	defer s.Unlock()
	v := s.Stages[stage]
	if v == nil {
		v = &routingCapacityStage{}
		s.Stages[stage] = v
	}
	v.Count++
	ms := float64(d) / float64(time.Millisecond)
	v.MaxMS = max(v.MaxMS, ms)
	v.Histogram[min(int(math.Ceil(ms)), 60001)]++
	if err != nil {
		v.Errors++
	}
	if second >= 0 {
		w := s.window(second)
		if err == nil {
			w.Success++
		} else {
			w.Errors++
			kind := "other_redacted"
			text := strings.ToLower(err.Error())
			for _, k := range []string{"deadline exceeded", "deadlock", "database is locked", "sqlite_busy", "lock wait timeout", "quota insufficient"} {
				if strings.Contains(text, k) {
					kind = k
					break
				}
			}
			s.ErrorKinds[kind]++
		}
	}
}

// Caller holds the statistics mutex.
func (s *routingCapacityStats) window(second int64) *routingCapacityWindow {
	w := s.Windows[second]
	if w == nil {
		w = &routingCapacityWindow{}
		s.Windows[second] = w
	}
	return w
}

type routingCapacityIdentity struct {
	Commit       string `json:"commit"`
	TreeDigest   string `json:"tree_digest_sha256"`
	BinaryDigest string `json:"test_binary_sha256"`
	Dirty        bool   `json:"dirty"`
}

func routingCapacityCandidate() (routingCapacityIdentity, error) {
	var identity routingCapacityIdentity
	git := func(args ...string) ([]byte, error) { return exec.Command("git", args...).Output() }
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return identity, fmt.Errorf("candidate git HEAD: %w", err)
	}
	identity.Commit = strings.TrimSpace(string(head))
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return identity, err
	}
	status, err := git("status", "--porcelain", "-z")
	if err != nil {
		return identity, err
	}
	identity.Dirty = len(status) != 0
	files, err := git("-C", strings.TrimSpace(string(root)), "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return identity, err
	}
	paths := strings.Split(strings.TrimRight(string(files), "\x00"), "\x00")
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		full := filepath.Join(strings.TrimSpace(string(root)), filepath.FromSlash(path))
		info, err := os.Lstat(full)
		if os.IsNotExist(err) {
			fmt.Fprintf(h, "deleted:%s\x00", path)
			continue
		}
		if err != nil {
			return identity, err
		}
		fmt.Fprintf(h, "%s\x00%s\x00", path, info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return identity, err
			}
			io.WriteString(h, target)
		} else if info.Mode().IsRegular() {
			f, err := os.Open(full)
			if err != nil {
				return identity, err
			}
			_, copyErr := io.Copy(h, f)
			closeErr := f.Close()
			if copyErr != nil {
				return identity, copyErr
			}
			if closeErr != nil {
				return identity, closeErr
			}
		}
		h.Write([]byte{0})
	}
	identity.TreeDigest = hex.EncodeToString(h.Sum(nil))
	bin, err := os.Executable()
	if err != nil {
		return identity, err
	}
	f, err := os.Open(bin)
	if err != nil {
		return identity, err
	}
	h.Reset()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return identity, copyErr
	}
	if closeErr != nil {
		return identity, closeErr
	}
	identity.BinaryDigest = hex.EncodeToString(h.Sum(nil))
	return identity, nil
}

func TestRoutingCapacityDiagnosticBoundaries(t *testing.T) {
	t.Setenv("ROUTING_CAPACITY_REPORT", filepath.Join(t.TempDir(), "report.json"))
	for key, bad := range map[string]string{"DURATION": "0s", "CONCURRENCY": "0", "EXPECTED_PEAK_RPS": "NaN", "DIALECT": "production", "POOL": "0"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("ROUTING_CAPACITY_"+key, bad)
			_, err := routingCapacityConfigFromEnv()
			require.Error(t, err)
		})
	}
	stage := &routingCapacityStage{}
	stats := routingCapacityStats{Stages: map[string]*routingCapacityStage{"test": stage}, ErrorKinds: map[string]int64{}, Windows: map[int64]*routingCapacityWindow{}}
	for _, ms := range []time.Duration{1, 2, 3, 4, 100} {
		stats.record("test", ms*time.Millisecond, nil, 0)
	}
	require.Equal(t, 3, stage.report()["p50"])
	require.Equal(t, 100, stage.report()["p95"])
	require.EqualValues(t, 5, stats.Windows[0].Success)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, routingCapacityEmptyDatabase(db))
	require.NoError(t, db.Exec("CREATE TABLE occupied_fixture (id INTEGER)").Error)
	require.ErrorContains(t, routingCapacityEmptyDatabase(db), "fixture must be empty")
	require.True(t, db.Migrator().HasTable("occupied_fixture"), "refusal must leave unrelated tables untouched")
}
