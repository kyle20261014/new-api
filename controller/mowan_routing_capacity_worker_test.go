package controller

// TEST ONLY synthetic bootstrap. This harness never runs InitResources, writes a
// funding-mode receipt, or bypasses a production startup with an environment flag.
import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	plugin "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type capacitySample struct {
	Count, Errors int64
	Bins          map[int64]int64
	MaxMS         float64
}
type capacityRecorder struct {
	sync.Mutex
	Stages map[string]*capacitySample
}

func (s *capacityRecorder) add(stage string, elapsed time.Duration, err error) {
	s.Lock()
	defer s.Unlock()
	if s.Stages == nil {
		s.Stages = map[string]*capacitySample{}
	}
	v := s.Stages[stage]
	if v == nil {
		v = &capacitySample{Bins: map[int64]int64{}}
		s.Stages[stage] = v
	}
	v.Count++
	if err != nil {
		v.Errors++
	}
	ms := float64(elapsed) / float64(time.Millisecond)
	v.MaxMS = max(v.MaxMS, ms)
	bucket := int64((elapsed + time.Millisecond - 1) / time.Millisecond)
	v.Bins[bucket]++
}
func (s *capacityRecorder) report() map[string]any {
	s.Lock()
	defer s.Unlock()
	out := map[string]any{}
	for name, v := range s.Stages {
		keys := make([]int64, 0, len(v.Bins))
		for k := range v.Bins {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		q := func(percent int64) int64 {
			rank := (v.Count*percent + 99) / 100
			var n int64
			for _, k := range keys {
				n += v.Bins[k]
				if n >= rank {
					return k
				}
			}
			return 0
		}
		out[name] = map[string]any{"count": v.Count, "errors": v.Errors, "p50_ms": q(50), "p95_ms": q(95), "p99_ms": q(99), "max_ms": v.MaxMS}
	}
	return out
}

// Captures actual database/sql transaction boundaries, including explicit model
// Transaction calls; HTTP timings are collected separately. Labels are inferred
// only from INSERT ledger kind, otherwise unclassified (never guessed from HTTP).
type capacityPool struct {
	gorm.ConnPool
	DB    *sql.DB
	Stats *capacityRecorder
}

func (p *capacityPool) GetDBConn() (*sql.DB, error) { return p.DB, nil }
func (p *capacityPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	at := time.Now()
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		p.Stats.add("begin_failed", time.Since(at), err)
		return nil, err
	}
	return &capacityTx{Tx: tx, at: at, stats: p.Stats, kind: "unclassified"}, nil
}

type capacityTx struct {
	*sql.Tx
	at    time.Time
	stats *capacityRecorder
	kind  string
	once  sync.Once
}

func (tx *capacityTx) observe(q string, args []any) {
	if !strings.Contains(q, "mowan_routing_funding_operations") || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(q)), "INSERT") {
		return
	}
	open := strings.Index(q, "(")
	if open < 0 {
		return
	}
	close := strings.Index(q[open:], ")")
	if close < 0 {
		return
	}
	columns := strings.Split(q[open+1:open+close], ",")
	if len(args) != len(columns) {
		return
	}
	fields := map[string]any{}
	for i, column := range columns {
		fields[strings.Trim(strings.TrimSpace(column), "`\"")] = args[i]
	}
	kind, ok := fields["kind"].(string)
	if !ok {
		return
	}
	switch kind {
	case "reserve":
		quota, qok := fields["quota"].(int)
		delta, dok := fields["delta"].(int)
		if !qok || !dok {
			return
		}
		if delta > 0 && quota > delta {
			tx.kind = "funding_supplement"
		} else if delta > 0 && quota == delta {
			tx.kind = "funding_reserve"
		} else if delta == 0 {
			tx.kind = "funding_prepare_no_money"
		}
	case "settle", "refund", "accepted", "delta":
		tx.kind = "funding_" + kind
	}
}
func (tx *capacityTx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	tx.observe(q, args)
	return tx.Tx.ExecContext(ctx, q, args...)
}
func (tx *capacityTx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	tx.observe(q, args)
	return tx.Tx.QueryContext(ctx, q, args...)
}
func (tx *capacityTx) Commit() error {
	err := tx.Tx.Commit()
	tx.once.Do(func() { tx.stats.add(tx.kind, time.Since(tx.at), err) })
	return err
}
func (tx *capacityTx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.once.Do(func() { tx.stats.add(tx.kind+"_rollback", time.Since(tx.at), fmt.Errorf("rolled back")) })
	return err
}

func capacityOpen(t *testing.T, c capacityConfig, stats *capacityRecorder) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("CAPACITY_DSN")
	require.NotEmpty(t, dsn, "CAPACITY_DSN secret environment required")
	var d gorm.Dialector = mysql.Open(dsn)
	if c.Dialect == "postgres" {
		d = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(d, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.True(t, err == nil, "open owned test DB failed; details redacted")
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(c.Pool)
	pool.SetMaxIdleConns(c.Pool)
	t.Cleanup(func() { _ = pool.Close() })
	if stats != nil {
		wrapped := &capacityPool{ConnPool: pool, DB: pool, Stats: stats}
		db.ConnPool = wrapped
		db.Statement.ConnPool = wrapped
	}
	common.SetDatabaseTypes(common.DatabaseType(c.Dialect), common.DatabaseType(c.Dialect))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	common.LogConsumeEnabled = true
	constant.CountToken = false
	constant.StreamingTimeout = c.RequestTimeoutSeconds
	constant.StreamScannerMaxBufferMB = 128
	constant.TaskQueryLimit = 1000
	constant.TaskTimeoutMinutes = 4320
	common.RetryTimes = 2
	common.RelayTimeout = c.RequestTimeoutSeconds
	return db
}
func capacitySettings(t *testing.T, c capacityConfig) {
	t.Helper()
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"auto":"Auto","cap-cheap":"Capacity cheap","cap-dear":"Capacity dear"}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["cap-cheap","cap-dear"]`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"cap-cheap":1,"cap-dear":2}`))
	prices, err := common.Marshal(map[string]float64{c.Model: .0001, c.AsyncModel: .0001})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
	service.InitHttpClient()
	service.GetTaskAdaptorFunc = func(p constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(p) }
}
func capacityMigrate(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{}, &model.Option{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.PerfMetric{}))
	require.NoError(t, model.MigrateTokenRouting(db))
	require.NoError(t, model.MigrateRoutingFunding(db))
	require.NoError(t, model.MigrateRoutingMetrics(db))
}
func capacityInitColumns(t *testing.T, c capacityConfig) {
	// InitDB initializes private dialect quoting. All schema already belongs to the
	// test bootstrap; master=false avoids running the production schema migration.
	t.Helper()
	t.Setenv("SQL_DSN", os.Getenv("CAPACITY_DSN"))
	common.IsMasterNode = false
	require.NoError(t, model.InitDB())
	p, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, p.Close())
}

type capacityKeys struct {
	Legacy, Smart             string
	UserID, LegacyID, SmartID int
}
type capacityState struct {
	Config         capacityConfig
	Keys           []capacityKeys
	Upstream       string
	ExpectedMu     sync.Mutex             `json:"-"`
	ExpectedUsers  map[int]int64          `json:"-"`
	ExpectedTokens map[int]int64          `json:"-"`
	Evidence       *capacityUpstreamState `json:"-"`
}

func capacitySeed(t *testing.T, c capacityConfig, upstream string) *capacityState {
	t.Helper()
	db := capacityOpen(t, c, nil)
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "owned test database must be entirely empty; nothing was modified")
	capacityMigrate(t, db)
	capacityInitColumns(t, c)
	db = capacityOpen(t, c, nil)
	capacitySettings(t, c)
	state := &capacityState{Config: c, Upstream: upstream, ExpectedUsers: map[int]int64{}, ExpectedTokens: map[int]int64{}}
	for i := 0; i < c.Wallets; i++ {
		u := model.User{Username: fmt.Sprintf("cap%d", i), AffCode: fmt.Sprintf("cap%d", i), Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default", AuthVersion: 1, Quota: c.InitialQuota, Setting: `{"billing_preference":"wallet_only"}`}
		require.NoError(t, db.Create(&u).Error)
		keys := capacityKeys{UserID: u.Id}
		for _, smart := range []bool{false, true} {
			key := common.GetRandomString(40)
			tok := model.Token{UserId: u.Id, Key: key, Name: "synthetic-capacity", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: c.InitialQuota, Group: "auto", AutoGroups: `["cap-cheap","cap-dear"]`, CrossGroupRetry: true}
			require.NoError(t, db.Create(&tok).Error)
			if smart {
				policy := smartrouting.Policy{SchemaVersion: 1, Mode: "smart", Strategy: "custom", Models: map[string]smartrouting.ModelPolicy{c.Model: {Strategy: "balanced", Groups: []string{"cap-cheap", "cap-dear"}}, c.AsyncModel: {Strategy: "balanced", Groups: []string{"cap-cheap", "cap-dear"}}}}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := model.SaveTokenRoutingTx(tx, &tok, &policy, 0, nil, false, false, false)
					return err
				}))
				keys.Smart, keys.SmartID = key, tok.Id
			} else {
				keys.Legacy, keys.LegacyID = key, tok.Id
			}
		}
		state.Keys = append(state.Keys, keys)
	}
	for _, g := range []string{"cap-cheap", "cap-dear"} {
		for _, async := range []bool{false, true} {
			kind, name := constant.ChannelTypeOpenAI, c.Model
			if async {
				kind, name = constant.ChannelTypeTaskPlugin, c.AsyncModel
			}
			base := upstream + "/" + g
			ch := model.Channel{Type: kind, Name: g, Key: "synthetic-upstream", BaseURL: &base, Status: common.ChannelStatusEnabled, Models: name, Group: g}
			if async {
				ch.SetSetting(dto.ChannelSettings{TaskPluginKey: "capacity-task"})
			}
			require.NoError(t, db.Create(&ch).Error)
			require.NoError(t, db.Create(&model.Ability{Group: g, Model: name, ChannelId: ch.Id, Enabled: true}).Error)
		}
	}
	return state
}
func capacityPlugin(t *testing.T, c capacityConfig) *plugin.LoadedPlugin {
	t.Helper()
	source := fmt.Sprintf(`export const meta={apiVersion:1,key:"capacity-task",name:"Capacity task",version:"1.0.0",author:{name:"Test"},models:[%q],fetchMode:"per_task",routes:[{method:"POST",path:"/capacity/tasks",type:"submit",action:"generate",decode:"decode",render:"created"}]};
 export const native={decode:function(ctx){return {kind:"submit",model:%q,requestBody:ctx.body.value};},created:function(ctx,task){return {task_id:task.task_id};}};
 export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",method:"POST",body:ctx.requestBody};}
 export function parseSubmitResponse(ctx,resp){return {taskId:resp.body.id};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/query/"+ctx.taskId};}
 export function parseTaskResult(ctx,body){return body;}
 `, c.AsyncModel, c.AsyncModel)
	p, err := plugin.DefaultRegistry.Register(source, plugin.Options{})
	require.NoError(t, err)
	return p
}

func capacityCorrelations(t *testing.T, path string) gin.HandlerFunc {
	t.Helper()
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	var mu sync.Mutex
	return func(c *gin.Context) {
		c.Next()
		marker := c.GetHeader("X-Capacity-Marker")
		if marker == "" || len(marker) > 64 || capacityMarkerPattern.FindString(marker) != marker {
			return
		}
		requestID, fundingID, attempts := "", "", 0
		// Read only an existing plan: RoutingPlanFor would create one on rejected requests.
		if value, ok := c.Get("mowan_routing_plan"); ok {
			if plan, ok := value.(*service.RoutingPlan); ok && plan != nil && plan.RequestID != "" {
				requestID, fundingID, attempts = plan.RequestID, "billing:"+plan.RequestID, plan.Sent
			}
		}
		// This is the existing plan's funding identity, not proof a reserve row exists.
		data, err := common.Marshal(map[string]any{"marker": marker, "request_id": requestID, "funding_id": fundingID, "user_id": c.GetInt("id"), "token_id": c.GetInt("token_id"), "http_status": c.Writer.Status(), "attempts": attempts})
		if err == nil {
			mu.Lock()
			_, err = f.Write(append(data, '\n'))
			mu.Unlock()
		}
		if err != nil {
			t.Errorf("write capacity request correlation: %v", err)
		}
	}
}

func TestRoutingCapacityWorker(t *testing.T) {
	if os.Getenv("CAPACITY_WORKER") != "1" {
		t.Skip("test-only capacity worker")
	}
	var state capacityState
	capacityRead(t, os.Getenv("CAPACITY_STATE"), &state)
	c := state.Config
	require.NoError(t, c.validate())
	require.True(t, capacityOwnedDSN(c), "worker requires explicit owned fixture database before InitDB")
	capacityInitColumns(t, c)
	txStats := &capacityRecorder{}
	db := capacityOpen(t, c, txStats)
	capacitySettings(t, c)
	p := capacityPlugin(t, c)
	pool, err := db.DB()
	require.NoError(t, err)
	httpStats := &capacityRecorder{}
	seen := map[string]int64{}
	var seenMu sync.Mutex
	seenSnapshot := func() map[string]int64 {
		seenMu.Lock()
		defer seenMu.Unlock()
		v := map[string]int64{}
		for k, n := range seen {
			v[k] = n
		}
		return v
	}
	var sampleMu sync.Mutex
	var poolSamples []any
	sampleSnapshot := func() []any { sampleMu.Lock(); defer sampleMu.Unlock(); return append([]any(nil), poolSamples...) }
	correlationPath := strings.TrimSuffix(os.Getenv("CAPACITY_WORKER_REPORT"), ".json") + "-correlations.ndjson"
	engine := gin.New()
	engine.Use(capacityCorrelations(t, correlationPath), gin.Recovery(), func(ctx *gin.Context) {
		ctx.Set(common.RequestIdKey, common.GetRandomString(24))
		at := time.Now()
		ctx.Next()
		if ctx.GetInt("id") > 0 && ctx.GetInt("token_id") > 0 {
			seenMu.Lock()
			seen[fmt.Sprintf("%d/%d", ctx.GetInt("id"), ctx.GetInt("token_id"))]++
			seenMu.Unlock()
		}
		httpStats.add(ctx.Request.Method+" "+ctx.FullPath(), time.Since(at), nil)
	})
	auth := []gin.HandlerFunc{middleware.TokenAuth(), middleware.ModelRequestRateLimit(), middleware.Distribute()}
	engine.POST("/v1/chat/completions", append(auth, func(ctx *gin.Context) { Relay(ctx, types.RelayFormatOpenAI) })...)
	gen := plugin.DefaultRegistry.Generation()
	binding, ok := gen.LookupDeclaredRoute("POST", "/capacity/tasks")
	require.True(t, ok)
	engine.POST("/capacity/tasks", middleware.TokenAuth(), func(ctx *gin.Context) {
		ctx.Set(plugin.ContextKeyPinnedRoute, plugin.PinnedRoute{Generation: gen, Plugin: p, Route: binding.Route})
	}, middleware.PrepareTaskPluginRoute(), middleware.ModelRequestRateLimit(), middleware.Distribute(), RelayTask)
	engine.GET("/v1/tasks/:key", middleware.TokenAuth(), GetTask)
	stop := make(chan struct{})
	var stopOnce sync.Once
	engine.POST("/__capacity/stop", func(ctx *gin.Context) {
		if ctx.GetHeader("X-Capacity-Nonce") != os.Getenv("CAPACITY_NONCE") {
			ctx.AbortWithStatus(403)
			return
		}
		ctx.Status(200)
		stopOnce.Do(func() { close(stop) })
	})
	engine.GET("/__capacity/stats", func(ctx *gin.Context) {
		if ctx.GetHeader("X-Capacity-Nonce") != os.Getenv("CAPACITY_NONCE") {
			ctx.AbortWithStatus(403)
			return
		}
		_ = service.FlushRoutingMetrics(ctx.Request.Context())
		ctx.JSON(200, gin.H{"pid": os.Getpid(), "transaction_latency": txStats.report(), "http_latency": httpStats.report(), "pool": pool.Stats(), "pool_samples": sampleSnapshot(), "authenticated_wallet_key_hits": seenSnapshot()})
	})
	server := httptest.NewServer(engine)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		lastSample := time.Now()
		prior := pool.Stats()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if time.Since(lastSample) >= time.Second {
					now := pool.Stats()
					sampleMu.Lock()
					poolSamples = append(poolSamples, map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "pool": now, "wait_count_delta": now.WaitCount - prior.WaitCount, "wait_ms_delta": float64(now.WaitDuration-prior.WaitDuration) / float64(time.Millisecond)})
					sampleMu.Unlock()
					prior = now
					lastSample = time.Now()
				}
				service.RunTaskPollingOnce(ctx, nil)
			}
		}
	}()
	capacityWrite(t, os.Getenv("CAPACITY_READY"), map[string]any{"url": server.URL, "pid": os.Getpid()})
	select {
	case <-stop:
	case <-time.After(time.Duration(c.totalSeconds()+c.RequestTimeoutSeconds*3+120) * time.Second):
		t.Error("capacity worker lifetime exceeded")
	}
	cancel()
	<-done
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer flushCancel()
	require.NoError(t, service.CloseRoutingMetrics(flushCtx))
	capacityWrite(t, os.Getenv("CAPACITY_WORKER_REPORT"), map[string]any{"pid": os.Getpid(), "transaction_latency": txStats.report(), "http_latency": httpStats.report(), "pool": pool.Stats(), "pool_samples": sampleSnapshot(), "authenticated_wallet_key_hits": seenSnapshot(), "request_correlations": correlationPath})
}

// Avoid standard-library JSON encoding in this repository.
func capacityRead(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(data, out))
}
func capacityWrite(t *testing.T, path string, v any) {
	t.Helper()
	data, err := common.Marshal(v)
	require.NoError(t, err)
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.Write(append(data, '\n'))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}
