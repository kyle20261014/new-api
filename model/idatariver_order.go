package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"gorm.io/gorm"
)

const PaymentProviderIdataRiver = "idatariver"
const (
	IdataRiverRefundNone           = "none"
	IdataRiverRefundRecovered      = "recovered"
	IdataRiverRefundManualRequired = "manual_required"
	IdataRiverRefundBeforeCredit   = "refunded_before_credit"
)

// Immutable checkout terms live beside TopUp. Nullable platform IDs allow a
// local snapshot to commit before the non-idempotent external creation call.
type IdataRiverOrder struct {
	TradeNo         string  `gorm:"primaryKey;type:varchar(64)" json:"-"`
	PlatformOrderID *string `gorm:"uniqueIndex;type:varchar(64)" json:"-"`
	MerchantID      string  `gorm:"type:varchar(24)" json:"-"`
	ProjectID       string  `gorm:"type:varchar(24)" json:"-"`
	SKUID           string  `gorm:"type:varchar(24)" json:"-"`
	PriceUSD        string  `gorm:"type:varchar(64)" json:"-"`
	QuotaUSD        string  `gorm:"type:varchar(64)" json:"-"`
	Quota           int     `gorm:"type:bigint" json:"-"`
	CreditedQuota   int     `gorm:"type:bigint" json:"-"`
	CallbackOrigin  string  `gorm:"type:varchar(255)" json:"-"`
	RefundStatus    string  `gorm:"type:varchar(32)" json:"-"`
	RefundTime      int64   `json:"-"`
}

func CreateIdataRiverOrder(ctx context.Context, topup *TopUp, snapshot *IdataRiverOrder) error {
	if topup.UserId <= 0 || topup.TradeNo == "" || topup.TradeNo != snapshot.TradeNo || snapshot.Quota <= 0 || snapshot.Quota > common.MaxWalletQuota || topup.PaymentProvider != PaymentProviderIdataRiver {
		return ErrInvalidTopUpQuota
	}
	snapshot.RefundStatus = IdataRiverRefundNone
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(topup).Error; err != nil {
			return err
		}
		return tx.Create(snapshot).Error
	})
}

func AssociateIdataRiverOrder(ctx context.Context, tradeNo, platformID string) error {
	if platformID == "" || len(platformID) > 64 {
		return ErrTopUpNotFound
	}
	result := DB.WithContext(ctx).Model(&IdataRiverOrder{}).Where("trade_no = ? AND platform_order_id IS NULL", tradeNo).Update("platform_order_id", platformID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTopUpStatusInvalid
	}
	return nil
}

// The owner predicate is applied before any platform lookup. userID=0 is only
// used by the public webhook, after resolving its platform ID locally.
func GetIdataRiverOrder(ctx context.Context, tradeNo string, userID int) (*TopUp, *IdataRiverOrder, error) {
	var topup TopUp
	query := DB.WithContext(ctx).Where("trade_no = ? AND payment_provider = ?", tradeNo, PaymentProviderIdataRiver)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&topup).Error; err != nil {
		return nil, nil, err
	}
	var snapshot IdataRiverOrder
	if err := DB.WithContext(ctx).Where("trade_no = ?", tradeNo).First(&snapshot).Error; err != nil {
		return nil, nil, err
	}
	topup.RefundStatus = snapshot.RefundStatus
	return &topup, &snapshot, nil
}

func GetIdataRiverTradeNo(ctx context.Context, platformID string) (string, error) {
	var snapshot IdataRiverOrder
	if platformID == "" || len(platformID) > 64 {
		return "", ErrTopUpNotFound
	}
	err := DB.WithContext(ctx).Where("platform_order_id = ?", platformID).First(&snapshot).Error
	return snapshot.TradeNo, err
}

func PopulateIdataRiverRefundStatus(topups []*TopUp) error {
	tradeNos := make([]string, 0)
	for _, topup := range topups {
		if topup.PaymentProvider == PaymentProviderIdataRiver {
			tradeNos = append(tradeNos, topup.TradeNo)
		}
	}
	if len(tradeNos) == 0 {
		return nil
	}
	var snapshots []IdataRiverOrder
	if err := DB.Select("trade_no", "refund_status").Where("trade_no IN ?", tradeNos).Find(&snapshots).Error; err != nil {
		return err
	}
	statuses := make(map[string]string, len(snapshots))
	for _, snapshot := range snapshots {
		statuses[snapshot.TradeNo] = snapshot.RefundStatus
	}
	for _, topup := range topups {
		if topup.PaymentProvider == PaymentProviderIdataRiver {
			topup.RefundStatus = statuses[topup.TradeNo]
			if topup.RefundStatus == "" {
				topup.RefundStatus = IdataRiverRefundNone
			}
		}
	}
	return nil
}

// ReconcileIdataRiverOrder accepts only a status verified by the platform
// client against the immutable snapshot. CAS predicates also protect SQLite,
// where SELECT FOR UPDATE is unavailable. Failed transactions remain retryable.
func ReconcileIdataRiverOrder(ctx context.Context, tradeNo, platformID, status, callerIP string) error {
	var topup TopUp
	var snapshot IdataRiverOrder
	var credited, reclaimed bool
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("trade_no = ? AND payment_provider = ?", tradeNo, PaymentProviderIdataRiver).First(&topup).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("trade_no = ? AND platform_order_id = ?", tradeNo, platformID).First(&snapshot).Error; err != nil {
			return err
		}
		if snapshot.RefundStatus != IdataRiverRefundNone {
			return nil
		}
		if snapshot.Quota <= 0 || snapshot.Quota > common.MaxWalletQuota {
			return ErrInvalidTopUpQuota
		}
		switch status {
		case "DONE":
			if snapshot.CreditedQuota > 0 {
				return nil
			}
			if topup.Status != common.TopUpStatusPending && topup.Status != common.TopUpStatusExpired {
				return ErrTopUpStatusInvalid
			}
			claim := tx.Model(&IdataRiverOrder{}).Where("trade_no = ? AND credited_quota = 0 AND refund_status = ?", tradeNo, IdataRiverRefundNone).Update("credited_quota", snapshot.Quota)
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected != 1 {
				return ErrTopUpStatusInvalid
			}
			if err := creditTopUpQuota(tx, topup.UserId, snapshot.Quota, nil); err != nil {
				return err
			}
			if err := tx.Model(&topup).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": common.GetTimestamp()}).Error; err != nil {
				return err
			}
			credited = true
		case "EXPIRED":
			if topup.Status == common.TopUpStatusPending {
				return tx.Model(&topup).Update("status", common.TopUpStatusExpired).Error
			}
		case "REFUND":
			refundStatus := IdataRiverRefundBeforeCredit
			if snapshot.CreditedQuota > 0 {
				if snapshot.CreditedQuota != snapshot.Quota {
					return ErrInvalidTopUpQuota
				}
				var user User
				if err := lockForUpdate(tx).Select("id").First(&user, topup.UserId).Error; err != nil {
					return err
				}
				refundStatus = IdataRiverRefundManualRequired
				result := tx.Model(&User{}).Where("id = ? AND quota >= ?", topup.UserId, snapshot.CreditedQuota).Update("quota", gorm.Expr("quota - ?", snapshot.CreditedQuota))
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 1 {
					refundStatus = IdataRiverRefundRecovered
					reclaimed = true
				}
			}
			claim := tx.Model(&IdataRiverOrder{}).Where("trade_no = ? AND refund_status = ?", tradeNo, IdataRiverRefundNone).Updates(map[string]any{"refund_status": refundStatus, "refund_time": common.GetTimestamp()})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected != 1 {
				return ErrTopUpStatusInvalid
			}
			if snapshot.CreditedQuota == 0 {
				return tx.Model(&topup).Update("status", common.TopUpStatusExpired).Error
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// ponytail: log DB writes follow existing best-effort semantics; an outbox is needed for crash-safe delivery across databases.
	if credited || reclaimed {
		var user User
		_ = DB.WithContext(ctx).Select("username").First(&user, topup.UserId).Error
		other := NewLogOther()
		log := &Log{UserId: topup.UserId, Username: user.Username, CreatedAt: common.GetTimestamp(), Type: LogTypeTopup, Ip: callerIP}
		message := common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"provider": "iDataRiver", "quota": logger.FormatQuota(snapshot.Quota), "amount": snapshot.PriceUSD})
		if reclaimed {
			log.Type = LogTypeRefund
			message = common.NewMessage("iDataRiver refund recovered wallet quota: {{quota}}, order: {{order}}", map[string]any{"quota": logger.FormatQuota(snapshot.CreditedQuota), "order": tradeNo})
		}
		log.Content = other.setContent([]*common.Message{message})
		log.Other = other.JSONString()
		ensureLogRequestId(log)
		if logErr := LOG_DB.WithContext(ctx).Create(log).Error; logErr != nil {
			common.SysError(fmt.Sprintf("iDataRiver accounting log failed user_id=%d trade_no=%s", topup.UserId, tradeNo))
		}
	}
	if common.RedisEnabled && (credited || reclaimed) {
		delta := int64(snapshot.Quota)
		if reclaimed {
			delta = -int64(snapshot.CreditedQuota)
		}
		// Keep display-cache maintenance inside the request deadline, with time left to read the committed order.
		cacheCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if _, cacheErr := cacheApplyUserQuotaDelta(cacheCtx, topup.UserId, delta); cacheErr != nil {
			common.SysError(fmt.Sprintf("iDataRiver quota cache update failed user_id=%d trade_no=%s: %v", topup.UserId, tradeNo, cacheErr))
		}
	}
	return nil
}
