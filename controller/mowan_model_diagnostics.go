package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	diagnostics "github.com/QuantumNous/new-api/internal/mowan/modeldiagnostics"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const diagnosticTaskType = "mowan_model_diagnostics"

type diagnosticStart struct {
	Model          string `json:"model"`
	Group          string `json:"group"`
	Kind           string `json:"kind"`
	Creative       bool   `json:"creative"`
	IdempotencyKey string `json:"idempotency_key"`
}

func diagnosticAccess(c *gin.Context, name, group string) (bool, error) {
	if name == "" || !utf8.ValidString(name) || len(name) > 255 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") || len(group) > 64 {
		return false, common.NewMessage("Invalid diagnostic request")
	}
	groups, chat, err := service.DiagnosticGroups(c, c.GetInt("id"), name)
	if err != nil || !slices.Contains(groups, group) {
		return false, common.NewMessage("Model or group is unavailable")
	}
	return chat, nil
}

func GetModelDiagnosticCatalog(c *gin.Context) {
	groups, chat, err := service.DiagnosticGroups(c, c.GetInt("id"), c.Query("model"))
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	common.ApiSuccess(c, gin.H{"groups": groups, "chat_supported": chat})
}

func StartModelDiagnostic(c *gin.Context) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	var input diagnosticStart
	if err != nil || len(data) > 4096 || common.Unmarshal(data, &input) != nil || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || !slices.Contains([]string{"connection", "fingerprint", "intelligence", "drawing"}, input.Kind) || input.Creative && input.Kind != "drawing" {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid diagnostic request"))
		return
	}
	chat, err := diagnosticAccess(c, input.Model, input.Group)
	if err != nil || !chat {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Only text models support diagnostics."))
		return
	}
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || c.GetBool("use_access_token") {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("A current login session is required"))
		return
	}
	plan, err := diagnostics.NewPlan(input.Kind, input.Creative)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Diagnostic reference data are unavailable"))
		return
	}
	planJSON, err := common.Marshal(plan)
	if err != nil || len(plan.Steps) == 0 {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	configHash, err := service.DiagnosticConfigHash(input.Model, input.Group)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	// Fingerprint challenges and creative material are random per run. Dedup uses
	// their fixed method/parameters, not a newly rolled prompt on every click.
	scope := service.DiagnosticHash([]any{input.Model, input.Group, input.Kind, input.Creative, plan.Version, configHash})
	if input.Creative {
		scope = service.DiagnosticHash([]any{scope, identity.UserID})
	}
	run := &model.DiagnosticRun{ID: uuid.NewString(), UserID: identity.UserID, SubmissionKey: service.DiagnosticHash(input.IdempotencyKey), SessionID: identity.SessionID, UserAuthVersion: identity.UserAuthVersion, SessionVersion: identity.SessionVersion,
		ModelName: input.Model, GroupName: input.Group, Kind: input.Kind, Creative: input.Creative, Version: plan.Version, ScopeKey: scope, ActiveKey: &scope, State: "queued", PlanJSON: string(planJSON), ConfigHash: configHash}
	steps := make([]model.DiagnosticStep, 0, len(plan.Steps))
	for i := range plan.Steps {
		id := uuid.NewString()
		steps = append(steps, model.DiagnosticStep{ID: id, RunID: run.ID, Ordinal: i, RequestID: id, FundingID: "billing:" + id, State: "queued"})
	}
	created, err := model.CreateDiagnosticRun(run, steps)
	if err != nil {
		if errors.Is(err, model.ErrDiagnosticSubmissionConflict) {
			common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Diagnostic submission key was already used for another request"))
			return
		}
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Unable to start diagnostics"))
		return
	}
	// The saved job survives a failed wake-up; the scheduled handler also scans it.
	if _, _, err := service.EnqueueSystemTask(diagnosticTaskType, nil); err != nil {
		common.SysError(common.LogText("diagnostic dispatcher wake-up failed: %s", err.Error()))
	}
	view, err := diagnosticView(c, created)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	common.ApiSuccess(c, view)
}

func ListModelDiagnosticReports(c *gin.Context) {
	name, group := c.Query("model"), c.Query("group")
	if _, err := diagnosticAccess(c, name, group); err != nil {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Model or group is unavailable"))
		return
	}
	runs := []model.DiagnosticRun{}
	if err := model.DB.Where("model_name = ? AND group_name = ? AND (creative = ? OR user_id = ?)", name, group, false, c.GetInt("id")).Order("created_at desc").Limit(40).Find(&runs).Error; err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	views := []gin.H{}
	for i := range runs {
		view, err := diagnosticView(c, &runs[i])
		if err != nil {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
			return
		}
		views = append(views, view)
	}
	common.ApiSuccess(c, views)
}

func GetModelDiagnosticReport(c *gin.Context) {
	run, err := model.GetDiagnosticRun(c.Param("id"))
	if err != nil || run.Creative && run.UserID != c.GetInt("id") {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Diagnostic report not found"))
		return
	}
	if _, err := diagnosticAccess(c, run.ModelName, run.GroupName); err != nil {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Model or group is unavailable"))
		return
	}
	view, err := diagnosticView(c, run)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("Model diagnostics are unavailable"))
		return
	}
	common.ApiSuccess(c, view)
}

func diagnosticView(c *gin.Context, run *model.DiagnosticRun) (gin.H, error) {
	var plan diagnostics.Plan
	if err := common.UnmarshalJsonStr(run.PlanJSON, &plan); err != nil {
		return nil, err
	}
	steps, err := model.GetDiagnosticSteps(run.ID)
	if err != nil {
		return nil, err
	}
	views := []gin.H{}
	fee := gin.H{"status": "settled"}
	if run.State == "review_pending" {
		fee["status"] = "pending"
	}
	var totalQuota int64
	for _, step := range steps {
		if step.Ordinal < 0 || step.Ordinal >= len(plan.Steps) {
			return nil, errors.New("invalid persisted diagnostic step")
		}
		probe := plan.Steps[step.Ordinal]
		stepState := step.State
		if slices.Contains([]string{"prepared", "funding", "sending"}, stepState) {
			stepState = "running"
		}
		views = append(views, gin.H{"id": step.ID, "title": probe.Title, "prompt": probe.Prompt, "state": stepState, "answer": step.Answer, "complete": step.Complete, "input_tokens": step.InputTokens, "output_tokens": step.OutputTokens, "reasoning_tokens": step.ReasoningTokens, "duration_ms": step.DurationMS, "error": step.ErrorCode})
		if step.State == "queued" {
			if run.State == "queued" || run.State == "running" {
				fee["status"] = "pending"
			}
			continue
		}
		if step.BillingState == "not_started" {
			continue
		}
		if step.BillingState != "free" {
			var funding model.RoutingFundingSession
			err := model.DB.First(&funding, "funding_id = ?", step.FundingID).Error
			if err != nil || funding.Status != "settled" && funding.Status != "refunded" {
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, err
				}
				fee["status"] = "pending"
			} else if funding.Status == "settled" {
				if funding.Quota < 0 || funding.Quota > common.MaxQuota {
					fee["status"] = "pending"
				} else {
					totalQuota += int64(funding.Quota)
				}
			}
		}
		// Existing policy may charge a violation fee even when generation refunded
		// or the model price was free. Read the authoritative operation, not logs.
		var extra model.RoutingFundingOperation
		err := model.DB.First(&extra, "operation_id = ? AND user_id = ?", service.ViolationFeeOperationID(step.RequestID), run.UserID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			if extra.Kind != "delta" || extra.Delta < 0 || extra.Delta > common.MaxQuota {
				fee["status"] = "pending"
			} else {
				totalQuota += int64(extra.Delta)
			}
		}
	}
	if fee["status"] == "settled" {
		fee["quota"] = totalQuota
	}
	configHash, err := service.DiagnosticConfigHash(run.ModelName, run.GroupName)
	if err != nil {
		return nil, err
	}
	stamp := run.CompletedAt
	if stamp == 0 {
		stamp = run.CreatedAt
	}
	stale := time.Now().Unix()-stamp >= 86400 || run.ConfigHash != configHash
	stale = stale || diagnostics.CurrentVersion(run.Kind) != run.Version
	view := gin.H{"id": run.ID, "model": run.ModelName, "group": run.GroupName, "kind": run.Kind, "creative": run.Creative, "state": run.State, "created_at": run.CreatedAt, "updated_at": run.UpdatedAt, "completed_at": run.CompletedAt, "stale": stale, "version": run.Version, "materials": plan.Materials, "steps": views, "owned": run.UserID == c.GetInt("id")}
	if run.ResultJSON != "" {
		var result diagnostics.Result
		if err := common.UnmarshalJsonStr(run.ResultJSON, &result); err != nil {
			return nil, err
		}
		view["result"] = result
	}
	if run.UserID == c.GetInt("id") {
		view["fee"] = fee
	}
	return view, nil
}

type diagnosticDispatcher struct{}

func (diagnosticDispatcher) Type() string            { return diagnosticTaskType }
func (diagnosticDispatcher) Enabled() bool           { return true }
func (diagnosticDispatcher) Interval() time.Duration { return 15 * time.Second }
func (diagnosticDispatcher) NewPayload() any         { return nil }
func (diagnosticDispatcher) Run(ctx context.Context, task *model.SystemTask, runner string) {
	var runs []model.DiagnosticRun
	err := model.DB.WithContext(ctx).Where("state = ? OR (state = ? AND lease_until < ?)", "queued", "running", time.Now().Unix()).Find(&runs).Error
	if err == nil {
		for _, row := range runs {
			if ctx.Err() != nil {
				break
			}
			owner := uuid.NewString()
			claimed, claimErr := model.ClaimDiagnosticRun(row.ID, owner, time.Now().Unix())
			if claimErr != nil || claimed.State != "running" {
				continue
			}
			// Each worker owns a durable lease independent of the short dispatcher.
			// It rechecks ownership before every transport send, not just on launch.
			go runModelDiagnostic(claimed, owner)
		}
	}
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, "diagnostic_dispatch_failed"
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runner, status, nil, message); finishErr != nil {
		common.SysError(common.LogText("diagnostic dispatcher completion failed: %s", finishErr.Error()))
	}
}
