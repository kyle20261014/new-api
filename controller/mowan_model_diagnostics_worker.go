package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	diagnostics "github.com/QuantumNous/new-api/internal/mowan/modeldiagnostics"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func runModelDiagnostic(run *model.DiagnosticRun, owner string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(common.LogText("diagnostic worker interrupted: %v", recovered))
			_ = model.FinishDiagnosticRun(run.ID, owner, "review_pending", "")
		}
	}()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !model.RenewDiagnosticLease(run.ID, owner) {
					cancel()
					return
				}
			}
		}
	}()
	var plan diagnostics.Plan
	if common.UnmarshalJsonStr(run.PlanJSON, &plan) != nil {
		_ = model.FinishDiagnosticRun(run.ID, owner, "failed", "")
		return
	}
	steps, err := model.GetDiagnosticSteps(run.ID)
	if err != nil {
		return // Expired lease recovery will safely inspect persisted step states.
	}
	unauthorized := false
	for _, step := range steps {
		if ctx.Err() != nil {
			return
		}
		if step.State == "completed" || step.State == "failed" {
			continue
		}
		if step.State != "queued" && step.State != "prepared" || step.Ordinal < 0 || step.Ordinal >= len(plan.Steps) {
			_ = model.FinishDiagnosticRun(run.ID, owner, "review_pending", "")
			return
		}
		if err := model.UpdateDiagnosticStep(run.ID, owner, step.ID, map[string]any{"state": "prepared"}); err != nil {
			return
		}
		// The step's RequestID/FundingID were saved with creation, before any
		// reservation; normal routing and BillingSession use that same identity.
		values, revoked := executeDiagnosticStep(ctx, run, &step, plan.Steps[step.Ordinal], owner)
		if err := model.UpdateDiagnosticStep(run.ID, owner, step.ID, values); err != nil {
			return
		}
		if revoked {
			unauthorized = true
			break
		}
	}
	steps, err = model.GetDiagnosticSteps(run.ID)
	if err != nil {
		return
	}
	answers := make([]diagnostics.Answer, 0, len(steps))
	completed := 0
	for _, step := range steps {
		if step.Ordinal < 0 || step.Ordinal >= len(plan.Steps) {
			return
		}
		probe := plan.Steps[step.Ordinal]
		answers = append(answers, diagnostics.Answer{ID: probe.ID, Text: step.Answer, Complete: step.Complete, Source: step.SourceHash, ExpectedCount: probe.ExpectedCount})
		if step.Complete {
			completed++
		}
	}
	result := diagnostics.Evaluate(run.Kind, answers)
	resultJSON, err := common.Marshal(result)
	if err != nil {
		return
	}
	state := "completed"
	if !result.Complete {
		state = "partial"
	}
	if completed == 0 {
		state = "failed"
	}
	if unauthorized {
		state = "unauthorized"
	}
	if err := model.FinishDiagnosticRun(run.ID, owner, state, string(resultJSON)); err != nil {
		common.SysError(common.LogText("diagnostic completion pending: %s", err.Error()))
	}
}

// A bounded in-process response avoids loopback HTTP, raw credentials and
// retaining a browser request/Gin context across the asynchronous task.
type diagnosticResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *diagnosticResponse) Header() http.Header { return w.header }
func (w *diagnosticResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *diagnosticResponse) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.body.Len()+len(data) > 2<<20 {
		w.overflow = true
		return 0, errors.New("diagnostic response too large")
	}
	return w.body.Write(data)
}

// Flush satisfies http.Flusher for Gin's response writer wrapper. The
// diagnostic relay keeps the response in bounded memory, so flushing has no
// transport to write to.
func (w *diagnosticResponse) Flush() {}

// newDiagnosticChatRequest keeps the internal relay request on a relative path:
// the relay copies c.Request.URL.String() into the upstream URL, so an absolute
// internal URL would be appended to the channel base URL.
func newDiagnosticChatRequest(ctx context.Context, body []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.RequestURI = "/v1/chat/completions"
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "127.0.0.1:0"
	return request, nil
}

func executeDiagnosticStep(ctx context.Context, run *model.DiagnosticRun, step *model.DiagnosticStep, probe diagnostics.Probe, owner string) (map[string]any, bool) {
	started := time.Now()
	values := map[string]any{"state": "failed", "complete": false, "error_code": "diagnostic_request_failed"}
	body, err := common.Marshal(gin.H{"model": run.ModelName, "stream": false, "messages": []gin.H{{"role": "user", "content": probe.Prompt}}, "max_tokens": probe.MaxTokens})
	if err != nil {
		return values, false
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request, err := newDiagnosticChatRequest(callCtx, body)
	if err != nil {
		return values, false
	}
	response := &diagnosticResponse{header: http.Header{}}
	engine := gin.New()
	revoked := false
	free := false
	relayEntered := false
	engine.POST("/v1/chat/completions", middleware.BodyStorageCleanup(), func(c *gin.Context) {
		if err := service.PrepareDiagnosticRelay(c, run, step, owner); err != nil {
			revoked = true
			// Only a never-started step is known to have no previous reservation.
			// A reclaimed prepared step may already own funds: keep it pending.
			if step.State == "queued" {
				values["billing_state"] = "not_started"
			}
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		if err := middleware.SetupContextForToken(c, &model.Token{UserId: run.UserID, Name: "model-diagnostics", Group: run.GroupName, Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1}); err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		// Persist before entering any reserve/refund path. An expired worker here
		// must never race a replacement BillingSession for the same funding ID.
		if err := model.UpdateDiagnosticStep(run.ID, owner, step.ID, map[string]any{"state": "funding"}); err != nil {
			c.AbortWithStatus(http.StatusConflict)
			return
		}
		c.Next()
		free = service.DiagnosticWasFree(c)
	}, middleware.SystemPerformanceCheck(), middleware.RoutingModelRequestRateLimit(), middleware.Distribute(), func(c *gin.Context) {
		relayEntered = true
		Relay(c, types.RelayFormatOpenAI)
	})
	engine.ServeHTTP(response, request)
	values["duration_ms"] = time.Since(started).Milliseconds()
	if !relayEntered && step.State == "queued" {
		// This first execution ended in middleware, before any billing call.
		values["billing_state"] = "not_started"
	}
	if free {
		values["billing_state"] = "free"
	}
	if revoked {
		values["error_code"] = "diagnostic_permission_expired"
		return values, true
	}
	if response.status >= 400 || response.overflow || callCtx.Err() != nil {
		return values, false
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens      *int `json:"prompt_tokens"`
			CompletionTokens  *int `json:"completion_tokens"`
			CompletionDetails *struct {
				ReasoningTokens *int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if common.Unmarshal(response.body.Bytes(), &payload) != nil || len(payload.Choices) == 0 {
		return values, false
	}
	choice := payload.Choices[0]
	values["answer"] = choice.Message.Content
	if choice.FinishReason == "stop" && choice.Message.Content != "" {
		values["state"], values["complete"], values["error_code"] = "completed", true, ""
	} else {
		values["error_code"] = "diagnostic_incomplete_response"
	}
	if payload.Usage != nil {
		values["input_tokens"], values["output_tokens"] = payload.Usage.PromptTokens, payload.Usage.CompletionTokens
		if payload.Usage.CompletionDetails != nil {
			values["reasoning_tokens"] = payload.Usage.CompletionDetails.ReasoningTokens
		}
	}
	return values, false
}

var _ io.Writer = (*diagnosticResponse)(nil)
