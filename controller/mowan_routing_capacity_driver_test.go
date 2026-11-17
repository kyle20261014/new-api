package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type capacityConfig struct {
	SourceRevision                                                                    string
	Dialect, Target, Hardware, Model, AsyncModel                                      string
	Phase, Output, CollectorReport                                                    string
	Workers, Pool, Wallets, InitialQuota                                              int
	WindowSeconds, RequestTimeoutSeconds, MaxInflight                                 int
	UpstreamDelayMS, StreamDurationMS, AsyncDurationMS                                int
	SmartPercent, HotPercent, SyncPercent, StreamPercent, RefundPercent, RetryPercent int
	TopUpIntervalMS, TopUpQuota                                                       int
	RPS                                                                               []float64
	ExpectedPeakRPS, SLOP99MS                                                         float64
	ShortCheck                                                                        bool
}

func (c capacityConfig) validate() error {
	if c.Dialect != "mysql" && c.Dialect != "postgres" {
		return fmt.Errorf("Dialect must be mysql or postgres; SQLite cannot exercise multi-process writers")
	}
	if strings.TrimSpace(c.SourceRevision) == "" {
		return fmt.Errorf("explicit source revision required; phase is not source identity")
	}
	if c.Target == "" || c.Hardware == "" || c.Model == "" || c.AsyncModel == "" || c.Model == c.AsyncModel || c.Output == "" || (c.Phase != "baseline" && c.Phase != "candidate") {
		return fmt.Errorf("Target, Hardware, distinct Model/AsyncModel, Output and Phase are required")
	}
	if c.Workers < 2 || c.Workers > 16 || c.Pool < 1 || c.Pool > 128 || c.Wallets < 2 || c.Wallets > 10000 || c.InitialQuota <= 0 || c.InitialQuota > common.MaxQuota || c.MaxInflight < 1 || c.MaxInflight > 4096 {
		return fmt.Errorf("invalid workers/pool/wallet/quota/inflight bounds")
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > 300 || c.WindowSeconds > 7200 || (!c.ShortCheck && c.WindowSeconds < 1800) || (c.ShortCheck && (c.WindowSeconds < 3 || c.WindowSeconds > 30)) {
		return fmt.Errorf("formal windows require >=1800s; ShortCheck only permits 3..30s")
	}
	if c.UpstreamDelayMS < 1 || c.UpstreamDelayMS > c.RequestTimeoutSeconds*1000 || c.StreamDurationMS < c.UpstreamDelayMS || c.AsyncDurationMS < c.UpstreamDelayMS || max(c.StreamDurationMS, c.AsyncDurationMS) > c.RequestTimeoutSeconds*1000 || max(c.StreamDurationMS, c.AsyncDurationMS)+2*c.UpstreamDelayMS+250 >= c.RequestTimeoutSeconds*1000 || c.TopUpIntervalMS < 100 || c.TopUpIntervalMS > 3600000 || c.TopUpQuota < 1 || c.TopUpQuota > common.MaxQuota {
		return fmt.Errorf("explicit bounded upstream delays and top-up configuration required")
	}
	for _, n := range []int{c.SmartPercent, c.HotPercent, c.SyncPercent, c.StreamPercent, c.RefundPercent, c.RetryPercent} {
		if n <= 0 || n >= 100 {
			return fmt.Errorf("all mix percentages must be 1..99")
		}
	}
	if c.SyncPercent+c.StreamPercent >= 100 || c.RefundPercent+c.RetryPercent >= 100 {
		return fmt.Errorf("mix must leave async and normal successes")
	}
	if len(c.RPS) == 0 || len(c.RPS) > 20 || math.IsNaN(c.ExpectedPeakRPS) || math.IsInf(c.ExpectedPeakRPS, 0) || c.ExpectedPeakRPS <= 0 || c.SLOP99MS <= 0 || math.IsNaN(c.SLOP99MS) || math.IsInf(c.SLOP99MS, 0) {
		return fmt.Errorf("RPS staircase, positive finite expected peak and SLO required")
	}
	var prior float64
	for _, r := range c.RPS {
		if math.IsNaN(r) || math.IsInf(r, 0) || r < 1 || r > 10000 || r <= prior {
			return fmt.Errorf("RPS staircase must increase within 1..10000")
		}
		prior = r
	}
	return nil
}
func (c capacityConfig) totalSeconds() int {
	return len(c.RPS) * (c.WindowSeconds + c.RequestTimeoutSeconds)
}

func capacityOwnedDSN(c capacityConfig) bool {
	dsn := os.Getenv("CAPACITY_DSN")
	if os.Getenv("TEST_ROUTING_DB_FIXTURE") != "1" {
		return false
	}
	name := ""
	if c.Dialect == "postgres" {
		u, err := url.Parse(dsn)
		if err != nil {
			return false
		}
		name = strings.TrimPrefix(u.Path, "/")
	} else {
		if at := strings.LastIndex(dsn, "/"); at >= 0 {
			name = strings.Split(dsn[at+1:], "?")[0]
		}
	}
	return regexp.MustCompile(`^routing_matrix_[a-zA-Z0-9_]+$`).MatchString(name)
}

type capacityChild struct {
	cmd    *exec.Cmd
	done   chan error
	URL    string
	PID    int
	report string
}

func capacityReady(data []byte, pid int) (string, bool) {
	var info struct {
		URL string
		PID int
	}
	if common.Unmarshal(data, &info) != nil || info.PID != pid || !strings.HasPrefix(info.URL, "http://127.0.0.1:") {
		return "", false
	}
	return info.URL, true
}

func capacityStartWorker(t *testing.T, state, dir string, index int, nonce string) *capacityChild {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	ready := filepath.Join(dir, fmt.Sprintf("worker-%d-ready.json", index))
	report := filepath.Join(dir, fmt.Sprintf("worker-%d-report.json", index))
	log, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("worker-%d.log", index)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	child := &capacityChild{report: report, done: make(chan error, 1)}
	child.cmd = exec.Command(exe, "-test.run=^TestRoutingCapacityWorker$", "-test.count=1", "-test.timeout=24h")
	child.cmd.Env = append(os.Environ(), "CAPACITY_WORKER=1", "CAPACITY_STATE="+state, "CAPACITY_READY="+ready, "CAPACITY_WORKER_REPORT="+report, "CAPACITY_NONCE="+nonce)
	child.cmd.Stdout, child.cmd.Stderr = log, log
	require.NoError(t, child.cmd.Start())
	child.PID = child.cmd.Process.Pid
	go func() { child.done <- child.cmd.Wait(); close(child.done); _ = log.Close() }()
	// os.Process is the exact owned handle from Start, never looked up by PID.
	// No user process enumeration or Kill(pid) is used; PID reuse cannot retarget it.
	t.Cleanup(func() {
		select {
		case <-child.done:
			return
		default:
		}
		if child.cmd.Process.Pid == child.PID && child.cmd.Path == exe {
			_ = child.cmd.Process.Kill()
		}
		select {
		case <-child.done:
		case <-time.After(10 * time.Second):
			t.Errorf("owned worker %d did not exit", index)
		}
	})
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(ready); err == nil {
			if endpoint, complete := capacityReady(data, child.PID); complete {
				child.URL = endpoint
				return child
			}
		}
		select {
		case err := <-child.done:
			t.Fatalf("owned worker %d exited before ready (exit=%v); see private worker log", index, err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("owned worker %d startup timeout", index)
	return nil
}
func capacityRequest(client *http.Client, method, endpoint, key string, body []byte) (int, []byte, time.Duration, error) {
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer sk-"+key)
	req.Header.Set("Content-Type", "application/json")
	if marker := capacityMarkerPattern.Find(body); len(marker) > 0 {
		req.Header.Set("X-Capacity-Marker", string(marker))
	}
	at := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer res.Body.Close()
	first := make([]byte, 1)
	n, err := io.ReadFull(res.Body, first)
	firstByte := time.Since(at)
	if err != nil && err != io.EOF {
		return res.StatusCode, nil, firstByte, err
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)-int64(n)))
	return res.StatusCode, append(first[:n], data...), firstByte, err
}
func capacityControl(client *http.Client, child *capacityChild, path, nonce string) ([]byte, error) {
	method := "GET"
	if path == "stop" {
		method = "POST"
	}
	req, err := http.NewRequest(method, child.URL+"/__capacity/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Capacity-Nonce", nonce)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("control status %d", res.StatusCode)
	}
	return data, err
}

type capacityUpstreamState struct {
	sync.Mutex
	Tasks    map[string]time.Time
	Refunds  map[string]bool
	Sends    map[string][]capacitySend
	Evidence *os.File
	Seq      atomic.Int64
}

type capacitySend struct {
	Group, Path string
	Status      int
	At          string
}

var capacityMarkerPattern = regexp.MustCompile(`cap-(normal|retry|refund)-[0-9]+`)

func (s *capacityUpstreamState) reconcile(result *capacityCycleResult) {
	s.Lock()
	defer s.Unlock()
	sends := s.Sends[result.Marker]
	delete(s.Sends, result.Marker)
	group := ""
	successes := 0
	failures := 0
	for _, send := range sends {
		if send.Status == 200 {
			group = send.Group
			successes++
		} else if send.Status == 503 {
			failures++
		}
	}
	if result.Err == nil {
		if result.ExpectedFailure {
			if successes != 0 || failures == 0 {
				result.Err = fmt.Errorf("refund case did not reach actual failing upstream")
			}
		} else if successes != 1 {
			result.Err = fmt.Errorf("expected exactly one actual successful upstream send, got %d", successes)
		} else if !result.ExpectedTerminalFailure {
			result.Debit = int(math.Round(.0001 * common.QuotaPerUnit))
			if group == "cap-dear" {
				result.Debit *= 2
			}
		}
	}
	data, err := common.Marshal(map[string]any{"marker": result.Marker, "worker_pid": result.WorkerPID, "mode": result.Mode, "kind": result.Kind, "wallet": result.UserID, "token_id": result.TokenID, "sends": sends, "expected_debit": result.Debit, "cycle_error": fmt.Sprint(result.Err)})
	if err == nil {
		_, err = s.Evidence.Write(append(data, '\n'))
	}
	if err != nil {
		result.Err = fmt.Errorf("write upstream evidence: %w", err)
	}
}

func capacityUpstream(c capacityConfig, evidence *os.File) (*httptest.Server, *capacityUpstreamState) {
	s := &capacityUpstreamState{Tasks: map[string]time.Time{}, Refunds: map[string]bool{}, Sends: map[string][]capacitySend{}, Evidence: evidence}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		marker := string(data)
		cheap := strings.HasPrefix(r.URL.Path, "/cap-cheap/")
		async := strings.HasSuffix(r.URL.Path, "/submit")
		failSend := !async && (strings.Contains(marker, "cap-refund") || (cheap && strings.Contains(marker, "cap-retry")))
		key := capacityMarkerPattern.FindString(marker)
		if key != "" {
			status := 200
			if failSend {
				status = 503
			}
			group := "cap-dear"
			if cheap {
				group = "cap-cheap"
			}
			s.Lock()
			s.Sends[key] = append(s.Sends[key], capacitySend{Group: group, Path: r.URL.Path, Status: status, At: time.Now().UTC().Format(time.RFC3339Nano)})
			s.Unlock()
		}
		if failSend {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"message":"synthetic capacity failure","type":"upstream_error","code":"capacity_injected"}}`))
			return
		}
		timer := time.NewTimer(time.Duration(c.UpstreamDelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/submit") {
			id := fmt.Sprintf("up-%d", s.Seq.Add(1))
			s.Lock()
			s.Tasks[id] = time.Now().Add(time.Duration(c.AsyncDurationMS) * time.Millisecond)
			s.Refunds[id] = strings.Contains(marker, "cap-refund")
			s.Unlock()
			_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
			return
		}
		if strings.Contains(r.URL.Path, "/query/") {
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			s.Lock()
			ready, ok := s.Tasks[id]
			refund := s.Refunds[id]
			s.Unlock()
			if !ok {
				w.WriteHeader(404)
				return
			}
			if time.Now().Before(ready) {
				_, _ = w.Write([]byte(`{"status":"IN_PROGRESS","progress":"50%"}`))
			} else if refund {
				_, _ = w.Write([]byte(`{"status":"FAILURE","progress":"100%","reason":"synthetic terminal refund"}`))
			} else {
				_, _ = w.Write([]byte(`{"status":"SUCCESS","progress":"100%","url":"https://synthetic.invalid/result"}`))
			}
			return
		}
		var body struct{ Stream bool }
		_ = common.Unmarshal(data, &body)
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for i := 0; i < 4; i++ {
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"cap-stream\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"part\"},\"finish_reason\":null}]}\n\n", c.Model)
				if flusher != nil {
					flusher.Flush()
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Duration(c.StreamDurationMS/4) * time.Millisecond):
				}
			}
			_, _ = w.Write([]byte("data: {\"id\":\"cap-stream\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"))
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":"cap-sync","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`, c.Model)
	}))
	return server, s
}

type capacityCycleResult struct {
	WorkerPID               int
	Marker                  string
	UserID, TokenID, Debit  int
	Mode, Kind, Shape, Case string
	HTTP                    int
	ExpectedFailure         bool
	ExpectedTerminalFailure bool
	ResponseLatency         time.Duration
	Err                     error
}

func capacityPercent(id int64, domain string) int {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", domain, id)))
	return (int(h[0])*256 + int(h[1])) % 100
}

func capacityCycle(c capacityConfig, state *capacityState, children []*capacityChild, client *http.Client, id int64) capacityCycleResult {
	n := int(id)
	wallet := 0
	if capacityPercent(id, "wallet") >= c.HotPercent {
		wallet = 1 + (n % (len(state.Keys) - 1))
	}
	keys := state.Keys[wallet]
	key, mode := keys.Legacy, "legacy"
	if capacityPercent(id, "mode") < c.SmartPercent {
		key, mode = keys.Smart, "smart"
	}
	shape := "distributed"
	if wallet == 0 {
		shape = "hot"
	}
	kind := "async"
	p := capacityPercent(id, "kind")
	if p < c.SyncPercent {
		kind = "sync"
	} else if p < c.SyncPercent+c.StreamPercent {
		kind = "stream"
	}
	caseName := "normal"
	q := capacityPercent(id, "case")
	if q < c.RefundPercent {
		caseName = "refund"
	} else if q < c.RefundPercent+c.RetryPercent {
		caseName = "retry"
	}
	if kind == "async" && caseName == "retry" {
		caseName = "normal"
	}
	result := capacityCycleResult{Mode: mode, Kind: kind, Shape: shape, Case: caseName, ExpectedFailure: caseName == "refund" && kind != "async", ExpectedTerminalFailure: caseName == "refund" && kind == "async"}
	result.UserID = keys.UserID
	result.TokenID = keys.LegacyID
	if mode == "smart" {
		result.TokenID = keys.SmartID
	}
	marker := fmt.Sprintf("cap-%s-%d", caseName, id)
	result.Marker = marker
	payload := map[string]any{"model": c.Model, "messages": []any{map[string]any{"role": "user", "content": marker}}, "stream": kind == "stream", "max_tokens": 4}
	path := "/v1/chat/completions"
	if kind == "async" {
		path = "/capacity/tasks"
		payload = map[string]any{"model": c.AsyncModel, "case": marker}
	}
	body, err := common.Marshal(payload)
	if err != nil {
		result.Err = err
		return result
	}
	child := children[n%len(children)]
	result.WorkerPID = child.PID
	at := time.Now()
	status, data, firstByte, err := capacityRequest(client, "POST", child.URL+path, key, body)
	result.ResponseLatency = time.Since(at)
	if kind == "stream" && !result.ExpectedFailure {
		result.ResponseLatency = firstByte
	}
	result.HTTP = status
	if err != nil {
		result.Err = err
		return result
	}
	if result.ExpectedFailure {
		if status < 400 {
			result.Err = fmt.Errorf("injected failure unexpectedly accepted")
		}
		return result
	}
	if status < 200 || status >= 300 {
		result.Err = fmt.Errorf("HTTP %d: %s", status, string(data[:min(len(data), 400)]))
		return result
	}
	if kind == "stream" && !bytes.Contains(data, []byte("[DONE]")) {
		result.Err = fmt.Errorf("stream missing terminal DONE")
	}
	if kind != "async" {
		return result
	}
	var task struct {
		TaskID string `json:"task_id"`
	}
	if err = common.Unmarshal(data, &task); err != nil || task.TaskID == "" {
		result.Err = fmt.Errorf("async submit missing public task ID: %s", string(data[:min(len(data), 400)]))
		return result
	}
	deadline := time.Now().Add(time.Duration(c.RequestTimeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		at = time.Now()
		status, data, _, err = capacityRequest(client, "GET", child.URL+"/v1/tasks/"+url.PathEscape(task.TaskID), key, nil)
		result.ResponseLatency = max(result.ResponseLatency, time.Since(at))
		if err != nil || status != 200 {
			result.Err = fmt.Errorf("async fetch failed status=%d", status)
			return result
		}
		var terminal struct{ Status string }
		if common.Unmarshal(data, &terminal) != nil {
			result.Err = fmt.Errorf("bad task response")
			return result
		}
		if terminal.Status == "SUCCESS" {
			if result.ExpectedTerminalFailure {
				result.Err = fmt.Errorf("async refund expected terminal FAILURE")
			}
			return result
		}
		if terminal.Status == "FAILURE" {
			if !result.ExpectedTerminalFailure {
				result.Err = fmt.Errorf("unexpected async terminal failure")
			}
			return result
		}
		time.Sleep(50 * time.Millisecond)
	}
	result.Err = fmt.Errorf("async terminal deadline exceeded")
	return result
}

type capacityCounts struct {
	Arrivals, Completed, Rejected, Errors, InjectedFailures int64
	Inflight, PeakInflight                                  int64
	ByScenario                                              map[string]int64
	ErrorExamples                                           []string
}
type capacityLoad struct {
	sync.Mutex
	Counts    capacityCounts
	Latency   capacityRecorder
	Responses capacityRecorder
	Samples   []map[string]any
}

func (l *capacityLoad) snapshot() capacityCounts {
	l.Lock()
	defer l.Unlock()
	v := l.Counts
	v.ByScenario = map[string]int64{}
	for k, n := range l.Counts.ByScenario {
		v.ByScenario[k] = n
	}
	v.ErrorExamples = append([]string(nil), l.Counts.ErrorExamples...)
	return v
}
func capacityWindow(c capacityConfig, state *capacityState, children []*capacityChild, client *http.Client, rps float64, sequence *atomic.Int64) *capacityLoad {
	l := &capacityLoad{Counts: capacityCounts{ByScenario: map[string]int64{}}}
	var wg sync.WaitGroup
	sem := make(chan struct{}, c.MaxInflight)
	start := time.Now()
	end := start.Add(time.Duration(c.WindowSeconds) * time.Second)
	next := start
	sampleAt := start
	for time.Now().Before(end) {
		now := time.Now()
		if now.Before(next) {
			time.Sleep(time.Until(next))
			continue
		}
		if !now.Before(end) {
			break
		}
		if !now.Before(sampleAt) {
			l.Samples = append(l.Samples, map[string]any{"elapsed_s": now.Sub(start).Seconds(), "counts": l.snapshot()})
			sampleAt = sampleAt.Add(time.Second)
		}
		next = next.Add(time.Duration(float64(time.Second) / rps))
		l.Lock()
		l.Counts.Arrivals++
		select {
		case sem <- struct{}{}:
			l.Counts.Inflight++
			l.Counts.PeakInflight = max(l.Counts.PeakInflight, l.Counts.Inflight)
		default:
			l.Counts.Rejected++
			l.Unlock()
			continue
		}
		l.Unlock()
		id := sequence.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			at := time.Now()
			result := capacityCycle(c, state, children, client, id)
			state.Evidence.reconcile(&result)
			if result.Err == nil && !result.ExpectedFailure && !result.ExpectedTerminalFailure {
				state.ExpectedMu.Lock()
				state.ExpectedUsers[result.UserID] += int64(result.Debit)
				state.ExpectedTokens[result.TokenID] += int64(result.Debit)
				state.ExpectedMu.Unlock()
			}
			l.Latency.add(result.Mode+"/"+result.Kind+"/"+result.Shape+"/"+result.Case, time.Since(at), result.Err)
			l.Responses.add(result.Mode+"/"+result.Kind+"/"+result.Shape+"/"+result.Case, result.ResponseLatency, result.Err)
			l.Lock()
			defer l.Unlock()
			l.Counts.Inflight--
			l.Counts.Completed++
			l.Counts.ByScenario[result.Mode+"/"+result.Kind+"/"+result.Shape+"/"+result.Case]++
			if (result.ExpectedFailure || result.ExpectedTerminalFailure) && result.Err == nil {
				l.Counts.InjectedFailures++
			}
			if result.Err != nil {
				l.Counts.Errors++
				if len(l.Counts.ErrorExamples) < 12 {
					l.Counts.ErrorExamples = append(l.Counts.ErrorExamples, result.Err.Error())
				}
			}
		}()
	}
	wg.Wait()
	l.Samples = append(l.Samples, map[string]any{"elapsed_s": time.Since(start).Seconds(), "counts": l.snapshot()})
	return l
}
func capacityFunds(t *testing.T, state *capacityState, credits map[int]int64) map[string]any {
	t.Helper()
	db := model.DB
	var ops []model.RoutingFundingOperation
	require.NoError(t, db.Find(&ops).Error)
	tokenDebit := map[int]int64{}
	kinds := map[string]int64{}
	supplements := 0
	for _, op := range ops {
		tokenDebit[op.TokenID] += int64(op.Delta)
		kinds[op.Kind]++
		if op.Kind == "reserve" && op.Delta > 0 && op.Quota > op.Delta {
			supplements++
		}
	}
	var diffs []map[string]any
	negative := 0
	for _, k := range state.Keys {
		var u model.User
		require.NoError(t, db.First(&u, k.UserID).Error)
		diff := int64(state.Config.InitialQuota) + credits[u.Id] - int64(u.Quota) - state.ExpectedUsers[u.Id]
		if diff != 0 {
			diffs = append(diffs, map[string]any{"user_id": u.Id, "wallet_difference": diff})
		}
		if u.Quota < 0 {
			negative++
		}
		for _, id := range []int{k.LegacyID, k.SmartID} {
			var tok model.Token
			require.NoError(t, db.First(&tok, id).Error)
			d := int64(state.Config.InitialQuota) - int64(tok.RemainQuota) - state.ExpectedTokens[id]
			used := int64(tok.UsedQuota) - state.ExpectedTokens[id]
			if d != 0 || used != 0 {
				diffs = append(diffs, map[string]any{"token_id": id, "remain_difference": d, "used_difference": used})
			}
			if tok.RemainQuota < 0 {
				negative++
			}
			if id == k.SmartID && tokenDebit[id] != state.ExpectedTokens[id] {
				diffs = append(diffs, map[string]any{"smart_token_id": id, "ledger_vs_expected": tokenDebit[id] - state.ExpectedTokens[id]})
			}
		}
	}
	var reserved, unfinished, unresolved int64
	require.NoError(t, db.Model(&model.RoutingFundingSession{}).Where("status = ?", "reserved").Count(&reserved).Error)
	require.NoError(t, db.Model(&model.Task{}).Where("status NOT IN ?", []string{"SUCCESS", "FAILURE"}).Count(&unfinished).Error)
	require.NoError(t, db.Model(&model.RoutingSubmission{}).Where("state IN ?", []string{"prepared", "sending", "unknown"}).Count(&unresolved).Error)
	var boots []string
	require.NoError(t, db.Model(&model.RoutingMetricBucket{}).Distinct().Pluck("node_boot_id", &boots).Error)
	return map[string]any{"differences": diffs, "difference_count": len(diffs), "negative_wallet_or_key": negative, "reserved_sessions": reserved, "unfinished_tasks": unfinished, "unresolved_submissions": unresolved, "ledger_operations": len(ops), "operation_kinds": kinds, "supplemental_reserves": supplements, "observation_boot_ids": boots, "observation_boot_count": len(boots), "definition": "wallet+fixture credits and finite key remain/used independently checked against actual upstream marker/final successful group at fixed fixture price; smart additionally checked against durable ledger SUM(delta); legacy anonymous deltas are not invented as ledger entries; fixture credits are not payment API coverage"}
}

func TestRoutingCapacityMixed(t *testing.T) {
	if os.Getenv("TEST_ROUTING_MIXED_CAPACITY") != "1" {
		t.Skip("opt in via scripts/smart-routing-capacity.ps1")
	}
	var c capacityConfig
	capacityRead(t, os.Getenv("CAPACITY_CONFIG"), &c)
	require.NoError(t, c.validate())
	require.True(t, capacityOwnedDSN(c), "explicit disposable routing_matrix_* DB + TEST_ROUTING_DB_FIXTURE=1 required")
	require.NoError(t, os.Mkdir(c.Output, 0700), "phase output must not already exist")
	evidence, err := os.OpenFile(filepath.Join(c.Output, "upstream-sends.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	require.NoError(t, err)
	defer evidence.Close()
	upstream, upstreamEvidence := capacityUpstream(c, evidence)
	defer upstream.Close()
	state := capacitySeed(t, c, upstream.URL)
	state.Evidence = upstreamEvidence
	driverTxStats := &capacityRecorder{}
	capacityOpen(t, c, driverTxStats)
	statePath := filepath.Join(c.Output, "state.private.json")
	capacityWrite(t, statePath, state)
	nonce := common.GetRandomString(48)
	children := make([]*capacityChild, 0, c.Workers)
	for i := 0; i < c.Workers; i++ {
		children = append(children, capacityStartWorker(t, statePath, c.Output, i, nonce))
	}
	client := &http.Client{Timeout: time.Duration(c.RequestTimeoutSeconds) * time.Second}
	defer client.CloseIdleConnections()
	// Concurrent wallet credits use the actual shared model primitive. They are
	// fixture top-ups, tracked separately; payment callback coverage is not claimed.
	credits := map[int]int64{}
	var creditMu sync.Mutex
	creditStop := make(chan struct{})
	creditDone := make(chan struct{})
	var creditError atomic.Int64
	go func() {
		defer close(creditDone)
		tick := time.NewTicker(time.Duration(c.TopUpIntervalMS) * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-creditStop:
				return
			case <-tick.C:
				for _, k := range state.Keys {
					creditMu.Lock()
					if err := model.IncreaseUserQuota(k.UserID, c.TopUpQuota, false); err != nil {
						creditError.Add(1)
					} else {
						credits[k.UserID] += int64(c.TopUpQuota)
					}
					creditMu.Unlock()
				}
			}
		}
	}()
	stopped := false
	stopCredits := func() {
		if !stopped {
			close(creditStop)
			<-creditDone
			stopped = true
		}
	}
	defer stopCredits()
	started := time.Now()
	var sequence atomic.Int64
	windows := []map[string]any{}
	var highest float64
	for _, rps := range c.RPS {
		at := time.Now()
		load := capacityWindow(c, state, children, client, rps, &sequence)
		counts := load.snapshot()
		latency := load.Latency.report()
		responses := load.Responses.report()
		creditMu.Lock()
		funds := capacityFunds(t, state, credits)
		creditMu.Unlock()
		stable := counts.Rejected == 0 && counts.Errors == 0 && counts.Completed == counts.Arrivals && funds["difference_count"] == 0 && funds["negative_wallet_or_key"] == 0 && funds["reserved_sessions"] == int64(0) && funds["unfinished_tasks"] == int64(0)
		for _, v := range responses {
			if v.(map[string]any)["p99_ms"].(int64) > int64(c.SLOP99MS) {
				stable = false
			}
		}
		measured := float64(counts.Completed) / time.Since(at).Seconds()
		if stable && !c.ShortCheck {
			highest = max(highest, measured)
		}
		workerStats := []any{}
		for _, ch := range children {
			data, err := capacityControl(client, ch, "stats", nonce)
			require.NoError(t, err)
			var v any
			require.NoError(t, common.Unmarshal(data, &v))
			workerStats = append(workerStats, v)
		}
		windows = append(windows, map[string]any{"arrival_rps": rps, "window_seconds": c.WindowSeconds, "wall_with_drain_seconds": time.Since(at).Seconds(), "completed_rps_with_drain": measured, "counts": counts, "samples": load.Samples, "cycle_latency": latency, "protocol_response_max_per_cycle": responses, "response_slo_definition": "per-cycle maximum of full sync/async-submit/query JSON response times or stream first body byte; complete stream/async lifecycle remains in cycle_latency and retains configured request deadlines; first body byte is not first model token", "funds": funds, "local_stable_diagnostic": stable, "worker_stats": workerStats})
	}
	stopCredits()
	workerReports := []any{}
	for _, ch := range children {
		_, err := capacityControl(client, ch, "stop", nonce)
		require.NoError(t, err)
		select {
		case err := <-ch.done:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Fatal("owned worker stop timeout")
		}
		var v any
		capacityRead(t, ch.report, &v)
		workerReports = append(workerReports, v)
	}
	finalFunds := capacityFunds(t, state, credits)
	exe, err := os.Executable()
	require.NoError(t, err)
	binary, err := os.ReadFile(exe)
	require.NoError(t, err)
	sum := sha256.Sum256(binary)
	pool, err := model.DB.DB()
	require.NoError(t, err)
	var dbVersion string
	query := "SELECT VERSION()"
	require.NoError(t, model.DB.Raw(query).Scan(&dbVersion).Error)
	blocked := []string{"unreviewed synthetic upstream workload; no production migration reconciliation/old-writer isolation evidence", "test-only bootstrap is not production startup or deployment authorization", "no actual payment webhook/top-up API workload", "database CPU/IO/lock/deadlock/retry collector not measured"}
	var collector any
	if c.CollectorReport != "" {
		capacityRead(t, c.CollectorReport, &collector)
		blocked = append(blocked, "external collector attached but must be independently checked for this exact target/window")
	}
	if c.ShortCheck {
		blocked = append(blocked, "short-check windows <1800s; maximum stable capacity and 70% limit not established")
	}
	if finalFunds["observation_boot_count"].(int) < c.Workers {
		blocked = append(blocked, "not all worker boot IDs observed")
	}
	if creditError.Load() != 0 {
		blocked = append(blocked, "fixture top-up failures")
	}
	report := map[string]any{"schema": "smart-routing-mixed-capacity.v1", "reviewed": false, "capacity_passed": false, "a21": "blocked", "phase": c.Phase, "config": c, "started_at": started.UTC().Format(time.RFC3339Nano), "finished_at": time.Now().UTC().Format(time.RFC3339Nano), "binary_sha256": fmt.Sprintf("%x", sum), "db_version": dbVersion, "fixture_topup_transaction_latency": driverTxStats.report(), "driver_pool": pool.Stats(), "workers": workerReports, "windows": windows, "final_funds": finalFunds, "fixture_credits": credits, "fixture_credit_errors": creditError.Load(), "maximum_stable_rps_diagnostic": highest, "seventy_percent_rps_diagnostic": highest * .7, "expected_peak_within_diagnostic": highest > 0 && c.ExpectedPeakRPS <= highest*.7, "external_collector": collector, "blocked_reasons": blocked, "transaction_latency_definition": "database/sql BeginTx invocation through Commit/Rollback return, including connection wait; ledger INSERT kind/quota/delta identify initial reserve, positive supplement, zero-money prepare, settle, refund, accepted or delta; legacy transactions without ledger labels remain unclassified; driver transactions are fixture-only top-ups; HTTP and logical-cycle latency separate; uncapped ceil-ms percentile buckets. Async retry via non-2xx is excluded because acceptance may be unknown; async refunds use accepted tasks reaching terminal FAILURE."}
	capacityWrite(t, filepath.Join(c.Output, "report.json"), report)
	t.Logf("mixed capacity phase=%s workers=%d arrivals=%d report=%s capacity_passed=false A21=blocked", c.Phase, c.Workers, sequence.Load(), filepath.Join(c.Output, "report.json"))
	require.Equal(t, 0, finalFunds["difference_count"], "wallet/key ledger reconciliation failed")
	require.Equal(t, 0, finalFunds["negative_wallet_or_key"])
	require.Equal(t, int64(0), finalFunds["reserved_sessions"])
	require.Equal(t, int64(0), finalFunds["unfinished_tasks"])
	require.Equal(t, int64(0), finalFunds["unresolved_submissions"])
	require.GreaterOrEqual(t, finalFunds["observation_boot_count"].(int), c.Workers)
	require.Zero(t, creditError.Load())
	for _, w := range windows {
		counts := w["counts"].(capacityCounts)
		require.Zero(t, counts.Errors, "unexpected HTTP/terminal errors: %v", counts.ErrorExamples)
	}
}

func TestRoutingCapacityMixedBoundaries(t *testing.T) {
	c := capacityConfig{Dialect: "mysql", Target: "test", Hardware: "test", Model: "capacity-text", AsyncModel: "capacity-video", Phase: "candidate", Output: "unused", Workers: 2, Pool: 2, Wallets: 3, InitialQuota: 100000, WindowSeconds: 3, RequestTimeoutSeconds: 5, MaxInflight: 10, UpstreamDelayMS: 1, StreamDurationMS: 10, AsyncDurationMS: 10, SmartPercent: 50, HotPercent: 50, SyncPercent: 35, StreamPercent: 35, RefundPercent: 10, RetryPercent: 10, TopUpIntervalMS: 1000, TopUpQuota: 10, RPS: []float64{2, 4}, ExpectedPeakRPS: 1, SLOP99MS: 1000, ShortCheck: true}
	c.SourceRevision = "synthetic-test-only"
	require.NoError(t, c.validate())
	bad := c
	bad.ShortCheck = false
	require.Error(t, bad.validate())
	bad = c
	bad.Workers = 1
	require.Error(t, bad.validate())
	bad = c
	bad.ExpectedPeakRPS = math.NaN()
	require.Error(t, bad.validate())
	bad = c
	bad.RPS = []float64{4, 2}
	require.Error(t, bad.validate())
	bad = c
	bad.Dialect = "sqlite"
	require.Error(t, bad.validate())
	recorder := capacityRecorder{}
	recorder.add("reserve", time.Millisecond+1, nil)
	r := recorder.report()["reserve"].(map[string]any)
	require.Equal(t, int64(2), r["p99_ms"])
	recorder.add("slow", 120*time.Second, nil)
	require.Equal(t, int64(120000), recorder.report()["slow"].(map[string]any)["p99_ms"])
	_, ready := capacityReady([]byte(`{"URL":`), 12)
	require.False(t, ready)
	_, ready = capacityReady([]byte(`{"URL":"http://127.0.0.1:1234","PID":12}`), 12)
	require.True(t, ready)
	bad = c
	bad.UpstreamDelayMS = 3000
	bad.StreamDurationMS = 9000
	bad.AsyncDurationMS = 9000
	bad.RequestTimeoutSeconds = 10
	require.Error(t, bad.validate())
	bad = c
	bad.TopUpIntervalMS = math.MaxInt
	require.Error(t, bad.validate())
	bad = c
	bad.UpstreamDelayMS = math.MaxInt
	require.Error(t, bad.validate())
	bad = c
	bad.StreamDurationMS = math.MaxInt
	require.Error(t, bad.validate())
	for _, q := range []struct {
		SQL  string
		Args []any
		Want string
	}{
		{"INSERT INTO `mowan_routing_funding_operations` (`kind`,`quota`,`delta`) VALUES (?,?,?)", []any{"reserve", 40, 10}, "funding_supplement"},
		{`INSERT INTO "mowan_routing_funding_operations" ("delta","kind","quota") VALUES ($1,$2,$3) RETURNING *`, []any{30, "reserve", 30}, "funding_reserve"},
	} {
		tx := capacityTx{kind: "unclassified"}
		tx.observe(q.SQL, q.Args)
		require.Equal(t, q.Want, tx.kind)
	}
}

func TestRoutingCapacityStreamFirstByteExcludesTailDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: first\n\n"))
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	at := time.Now()
	status, data, firstByte, err := capacityRequest(server.Client(), "POST", server.URL, "test-only", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(data), "[DONE]")
	require.GreaterOrEqual(t, time.Since(at)-firstByte, 40*time.Millisecond)
}

func TestRoutingRetryStreamCompletionAccounting(t *testing.T) {
	for _, outcome := range []string{"completed_then_cancel", "failure_after_timeout", "cancel_without_usage"} {
		t.Run(outcome, func(t *testing.T) {
			cheapGate, dearGate, settleGate := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var cheapOnce, dearOnce, settleOnce sync.Once
			unblock := func() {
				cheapOnce.Do(func() { close(cheapGate) })
				dearOnce.Do(func() { close(dearGate) })
				settleOnce.Do(func() { close(settleGate) })
			}
			defer unblock()
			await := func(ch <-chan struct{}, event string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatalf("waiting for %s", event)
				}
			}
			cheapEntered, dearEntered := make(chan struct{}), make(chan struct{})
			providerDone, providerCanceled := make(chan struct{}), make(chan struct{})
			streamReady, settling := make(chan struct{}), make(chan struct{})
			var sends atomic.Int32
			var providerBytes int
			var providerErr error
			const success = "data: {\"id\":\"retry-completed\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"retry-completed\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4o\",\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
			const failure = `{"error":{"message":"fixture retry failure","type":"upstream_error"}}`
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if strings.HasPrefix(r.URL.Path, "/cheap/") {
					close(cheapEntered)
					<-cheapGate
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, failure)
					return
				}
				assert.Equal(t, "/dear/v1/chat/completions", r.URL.Path)
				close(dearEntered)
				<-dearGate
				defer close(providerDone)
				if outcome == "failure_after_timeout" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					providerBytes, providerErr = io.WriteString(w, failure)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if outcome == "cancel_without_usage" {
					providerBytes, providerErr = io.WriteString(w, ": waiting for generation\n\n")
					w.(http.Flusher).Flush()
					close(streamReady)
					select {
					case <-r.Context().Done():
						close(providerCanceled)
					case <-time.After(5 * time.Second):
						t.Error("upstream stream was not canceled")
					}
					return
				}
				providerBytes, providerErr = io.WriteString(w, success)
			}))
			t.Cleanup(upstream.Close)
			db, token := smartProtocolEntryDB(t, "gpt-4o", constant.ChannelTypeOpenAI, upstream.URL+"/cheap")
			previousTimeout := constant.StreamingTimeout
			constant.StreamingTimeout = 30
			t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
			common.LogConsumeEnabled = true
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","cheap":"Cheap","dear":"Dear"}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"cheap":1,"dear":2}`))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o":0.0001}`))
			require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "ModelPrice").Update("value", ratio_setting.ModelPrice2JSONString()).Error)
			require.NoError(t, db.Model(&model.TokenRouting{}).Where("token_id = ?", token.Id).Update("policy_json", `{"schema_version":1,"mode":"smart","strategy":"custom","models":{"gpt-4o":{"strategy":"manual","groups":["cheap","dear"]}}}`).Error)
			var channel model.Channel
			require.NoError(t, db.First(&channel).Error)
			require.NoError(t, db.Model(&channel).Update("group", "cheap").Error)
			require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("group", "cheap").Error)
			dearURL := upstream.URL + "/dear"
			channel.Id, channel.Name, channel.Group, channel.BaseURL = 0, "retry-dear", "dear", &dearURL
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, db.Create(&model.Ability{Group: "dear", Model: "gpt-4o", ChannelId: channel.Id, Enabled: true, Weight: 100}).Error)
			model.InvalidatePricingCache()
			// Hold the actual settlement transaction after SSE parsing, not an upstream status observer.
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("retry_stream_settle_gate", func(tx *gorm.DB) {
				if op, ok := tx.Statement.Dest.(*model.RoutingFundingOperation); ok && op.Kind == "settle" && outcome == "completed_then_cancel" {
					close(settling)
					<-settleGate
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove("retry_stream_settle_gate")) })
			gatewayDone := make(chan struct{})
			requestContext := make(chan context.Context, 1)
			var plan *service.RoutingPlan
			var successful bool
			var logRequestID string
			router := gin.New()
			router.Use(middleware.RequestId())
			router.Use(func(c *gin.Context) {
				requestContext <- c.Request.Context()
				defer close(gatewayDone)
				c.Next()
				plan = service.RoutingPlanFor(c)
				successful = service.RequestPolicy(c).Successful
				logRequestID = c.GetString(common.RequestIdKey)
			}, middleware.TokenAuth(), middleware.Distribute())
			router.POST("/v1/chat/completions", func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
			gateway := httptest.NewServer(router)
			t.Cleanup(gateway.Close)
			clientContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(clientContext, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"retry completion gate"}],"max_tokens":4,"stream":true,"stream_options":{"include_usage":true}}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			req.Header.Set("Content-Type", "application/json")
			client := gateway.Client()
			if outcome == "failure_after_timeout" {
				client.Timeout = 2 * time.Second
			}
			clientDone := make(chan struct{})
			var clientErr error
			var clientBody []byte
			go func() {
				defer close(clientDone)
				res, err := client.Do(req)
				clientErr = err
				if err == nil {
					defer res.Body.Close()
					clientBody, clientErr = io.ReadAll(res.Body)
				}
			}()
			await(cheapEntered, "cheap upstream send")
			var funds []model.RoutingFundingSession
			assertReserved := func(quota int) {
				t.Helper()
				require.NoError(t, db.Find(&funds).Error)
				require.Len(t, funds, 1)
				require.Equal(t, "reserved", funds[0].Status)
				require.Equal(t, quota, funds[0].Quota)
				assertSmartEntryBalances(t, db, quota)
			}
			assertReserved(50)
			fundingID := funds[0].FundingID
			cheapOnce.Do(func() { close(cheapGate) })
			await(dearEntered, "dear upstream send")
			assertReserved(100)
			require.Equal(t, fundingID, funds[0].FundingID)
			inbound := <-requestContext
			if outcome == "failure_after_timeout" {
				await(clientDone, "client HTTP timeout")
				await(inbound.Done(), "gateway observing client timeout")
			} else {
				dearOnce.Do(func() { close(dearGate) })
				if outcome == "completed_then_cancel" {
					await(providerDone, "provider finish, usage and DONE written")
					require.NoError(t, providerErr)
					require.Equal(t, len(success), providerBytes)
					await(settling, "parsed stream entering actual Settle")
				} else {
					await(streamReady, "provider stream open without output or usage")
				}
				cancel()
				await(clientDone, "real HTTP client cancellation")
				await(inbound.Done(), "gateway observing client cancellation")
			}
			require.Error(t, clientErr)
			unblock()
			await(providerDone, "provider handler return")
			await(gatewayDone, "Relay and accounting return")
			if outcome == "cancel_without_usage" {
				await(providerCanceled, "upstream stream context canceled")
				require.NoError(t, providerErr)
				assert.Equal(t, len(": waiting for generation\n\n"), providerBytes)
				assert.Empty(t, clientBody)
			}
			assert.Equal(t, outcome == "completed_then_cancel", successful)
			require.NotNil(t, plan)
			assert.Equal(t, 2, plan.Sent)
			assert.Equal(t, "cheap", plan.FirstSelected.Group)
			assert.Equal(t, "dear", plan.Selected.Group)
			assert.Equal(t, "billing:"+plan.RequestID, fundingID)
			require.NoError(t, db.Find(&funds).Error)
			require.Len(t, funds, 1)
			wantQuota, wantStatus, terminal := 100, "settled", "settle"
			// A stream canceled before any data has zero usage: Settle(0) releases
			// the reservation without recording a successful upstream completion.
			if outcome == "cancel_without_usage" {
				wantQuota = 0
			}
			if outcome == "failure_after_timeout" {
				wantQuota, wantStatus, terminal = 0, "refunded", "refund"
				require.NoError(t, providerErr)
				require.Equal(t, len(failure), providerBytes)
			}
			assert.Equal(t, fundingID, funds[0].FundingID)
			assert.Equal(t, wantStatus, funds[0].Status)
			assert.Equal(t, wantQuota, funds[0].Quota)
			assertSmartEntryBalances(t, db, wantQuota)
			var finalToken model.Token
			require.NoError(t, db.First(&finalToken, token.Id).Error)
			assert.Equal(t, wantQuota, finalToken.UsedQuota)
			var logs []model.Log
			require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
			if outcome == "failure_after_timeout" {
				assert.Empty(t, logs)
			} else {
				require.Len(t, logs, 1)
				require.NotEmpty(t, logRequestID)
				assert.Equal(t, logRequestID, logs[0].RequestId)
				assert.Equal(t, "dear", logs[0].Group)
				assert.True(t, logs[0].IsStream)
				assert.Equal(t, wantQuota, logs[0].Quota)
				if outcome == "completed_then_cancel" {
					assert.Equal(t, 8, logs[0].PromptTokens)
					assert.Equal(t, 4, logs[0].CompletionTokens)
				} else {
					assert.Zero(t, logs[0].PromptTokens)
					assert.Zero(t, logs[0].CompletionTokens)
				}
			}
			var operations []model.RoutingFundingOperation
			require.NoError(t, db.Order("quota, kind").Find(&operations).Error)
			require.Len(t, operations, 3)
			reserves, terminals := map[int]int{}, 0
			for _, op := range operations {
				assert.Equal(t, fundingID, op.FundingID)
				assert.Equal(t, token.UserId, op.UserID)
				assert.Equal(t, token.Id, op.TokenID)
				if op.Kind == "reserve" {
					reserves[op.Quota] = op.Delta
					assert.False(t, op.AllowDebt)
				} else {
					terminals++
					assert.Equal(t, terminal, op.Kind)
					assert.Equal(t, wantQuota, op.Quota)
					assert.Equal(t, wantQuota-100, op.Delta)
				}
			}
			assert.Equal(t, map[int]int{50: 50, 100: 50}, reserves)
			assert.Equal(t, 1, terminals)
			assert.EqualValues(t, 2, sends.Load())
			t.Logf("outcome=%s request=%s funding=%s upstream_sends=%d provider_bytes=%d provider_completed=%t upstream_usage=%t successful=%t status=%s quota=%d reserves=%v", outcome, plan.RequestID, fundingID, sends.Load(), providerBytes, outcome == "completed_then_cancel", outcome == "completed_then_cancel", successful, funds[0].Status, funds[0].Quota, reserves)
		})
	}
}

func TestRoutingCapacityRequestCorrelations(t *testing.T) {
	t.Run("retry_uses_one_actual_funding_session", func(t *testing.T) {
		var dbFunds []model.RoutingFundingSession
		var attemptFunds []string
		var attemptMu sync.Mutex
		var sends atomic.Int32
		var inspectFunds func()
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inspectFunds()
			w.Header().Set("Content-Type", "application/json")
			if sends.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture retry","type":"upstream_error"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"fixture-response","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
		}))
		t.Cleanup(upstream.Close)
		db, token := smartProtocolEntryDB(t, "gpt-4o", constant.ChannelTypeOpenAI, upstream.URL)
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"gpt-4o":0.0001}`))
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "ModelPrice").Update("value", ratio_setting.ModelPrice2JSONString()).Error)
		var channel model.Channel
		require.NoError(t, db.First(&channel).Error)
		channel.Id, channel.Name = 0, "retry-channel"
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: "gpt-4o", ChannelId: channel.Id, Enabled: true, Weight: 100}).Error)
		inspectFunds = func() {
			attemptMu.Lock()
			defer attemptMu.Unlock()
			var funds []model.RoutingFundingSession
			require.NoError(t, db.Find(&funds).Error)
			require.Len(t, funds, 1)
			attemptFunds = append(attemptFunds, funds[0].FundingID)
		}
		path := filepath.Join(t.TempDir(), "correlations.ndjson")
		router := gin.New()
		router.Use(capacityCorrelations(t, path), middleware.TokenAuth(), middleware.Distribute())
		router.POST("/v1/chat/completions", func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
		gateway := httptest.NewServer(router)
		t.Cleanup(gateway.Close)
		status, _, _, err := capacityRequest(gateway.Client(), http.MethodPost, gateway.URL+"/v1/chat/completions", token.Key, []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"cap-retry-1"}],"max_tokens":4}`))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		gateway.Close()
		require.Equal(t, int32(2), sends.Load())
		require.NoError(t, db.Find(&dbFunds).Error)
		require.Len(t, dbFunds, 1)
		require.Equal(t, []string{dbFunds[0].FundingID, dbFunds[0].FundingID}, attemptFunds)
		require.Equal(t, "settled", dbFunds[0].Status)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		require.Len(t, lines, 1)
		var row struct {
			Marker    string `json:"marker"`
			RequestID string `json:"request_id"`
			FundingID string `json:"funding_id"`
			UserID    int    `json:"user_id"`
			TokenID   int    `json:"token_id"`
			Status    int    `json:"http_status"`
			Attempts  int    `json:"attempts"`
		}
		require.NoError(t, common.Unmarshal(lines[0], &row))
		require.Equal(t, "cap-retry-1", row.Marker)
		require.NotEmpty(t, row.RequestID)
		require.Equal(t, "billing:"+row.RequestID, row.FundingID)
		require.Equal(t, dbFunds[0].FundingID, row.FundingID)
		require.Equal(t, token.UserId, row.UserID)
		require.Equal(t, token.Id, row.TokenID)
		require.Equal(t, http.StatusOK, row.Status)
		require.Equal(t, 2, row.Attempts)
		require.NotContains(t, string(data), token.Key)
		require.NotContains(t, string(data), "fixture-upstream-key")
		require.NotContains(t, string(data), "messages")
	})
	t.Run("observation_does_not_create_missing_plan", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "correlations.ndjson")
		var observed *gin.Context
		router := gin.New()
		router.Use(func(c *gin.Context) { observed = c; c.Next() }, capacityCorrelations(t, path))
		router.POST("/capacity/tasks", func(c *gin.Context) {
			c.Set(smartrouting.ContextKey, &model.TokenRouting{Mode: "smart"})
			c.Set("id", 1)
			c.Set("token_id", 2)
			c.AbortWithStatus(http.StatusForbidden)
		})
		for _, marker := range []string{"cap-normal-2", "private cap-normal-3 prompt", ""} {
			req := httptest.NewRequest(http.MethodPost, "/capacity/tasks", strings.NewReader("private prompt"))
			req.Header.Set("X-Capacity-Marker", marker)
			router.ServeHTTP(httptest.NewRecorder(), req)
			_, exists := observed.Get("mowan_routing_plan")
			require.False(t, exists)
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var row struct {
			Marker    string `json:"marker"`
			RequestID string `json:"request_id"`
			FundingID string `json:"funding_id"`
			UserID    int    `json:"user_id"`
			TokenID   int    `json:"token_id"`
			Status    int    `json:"http_status"`
			Attempts  int    `json:"attempts"`
		}
		require.NoError(t, common.Unmarshal(bytes.TrimSpace(data), &row))
		require.Equal(t, "cap-normal-2", row.Marker)
		require.Empty(t, row.RequestID)
		require.Empty(t, row.FundingID)
		require.Zero(t, row.Attempts)
		require.Equal(t, http.StatusForbidden, row.Status)
		require.NotContains(t, string(data), "private prompt")
	})
}
