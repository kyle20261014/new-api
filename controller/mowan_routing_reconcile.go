package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Recovery objects and the actor are server-owned. The request records evidence
// of an already-happened result; it can never supply a new task to create.
func ReconcileRoutingSubmission(c *gin.Context) {
	params := model.AuditFields{}
	defer func() {
		model.RecordAuditLog(c, model.AuditLog{
			UserId: c.GetInt("id"), Username: c.GetString("username"), ActorRole: c.GetInt("role"),
			Category: model.AuditCategorySecurity, Action: "routing.submission_reconcile",
			Content: "Routing submission reconciliation", Success: c.Writer.Status() < 400,
			Other: model.AuditOther{Op: &model.AuditOperation{Action: "routing.submission_reconcile", Params: params}},
		})
	}()
	id := c.Param("submission_id")
	if id == "" || len(id) > 128 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid submission ID"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 16385))
	if err != nil || len(data) > 16384 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation request"))
		return
	}
	var fields map[string]common.RawMessage
	if common.Unmarshal(data, &fields) != nil || len(fields) < 4 || len(fields) > 5 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation request"))
		return
	}
	for field, value := range fields {
		if (field != "expected_version" && field != "reason" && field != "outcome" && field != "proof" && field != "upstream_task_id") || strings.TrimSpace(string(value)) == "null" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation request"))
			return
		}
	}
	var proof map[string]common.RawMessage
	if common.Unmarshal(fields["proof"], &proof) != nil || len(proof) != 8 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation proof"))
		return
	}
	for _, key := range []string{"request_id", "channel_id", "provider_key", "operation_key", "outcome", "observed_at", "evidence_ref", "confirmed"} {
		if proof[key] == nil || strings.TrimSpace(string(proof[key])) == "null" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation proof"))
			return
		}
	}
	var request model.RoutingSubmissionResolution
	if common.Unmarshal(data, &request) != nil || request.ExpectedVersion < 1 || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 4096 || !request.Proof.Confirmed || request.Proof.ObservedAt <= 0 || request.Proof.ObservedAt > common.GetTimestamp()+60 || strings.TrimSpace(request.Proof.EvidenceRef) == "" || len(request.Proof.EvidenceRef) > 4096 || request.Proof.Outcome != request.Outcome || (request.Outcome != "not_sent" && request.Outcome != "rejected" && request.Outcome != "accepted") || len(request.UpstreamTaskID) > 191 || (request.Outcome != "accepted" && request.UpstreamTaskID != "") {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid reconciliation proof"))
		return
	}
	row, err := model.GetRoutingSubmission(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Submission not found"))
		return
	}
	if err != nil {
		routingTaskReadError(c, err)
		return
	}
	if request.Proof.RequestID != row.RequestID || request.Proof.ChannelID != row.ChannelID || request.Proof.ProviderKey != row.ProviderKey || request.Proof.OperationKey != row.OperationKey || request.Proof.ObservedAt < row.CreatedAt {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Reconciliation proof does not match the submission"))
		return
	}
	request.Actor = c.GetInt("id")
	params["submission_id"], params["version_from"], params["outcome"] = id, request.ExpectedVersion, request.Outcome
	resolved, err := service.ResolveRoutingSubmissionForAdmin(c, row, request)
	if errors.Is(err, model.ErrRoutingSubmissionConflict) || errors.Is(err, model.ErrRoutingFundingConflict) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Submission changed; reload before reconciliation"))
		return
	}
	if err != nil && strings.HasPrefix(err.Error(), "routing_provider_reconciliation_query_unavailable") {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Safe provider recovery evidence is unavailable; reservation retained"))
		return
	}
	if err != nil {
		routingTaskReadError(c, err)
		return
	}
	params["version_to"] = resolved.Version
	common.ApiSuccess(c, gin.H{"submission_id": resolved.SubmissionID, "submission_status": resolved.State, "version": resolved.Version})
}
