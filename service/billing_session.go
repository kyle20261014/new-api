package service

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int
	trusted          bool // Legacy initial holds remain zero through ordinary group repricing.
	settled          bool // Settle 全部完成（资金 + 令牌）
	refunded         bool // Refund 已调用
	mu               sync.Mutex
	fundingID        string
	submissionID     string
}

// Settle 根据实际消耗额度进行结算。
// 资金来源、令牌和操作凭据在一个主数据库事务内提交。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.fundingID != "" {
		var err error
		if s.submissionID != "" {
			err = model.CommitAcceptedRoutingTaskFunding(s.submissionID, actualQuota)
		} else {
			err = s.applyFunding("settle", actualQuota, false, nil)
		}
		if err != nil {
			return err
		}
		s.settled = true
		return nil
	}
	return errors.New("durable billing session identity unavailable")
}

// Refund 同步提交幂等退款；失败保留预留并允许恢复重试。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.fundingID != "" {
		defer s.mu.Unlock()
		if s.settled || s.refunded {
			return
		}
		if err := s.applyFunding("refund", 0, false, nil); err != nil {
			logger.LogError(c, common.LogText("durable funding refund pending (funding=%s): %s", s.fundingID, err.Error()))
			return
		}
		s.refunded = true
		return
	}
	s.mu.Unlock()
	logger.LogError(c, "durable billing session identity unavailable")
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	return !s.settled && !s.refunded && s.fundingID != ""
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	return s.reserve(targetQuota, false)
}

// ReserveBeforeSubmit 在发起上游请求前补扣，钱包余额必须覆盖差额。
func (s *BillingSession) ReserveBeforeSubmit(targetQuota int) error {
	return s.reserve(targetQuota, true)
}

func (s *BillingSession) reserve(targetQuota int, requireAvailableQuota bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relayInfo != nil {
		_, imageRequest := s.relayInfo.Request.(*dto.ImageRequest)
		requireAvailableQuota = requireAvailableQuota || imageRequest || s.relayInfo.ImageRequestCount > 0
	}
	if s.fundingID != "" {
		if s.settled || s.refunded || s.trusted && !requireAvailableQuota || targetQuota <= s.preConsumedQuota {
			return nil
		}
		if err := s.applyFunding("reserve", targetQuota, !requireAvailableQuota, nil); err != nil {
			return err
		}
		s.trusted = false
		return nil
	}
	return errors.New("durable billing session identity unavailable")
}

// ---------------------------------------------------------------------------
// PreConsume — 同步事务预扣费入口
// ---------------------------------------------------------------------------

// preConsume 原子写入资金、令牌、operation 和可选 prepared 凭据。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	// Legacy wallet requests keep the configured trust policy; smart/forced
	// reservations and subsequent ReserveBeforeSubmit always hold the full amount.
	threshold := operation_setting.GetQuotaSetting().TrustQuotaUSD * common.QuotaPerUnit
	if !IsSmartRouting(c) && !s.relayInfo.ForcePreConsume && s.funding.Source() == BillingSourceWallet && threshold > 0 && !math.IsInf(threshold, 0) && !math.IsNaN(threshold) && float64(s.relayInfo.UserQuota) > threshold {
		token, err := model.GetTokenByIds(s.relayInfo.TokenId, s.relayInfo.UserId)
		if err == nil && (token.UnlimitedQuota || float64(token.RemainQuota) > threshold) {
			quota = 0
			s.trusted = true
		}
	}
	requestID, _, _ := RoutingAttemptIdentity(c)
	var submission *model.RoutingSubmission
	if value, ok := c.Get("mowan_routing_submission"); ok {
		submission, _ = value.(*model.RoutingSubmission)
	}
	if submission != nil {
		requestID = submission.RequestID
	}
	if requestID == "" {
		requestID = common.GetRandomString(32)
	}
	s.fundingID = "billing:" + requestID
	if err := s.applyFunding("reserve", quota, false, submission); err != nil {
		code := types.ErrorCodeUpdateDataError
		if errors.Is(err, model.ErrRoutingFundingInsufficient) || strings.Contains(err.Error(), "subscription") {
			code = types.ErrorCodeInsufficientUserQuota
		} else if errors.Is(err, model.ErrRoutingTokenInsufficient) {
			code = types.ErrorCodePreConsumeTokenQuotaFailed
		}
		return types.NewErrorWithStatusCode(err, code, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// PrepareSubmission is called with the final provider/group pricing before the
// sending CAS. A caller can also pass the prepared row to initial pre-consume
// using mowan_routing_submission, so the first reservation and row share a tx.
func (s *BillingSession) PrepareSubmission(submission *model.RoutingSubmission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if submission == nil || s.fundingID == "" || s.settled || s.refunded {
		return errors.New("billing session cannot prepare submission")
	}
	return s.applyFunding("reserve", max(submission.Quota, s.preConsumedQuota), false, submission)
}

func (s *BillingSession) applyFunding(kind string, quota int, allowDebt bool, submission *model.RoutingSubmission) error {
	tokenID := s.relayInfo.TokenId
	if s.relayInfo.IsPlayground {
		tokenID = 0
	}
	opID := fmt.Sprintf("%s:%s:%d", s.fundingID, kind, quota)
	if submission != nil {
		opID = s.fundingID + ":prepare:" + submission.SubmissionID
	}
	op, err := model.PrepareRoutingFunding(model.RoutingFundingRequest{
		FundingID: s.fundingID, OperationID: opID, UserID: s.relayInfo.UserId,
		TokenID: tokenID, Source: s.funding.Source(), ModelName: s.relayInfo.GetBillingModelName(),
		Kind: kind, Quota: quota, AllowDebt: allowDebt,
	}, submission)
	if err != nil {
		return err
	}
	if kind == "reserve" {
		if submission != nil {
			s.submissionID = submission.SubmissionID
		}
		s.preConsumedQuota = max(s.preConsumedQuota, quota)
		if sub, ok := s.funding.(*SubscriptionFunding); ok && op.SubscriptionID > 0 {
			sub.subscriptionId, sub.preConsumed = op.SubscriptionID, int64(s.preConsumedQuota)
			var row model.UserSubscription
			if err := model.DB.First(&row, op.SubscriptionID).Error; err == nil {
				sub.AmountTotal, sub.AmountUsedAfter = row.AmountTotal, row.AmountUsed
			}
			if plan, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(op.SubscriptionID); err == nil && plan != nil {
				sub.PlanId, sub.PlanTitle = plan.PlanId, plan.PlanTitle
			}
		}
		s.syncRelayInfo()
	} else if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(op.Delta)
	}
	return nil
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if preConsumedQuota > 0 && userQuota < preConsumedQuota {
			return nil, types.NewErrorWithStatusCode(
				errors.New(i18n.T(c, i18n.MsgQuotaPreConsumeInsufficient, map[string]any{"Remaining": logger.FormatQuota(userQuota), "Required": logger.FormatQuota(preConsumedQuota)})),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota

		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   &WalletFunding{userId: relayInfo.UserId},
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.GetBillingModelName(),
				amount:    subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return nil, apiErr
			}
			return nil, apiErr
		}
		return session, nil
	}
}
