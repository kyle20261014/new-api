package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

func CovertMjpActionToModelName(mjAction string) string {
	modelName := "mj_" + strings.ToLower(mjAction)
	if mjAction == constant.MjActionSwapFace {
		modelName = "swap_face"
	}
	return modelName
}

// PrepareMidjourneySubmissionBilling reserves both balances before every create,
// including no-consumption actions. Midjourney retains its wallet-only product.
func PrepareMidjourneySubmissionBilling(c *gin.Context, info *relaycommon.RelayInfo, quota int) error {
	if info == nil {
		return errors.New("relay info is nil")
	}
	if info.BillingSource == BillingSourceSubscription || common.NormalizeBillingPreference(info.UserSetting.BillingPreference) == "subscription_only" {
		return errors.New("legacy Midjourney billing does not support subscriptions")
	}
	info.ForcePreConsume = true
	if _, err := PrepareRoutingProbeClaims(c); err != nil {
		return err
	}
	if err := prepareRoutingSubmission(c, info, true); err != nil {
		return err
	}
	pref := info.UserSetting.BillingPreference
	info.UserSetting.BillingPreference = "wallet_only"
	apiErr := PreConsumeBilling(c, quota, info)
	info.UserSetting.BillingPreference = pref
	if apiErr != nil {
		ReleaseRoutingUnusedProbes(c)
		return apiErr
	}
	return nil
}

// FailMidjourneySubmission distinguishes a proven pre-send failure from a
// transport ambiguity after the durable sending claim.
// Only recovery identity/state is public; transport and DB causes may contain private data.
func FailMidjourneySubmission(c *gin.Context, info *relaycommon.RelayInfo, _ error) error {
	s := CurrentRoutingSubmission(c)
	if s == nil {
		return errors.New("Midjourney submission failed")
	}
	state := "unknown"
	if s.State == "prepared" {
		state = "not_sent"
	}
	if s.State == "prepared" || s.State == "sending" {
		updated, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, state, "", nil, s.SendOwnerID)
		if err != nil {
			return fmt.Errorf("submission_id=%s state=%s outcome_save_pending", s.SubmissionID, state)
		}
		c.Set("mowan_routing_submission", updated)
	}
	if state == "not_sent" {
		info.Billing.Refund(c)
	} else {
		MarkRoutingBarrier(c, "submission_unknown")
	}
	return fmt.Errorf("submission_id=%s state=%s", s.SubmissionID, state)
}

func PersistMidjourneySubmission(c *gin.Context, info *relaycommon.RelayInfo, task *model.Midjourney, accepted bool) (bool, error) {
	s := CurrentRoutingSubmission(c)
	if s == nil || task == nil {
		return false, errors.New("Midjourney submission unavailable")
	}
	if !accepted {
		if task.Code == 0 || task.Code == 1 || task.Code == 21 || task.Code == 22 {
			return false, FailMidjourneySubmission(c, info, errors.New("Midjourney submission outcome ambiguous"))
		}
		updated, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "rejected", "", nil, s.SendOwnerID)
		if err != nil {
			return false, fmt.Errorf("submission_id=%s state=rejected outcome_save_pending", s.SubmissionID)
		}
		c.Set("mowan_routing_submission", updated)
		info.Billing.Refund(c)
		task.Quota, task.TokenId, task.BillingChannelId = 0, 0, 0
		if err := task.Insert(); err != nil {
			return false, fmt.Errorf("submission_id=%s state=rejected local_save_pending", s.SubmissionID)
		}
		return false, nil
	}
	if task.MjId == "" {
		return false, FailMidjourneySubmission(c, info, errors.New("accepted Midjourney identity missing"))
	}
	safe := *task
	safe.Id = 0
	safe.Prompt, safe.PromptEn, safe.Description, safe.State, safe.Properties, safe.Buttons = "", "", "", "", "", ""
	updated, err := model.RecordRoutingSubmissionOutcome(s.SubmissionID, s.Version, "accepted", task.MjId, &model.RoutingTaskRecovery{Midjourney: &safe}, s.SendOwnerID)
	if err != nil {
		return false, FailMidjourneySubmission(c, info, err)
	}
	c.Set("mowan_routing_submission", updated)
	persisted, err := model.PersistAcceptedRoutingMidjourney(s.SubmissionID)
	if err != nil {
		return false, fmt.Errorf("submission_id=%s state=accepted local_save_pending", s.SubmissionID)
	}
	persisted.Prompt, persisted.PromptEn, persisted.Description = task.Prompt, task.PromptEn, task.Description
	persisted.State, persisted.Properties, persisted.Buttons = task.State, task.Properties, task.Buttons
	if err := model.DB.Model(persisted).Select("prompt", "prompt_en", "description", "state", "properties", "buttons").Updates(persisted).Error; err != nil {
		return false, fmt.Errorf("submission_id=%s state=accepted local_save_pending", s.SubmissionID)
	}
	*task = *persisted
	if err := info.Billing.Settle(task.Quota); err != nil {
		return false, fmt.Errorf("submission_id=%s state=accepted funding_pending", s.SubmissionID)
	}
	if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		if _, err := model.FinalizeMidjourneyFunding(task, task.Status == model.TaskStatusFailure); err != nil {
			return false, fmt.Errorf("submission_id=%s state=accepted terminal_funding_pending", s.SubmissionID)
		}
	}
	return task.Quota > 0, nil
}

// PrepareMidjourneyTaskBilling sets the durable refund marker before the task is inserted.
func PrepareMidjourneyTaskBilling(relayInfo *relaycommon.RelayInfo, task *model.Midjourney, quota int, shouldBill bool) (bool, error) {
	if task == nil {
		return false, errors.New("Midjourney task is nil")
	}
	task.Quota = 0
	task.TokenId = 0
	task.BillingChannelId = 0
	if !shouldBill {
		return false, nil
	}
	if relayInfo == nil {
		return false, errors.New("relay info is nil")
	}
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if relayInfo.BillingSource == BillingSourceSubscription {
		return false, errors.New("legacy Midjourney billing does not support subscriptions")
	}

	task.Quota = quota
	task.BillingChannelId = task.ChannelId
	if relayInfo.ChannelMeta != nil && relayInfo.ChannelId > 0 {
		task.BillingChannelId = relayInfo.ChannelId
	}
	return true, nil
}

// SettleMidjourneyTaskBilling charges a persisted legacy task and records the applied stages.
func SettleMidjourneyTaskBilling(relayInfo *relaycommon.RelayInfo, task *model.Midjourney, prepared bool) (bool, error) {
	if !prepared {
		return false, nil
	}
	if relayInfo == nil {
		return false, errors.New("relay info is nil")
	}
	if task == nil || task.Id == 0 {
		return false, errors.New("Midjourney task must be persisted before billing")
	}

	result, billingErr := postConsumeQuotaWithResult(relayInfo, task.Quota, 0, true)
	if !result.FundingApplied {
		task.Quota = 0
		task.TokenId = 0
		task.BillingChannelId = 0
		if updateErr := task.UpdateBillingState(); updateErr != nil {
			return false, errors.Join(billingErr, fmt.Errorf("clear Midjourney billing state: %w", updateErr))
		}
		return false, billingErr
	}

	task.TokenId = 0
	if result.TokenApplied {
		task.TokenId = relayInfo.TokenId
	}
	if updateErr := task.UpdateBillingState(); updateErr != nil {
		return true, errors.Join(billingErr, fmt.Errorf("update Midjourney billing state: %w", updateErr))
	}
	return true, billingErr
}

// RefundMidjourneyQuota reverses every accounting element recorded for a billed legacy task.
func RefundMidjourneyQuota(ctx context.Context, task *model.Midjourney, reason string) bool {
	quota := task.Quota
	if quota == 0 {
		return true
	}

	op, err := model.FinalizeMidjourneyFunding(task, true)
	if err != nil {
		logger.LogWarn(ctx, common.LogText("failed to refund Midjourney user quota, task %s: %s", task.MjId, err.Error()))
		return false
	}
	if op.Replay {
		return true
	}
	quota = -op.Delta

	billingChannelId := task.GetBillingChannelId()
	model.UpdateUserUsedQuota(task.UserId, -quota)
	model.UpdateChannelUsedQuota(billingChannelId, -quota)
	other := model.NewLogOther()
	other.SetPublic("task_id", task.MjId)
	other.SetPublic("reason", reason)
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		ChannelId: billingChannelId,
		ModelName: CovertMjpActionToModelName(task.Action),
		Quota:     quota,
		TokenId:   task.TokenId,
		Other:     other,
	})

	return true
}

func GetMjRequestModel(relayMode int, midjRequest *dto.MidjourneyRequest) (string, *dto.MidjourneyResponse, bool) {
	action := ""
	if relayMode == relayconstant.RelayModeMidjourneyAction {
		// plus request
		err := CoverPlusActionToNormalAction(midjRequest)
		if err != nil {
			return "", err, false
		}
		action = midjRequest.Action
	} else {
		switch relayMode {
		case relayconstant.RelayModeMidjourneyImagine:
			action = constant.MjActionImagine
		case relayconstant.RelayModeMidjourneyVideo:
			action = constant.MjActionVideo
		case relayconstant.RelayModeMidjourneyEdits:
			action = constant.MjActionEdits
		case relayconstant.RelayModeMidjourneyDescribe:
			action = constant.MjActionDescribe
		case relayconstant.RelayModeMidjourneyBlend:
			action = constant.MjActionBlend
		case relayconstant.RelayModeMidjourneyShorten:
			action = constant.MjActionShorten
		case relayconstant.RelayModeMidjourneyChange:
			action = midjRequest.Action
		case relayconstant.RelayModeMidjourneyModal:
			action = constant.MjActionModal
		case relayconstant.RelayModeSwapFace:
			action = constant.MjActionSwapFace
		case relayconstant.RelayModeMidjourneyUpload:
			action = constant.MjActionUpload
		case relayconstant.RelayModeMidjourneySimpleChange:
			params := ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return "", MidjourneyErrorWrapper(constant.MjRequestError, "invalid_request"), false
			}
			action = params.Action
		case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition, relayconstant.RelayModeMidjourneyNotify:
			return "", nil, true
		default:
			return "", MidjourneyErrorWrapper(constant.MjRequestError, "unknown_relay_action"), false
		}
	}
	modelName := CovertMjpActionToModelName(action)
	return modelName, nil, true
}

func CoverPlusActionToNormalAction(midjRequest *dto.MidjourneyRequest) *dto.MidjourneyResponse {
	// "customId": "MJ::JOB::upsample::2::3dbbd469-36af-4a0f-8f02-df6c579e7011"
	customId := midjRequest.CustomId
	if customId == "" {
		return MidjourneyErrorWrapper(constant.MjRequestError, "custom_id_is_required")
	}
	splits := strings.Split(customId, "::")
	var action string
	if splits[1] == "JOB" {
		action = splits[2]
	} else {
		action = splits[1]
	}

	if action == "" {
		return MidjourneyErrorWrapper(constant.MjRequestError, "unknown_action")
	}
	if strings.Contains(action, "upsample") {
		index, err := strconv.Atoi(splits[3])
		if err != nil {
			return MidjourneyErrorWrapper(constant.MjRequestError, "index_parse_failed")
		}
		midjRequest.Index = index
		midjRequest.Action = constant.MjActionUpscale
	} else if strings.Contains(action, "variation") {
		midjRequest.Index = 1
		if action == "variation" {
			index, err := strconv.Atoi(splits[3])
			if err != nil {
				return MidjourneyErrorWrapper(constant.MjRequestError, "index_parse_failed")
			}
			midjRequest.Index = index
			midjRequest.Action = constant.MjActionVariation
		} else if action == "low_variation" {
			midjRequest.Action = constant.MjActionLowVariation
		} else if action == "high_variation" {
			midjRequest.Action = constant.MjActionHighVariation
		}
	} else if strings.Contains(action, "pan") {
		midjRequest.Action = constant.MjActionPan
		midjRequest.Index = 1
	} else if strings.Contains(action, "reroll") {
		midjRequest.Action = constant.MjActionReRoll
		midjRequest.Index = 1
	} else if action == "Outpaint" {
		midjRequest.Action = constant.MjActionZoom
		midjRequest.Index = 1
	} else if action == "CustomZoom" {
		midjRequest.Action = constant.MjActionCustomZoom
		midjRequest.Index = 1
	} else if action == "Inpaint" {
		midjRequest.Action = constant.MjActionInPaint
		midjRequest.Index = 1
	} else {
		return MidjourneyErrorWrapper(constant.MjRequestError, "unknown_action:"+customId)
	}
	return nil
}

func ConvertSimpleChangeParams(content string) *dto.MidjourneyRequest {
	split := strings.Split(content, " ")
	if len(split) != 2 {
		return nil
	}

	action := strings.ToLower(split[1])
	changeParams := &dto.MidjourneyRequest{}
	changeParams.TaskId = split[0]

	if action[0] == 'u' {
		changeParams.Action = "UPSCALE"
	} else if action[0] == 'v' {
		changeParams.Action = "VARIATION"
	} else if action == "r" {
		changeParams.Action = "REROLL"
		return changeParams
	} else {
		return nil
	}

	index, err := strconv.Atoi(action[1:2])
	if err != nil || index < 1 || index > 4 {
		return nil
	}
	changeParams.Index = index
	return changeParams
}

// RecordMidjourneyPolicyResponse distinguishes accepted tasks from errors inside
// HTTP 200 responses. Submissions are single-attempt: an ambiguous transport
// failure must never create a duplicate task on another channel.
func RecordMidjourneyPolicyResponse(c *gin.Context, response *dto.MidjourneyResponseWithStatusCode, requestErr error) bool {
	responseKnown := response != nil && requestErr == nil
	if response == nil {
		response = MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "empty_response", http.StatusBadGateway)
	}
	upstreamStatus := response.StatusCode
	var fact *routingUpstreamResponseError
	if errors.As(requestErr, &fact) {
		upstreamStatus, responseKnown = fact.statusCode, true
	}
	accepted := requestErr == nil && response.StatusCode == http.StatusOK && (response.Response.Code == 1 || response.Response.Code == 21 || response.Response.Code == 22)
	observation := RoutingAttemptResult{Outcome: "ignored", RateLimited: responseKnown && upstreamStatus == http.StatusTooManyRequests}
	clientCanceled := errors.Is(requestErr, context.Canceled) || (c.Request != nil && c.Request.Context().Err() != nil)
	if accepted {
		MarkRoutingBarrier(c, "submission_accepted")
		observation.Outcome = "success"
	} else if (responseKnown && (upstreamStatus >= 500 || observation.RateLimited)) || (requestErr != nil && !clientCanceled) {
		observation.Outcome = "failure"
	}
	if err := FinishRoutingAttempt(c, observation); err != nil {
		logger.LogWarn(c, common.LogText("Midjourney submit observation pending: %s", err.Error()))
	}
	if accepted {
		properties, _ := response.Response.Properties.(map[string]any)
		if properties["status"] != "FAILURE" {
			return true
		}
	}
	state := RequestPolicy(c)
	event := PolicyEvent{ChannelID: c.GetInt("channel_id"), Status: response.StatusCode, ErrorCode: strconv.Itoa(response.Response.Code), ErrorSource: "upstream", Decision: PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}}
	state.AddEvent(event)
	event.Decision, event.Health = PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}, "unchanged"
	if accepted {
		event.Decision.Reason = "task_accepted"
	}
	state.AddEvent(event)
	return false
}

func DoMidjourneyHttpRequest(c *gin.Context, timeout time.Duration, fullRequestURL string) (response *dto.MidjourneyResponseWithStatusCode, body []byte, requestErr error) {
	var nullBytes []byte
	//var requestBody io.Reader
	//requestBody = c.Request.Body
	// read request body to json, delete accountFilter and notifyHook
	var mapResult map[string]any
	// if get request, no need to read request body
	if c.Request.Method != "GET" {
		err := common.DecodeJson(c.Request.Body, &mapResult)
		if err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "read_request_body_failed", http.StatusInternalServerError), nullBytes, err
		}
		if !setting.MjAccountFilterEnabled {
			delete(mapResult, "accountFilter")
		}
		if !setting.MjNotifyEnabled {
			delete(mapResult, "notifyHook")
		}
		//req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
		// make new request with mapResult
	}
	if setting.MjModeClearEnabled {
		if prompt, ok := mapResult["prompt"].(string); ok {
			prompt = strings.Replace(prompt, "--fast", "", -1)
			prompt = strings.Replace(prompt, "--relax", "", -1)
			prompt = strings.Replace(prompt, "--turbo", "", -1)

			mapResult["prompt"] = prompt
		}
	}
	reqBody, err := common.Marshal(mapResult)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "marshal_request_body_failed", http.StatusInternalServerError), nullBytes, err
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "create_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// 使用带有超时的 context 创建新的请求
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))
	auth := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	if auth != "" {
		auth = strings.TrimPrefix(auth, "Bearer ")
		req.Header.Set("mj-api-secret", auth)
	}
	defer cancel()
	if c.Request.Method != http.MethodGet {
		if err := BeginRoutingTransportSend(c); err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "routing_before_send_failed", http.StatusForbidden), nullBytes, err
		}
		if err := StartRoutingAttempt(c, "async"); err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "routing_observation_start_failed", http.StatusServiceUnavailable), nullBytes, err
		}
		if !IsSmartRouting(c) {
			if submission := CurrentRoutingSubmission(c); submission != nil {
				updated, err := model.MarkRoutingSubmissionSending(submission.SubmissionID, submission.Version, submission.SendOwnerID)
				if err != nil {
					return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "routing_before_send_failed", http.StatusForbidden), nullBytes, err
				}
				c.Set("mowan_routing_submission", updated)
			}
		}
	}
	resp, err := GetHttpClient().Do(req)
	if err != nil {
		common.SysLog(common.LogText("do request failed: %s", err.Error()))
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "do_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	statusCode := resp.StatusCode
	defer func() {
		if requestErr != nil {
			requestErr = &routingUpstreamResponseError{error: requestErr, statusCode: statusCode}
		}
	}()
	//if statusCode != 200  {
	//	return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "bad_response_status_code", statusCode), nullBytes, nil
	//}
	err = req.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	err = c.Request.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	var midjResponse dto.MidjourneyResponse
	var midjourneyUploadsResponse dto.MidjourneyUploadResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "read_response_body_failed", statusCode), nullBytes, err
	}
	CloseResponseBodyGracefully(resp)
	logger.LogDebug(c, "midjourney response body: %s", responseBody)
	if len(responseBody) == 0 {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "empty_response_body", statusCode), responseBody, nil
	} else {
		err = common.Unmarshal(responseBody, &midjResponse)
		if err != nil {
			err2 := common.Unmarshal(responseBody, &midjourneyUploadsResponse)
			if err2 != nil {
				return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "unmarshal_response_body_failed", statusCode), responseBody, err
			}
		}
	}
	//for k, v := range resp.Header {
	//	c.Writer.Header().Set(k, v[0])
	//}
	return &dto.MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   midjResponse,
	}, responseBody, nil
}
