package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
)

// PrepareRoutingSubmission must run after pricing, before the first pre-consume.
// The session persists this metadata together with the reservation, including
// zero-price attempts. Synchronous transports do not use the task journal.
func PrepareRoutingSubmission(c *gin.Context, info *relaycommon.RelayInfo) error {
	return prepareRoutingSubmission(c, info, false)
}

func prepareRoutingSubmission(c *gin.Context, info *relaycommon.RelayInfo, includeLegacy bool) error {
	if (!IsSmartRouting(c) && !includeLegacy) || info == nil || !info.ForcePreConsume {
		return nil
	}
	if _, err := PrepareRoutingProbeClaims(c); err != nil {
		return err
	}
	requestID, attempt, _ := RoutingAttemptIdentity(c)
	if includeLegacy && requestID == "" {
		requestID, attempt = common.GetRandomString(32), 1
	}
	if requestID == "" || attempt < 1 || info.ChannelMeta == nil {
		return errors.New("routing_submission_identity_unavailable")
	}
	channel, err := model.GetChannelById(info.ChannelId, false)
	if err != nil {
		return err
	}
	key, err := RoutingObservationKey(c, info.OriginModelName, info.UsingGroup, channel, "async")
	if err != nil {
		return err
	}
	price, err := common.Marshal(struct {
		Price  any `json:"price"`
		Tiered any `json:"tiered,omitempty"`
	}{info.PriceData, info.TieredBillingSnapshot})
	if err != nil {
		return err
	}
	s := &model.RoutingSubmission{SubmissionID: fmt.Sprintf("%s:%d", requestID, attempt), RequestID: requestID, AttemptNo: attempt, ClientModel: key.ClientModel, BusinessGroup: key.BusinessGroup, ProtocolClass: key.ProtocolClass, ProviderKey: key.ProviderKey, OperationKey: key.OperationKey, ChannelID: channel.Id, PriceSnapshot: string(price)}
	if value, ok := c.Get("mowan_routing_probes"); ok {
		if claims, ok := value.([]model.RoutingProbeClaim); ok {
			for _, claim := range claims {
				if claim.Stage == model.RoutingStageTaskTerminal {
					s.TerminalProbeEpoch, s.TerminalProbeLeaseID = claim.Epoch, claim.LeaseID
				}
			}
		}
	}
	c.Set("mowan_routing_submission", s)
	return nil
}

// ResolveRoutingSubmissionForAdmin never accepts client-supplied Task/private
// state and never invokes a provider create. Durable server recovery material is
// sufficient to repair a failed local insert. Providers without a safe query
// adapter leave identity-unknown submissions reserved for manual verification.
func ResolveRoutingSubmissionForAdmin(c *gin.Context, row *model.RoutingSubmission, req model.RoutingSubmissionResolution) (*model.RoutingSubmission, error) {
	if row == nil {
		return nil, errors.New("routing_submission_unavailable")
	}
	stored, err := model.GetRoutingSubmission(row.SubmissionID)
	if err != nil {
		return nil, err
	}
	if req.Outcome == "accepted" {
		if stored.RecoveryJSON == "" || stored.UpstreamTaskID == "" {
			return nil, errors.New("routing_provider_reconciliation_query_unavailable: safe server recovery material required; reservation retained")
		}
		if req.UpstreamTaskID != "" && req.UpstreamTaskID != stored.UpstreamTaskID {
			return nil, errors.New("routing_reconciliation_identity_mismatch")
		}
		var recovery model.RoutingTaskRecovery
		if err := common.UnmarshalJsonStr(stored.RecoveryJSON, &recovery); err != nil {
			return nil, err
		}
		req.Recovery, req.UpstreamTaskID = &recovery, stored.UpstreamTaskID
	}
	resolved, err := model.ResolveRoutingSubmission(stored.SubmissionID, req)
	if err != nil {
		return nil, err
	}
	if req.Outcome == "accepted" {
		if req.Recovery.Midjourney != nil {
			if _, err := model.PersistAcceptedRoutingMidjourney(resolved.SubmissionID); err != nil {
				return resolved, err
			}
		} else if _, err := model.PersistAcceptedRoutingTask(resolved.SubmissionID); err != nil {
			return resolved, err
		}
		return model.GetRoutingSubmission(resolved.SubmissionID)
	}
	return resolved, nil
}

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
// 会话已存在时（任务重试换到另一个渠道）把预扣补到 preConsumedQuota，
// 和首次预扣一样必须由余额覆盖。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(
			relayInfo.QuotaClamp,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if relayInfo != nil && relayInfo.Billing != nil {
		if value, ok := c.Get("mowan_routing_submission"); ok {
			if submission, ok := value.(*model.RoutingSubmission); ok {
				session, valid := relayInfo.Billing.(*BillingSession)
				if !valid {
					return types.NewError(errors.New("routing billing session unavailable"), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
				}
				submission.Quota = preConsumedQuota
				if err := session.PrepareSubmission(submission); err != nil {
					return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
				}
				return nil
			}
		}
		if err := relayInfo.Billing.ReserveBeforeSubmit(preConsumedQuota); err != nil {
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return apiErr
			}
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry())
		}
		return nil
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 无预扣的旧入口建立零额会话，保留同事务结算；已预扣却无身份则拒绝猜测。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo == nil {
		return errors.New("billing relay info unavailable")
	}
	if relayInfo.Billing == nil && (actualQuota != 0 || relayInfo.FinalPreConsumedQuota != 0) {
		if ctx == nil || relayInfo.FinalPreConsumedQuota != 0 {
			return errors.New("durable pre-consumed funding identity unavailable")
		}
		session, err := NewBillingSession(ctx, relayInfo, 0)
		if err != nil {
			return err
		}
		relayInfo.Billing = session
	}
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if delta > 0 {
			logger.LogInfo(ctx, common.LogText("charging the difference after pre-consume: %s (actual %s, pre-consumed %s)",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, common.LogText("refunding the difference after pre-consume: %s (actual %s, pre-consumed %s)",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, common.LogText("pre-consumed quota matches the actual quota, no adjustment: %s (per-request billing)",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			return err
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
			} else {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	return nil
}
