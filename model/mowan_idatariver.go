package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const PaymentProviderIdataRiver = "idatariver"
const (
	IdataRiverRefundNone           = "none"
	IdataRiverRefundBeforeCredit   = "before_credit"
	IdataRiverRefundRecovered      = "recovered"
	IdataRiverRefundManualRequired = "manual_required"
)

// Private contact information can retrieve digital goods. Never serialize this
// record or use a SQL logger that expands its values (including in debug mode).
type IdataRiverOrder struct {
	TradeNo         string  `gorm:"primaryKey;type:varchar(64)" json:"-"`
	UserID          int     `gorm:"uniqueIndex:mowan_idr_user_request" json:"-"`
	RequestID       string  `gorm:"uniqueIndex:mowan_idr_user_request;type:varchar(128)" json:"-"`
	PlatformOrderID *string `gorm:"uniqueIndex;type:varchar(64)" json:"-"`
	ContactInfo     string  `gorm:"uniqueIndex;type:varchar(64)" json:"-"`
	MerchantID      string  `gorm:"type:varchar(24)" json:"-"`
	ProjectID       string  `gorm:"type:varchar(24)" json:"-"`
	SKUID           string  `gorm:"type:varchar(24)" json:"-"`
	Name            string  `gorm:"type:varchar(200)" json:"-"`
	PriceUSD        string  `gorm:"type:varchar(64)" json:"-"`
	QuotaUSD        string  `gorm:"type:varchar(64)" json:"-"`
	Currency        string  `gorm:"type:varchar(3)" json:"-"`
	Quantity        int     `json:"-"`
	Quota           int     `gorm:"type:bigint" json:"-"`
	CreditedQuota   int     `gorm:"type:bigint" json:"-"`
	CallbackOrigin  string  `gorm:"type:varchar(255)" json:"-"`
	CreationState   string  `gorm:"type:varchar(16)" json:"-"`
	PlatformStatus  string  `gorm:"type:varchar(16)" json:"-"`
	RefundStatus    string  `gorm:"type:varchar(32)" json:"-"`
	RefundTime      int64   `json:"-"`
}

func (IdataRiverOrder) TableName() string { return "mowan_idatariver_orders" }

func idataRiverDB(ctx context.Context) *gorm.DB {
	return DB.WithContext(ctx).Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

// Called by the startup assembly after InitDB, under the existing master gate.
// As with upstream migrations, multiple master processes must start serially.
func MigrateIdataRiver(db *gorm.DB) error { return db.AutoMigrate(&IdataRiverOrder{}) }

type IdataRiverPaymentMethod struct {
	Method string `json:"method"`
	Name   string `json:"name"`
}

type IdataRiverSummary struct {
	TradeNo        string                    `json:"trade_no"`
	RequestID      string                    `json:"request_id"`
	SKUID          string                    `json:"sku_id"`
	Name           string                    `json:"name"`
	Price          float64                   `json:"price"`
	Quota          float64                   `json:"quota"`
	Currency       string                    `json:"currency"`
	Status         string                    `json:"status"`
	PlatformStatus string                    `json:"platform_status"`
	CreationState  string                    `json:"creation_state"`
	Credited       bool                      `json:"credited"`
	RefundStatus   string                    `json:"refund_status"`
	PaymentMethods []IdataRiverPaymentMethod `json:"payment_methods"`
	CanPay         bool                      `json:"can_pay"`
	CanCheck       bool                      `json:"can_check"`
	CanRecreate    bool                      `json:"can_recreate"`
	QueryError     bool                      `json:"query_error"`
}

func IdataRiverOrderSummary(topup *TopUp, s *IdataRiverOrder) *IdataRiverSummary {
	price, _ := decimal.NewFromString(s.PriceUSD)
	quota, _ := decimal.NewFromString(s.QuotaUSD)
	return &IdataRiverSummary{TradeNo: s.TradeNo, RequestID: s.RequestID, SKUID: s.SKUID, Name: s.Name, Price: price.InexactFloat64(), Quota: quota.InexactFloat64(), Currency: s.Currency, Status: topup.Status, PlatformStatus: s.PlatformStatus, CreationState: s.CreationState, Credited: s.CreditedQuota > 0, RefundStatus: s.RefundStatus, PaymentMethods: []IdataRiverPaymentMethod{}, CanCheck: true, CanRecreate: s.PlatformStatus == "EXPIRED" && s.RefundStatus == IdataRiverRefundNone && s.CreditedQuota == 0}
}

func PopulateIdataRiverSummaries(ctx context.Context, topups []*TopUp) error {
	trades := make([]string, 0)
	for _, t := range topups {
		if t.PaymentProvider == PaymentProviderIdataRiver {
			trades = append(trades, t.TradeNo)
		}
	}
	if len(trades) == 0 {
		return nil
	}
	var snapshots []IdataRiverOrder
	if err := idataRiverDB(ctx).Where("trade_no IN ?", trades).Find(&snapshots).Error; err != nil {
		return err
	}
	byTrade := make(map[string]*IdataRiverOrder, len(snapshots))
	for i := range snapshots {
		byTrade[snapshots[i].TradeNo] = &snapshots[i]
	}
	for _, t := range topups {
		if t.PaymentProvider == PaymentProviderIdataRiver {
			s := byTrade[t.TradeNo]
			if s == nil || s.UserID != t.UserId {
				return ErrTopUpNotFound
			}
			t.IdataRiver = IdataRiverOrderSummary(t, s)
		}
	}
	return nil
}

func GetIdataRiverOrder(ctx context.Context, tradeNo, requestID string, userID int) (*TopUp, *IdataRiverOrder, error) {
	query := idataRiverDB(ctx)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if tradeNo != "" {
		query = query.Where("trade_no = ?", tradeNo)
	} else {
		query = query.Where("request_id = ?", requestID)
	}
	var s IdataRiverOrder
	if err := query.First(&s).Error; err != nil {
		return nil, nil, err
	}
	var t TopUp
	if err := idataRiverDB(ctx).Where("trade_no = ? AND user_id = ? AND payment_provider = ?", s.TradeNo, s.UserID, PaymentProviderIdataRiver).First(&t).Error; err != nil {
		return nil, nil, err
	}
	return &t, &s, nil
}

// The unique (user_id, request_id) insertion is the cross-instance creation
// claim. Only the transaction that inserted it may call external order/add.
func CreateIdataRiverAttempt(ctx context.Context, t *TopUp, s *IdataRiverOrder) (bool, error) {
	if t.UserId <= 0 || s.UserID != t.UserId || s.TradeNo != t.TradeNo || s.Quota <= 0 || s.Quota > common.MaxWalletQuota || s.Quantity != 1 || s.Currency != "USD" || s.RequestID == "" || len(s.ContactInfo) != 64 || t.PaymentProvider != PaymentProviderIdataRiver {
		return false, ErrInvalidTopUpQuota
	}
	s.RefundStatus = IdataRiverRefundNone
	s.CreationState = "creating"
	err := idataRiverDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		return tx.Create(t).Error
	})
	if err == nil {
		return true, nil
	}
	if _, _, existingErr := GetIdataRiverOrder(ctx, "", s.RequestID, s.UserID); existingErr == nil {
		return false, nil
	}
	return false, err
}

func MarkIdataRiverUnknown(ctx context.Context, tradeNo string) error {
	return idataRiverDB(ctx).Model(&IdataRiverOrder{}).Where("trade_no = ? AND platform_order_id IS NULL", tradeNo).Update("creation_state", "unknown").Error
}

func AssociateIdataRiverOrder(ctx context.Context, tradeNo, platformID string) error {
	if platformID == "" || len(platformID) > 64 {
		return ErrTopUpNotFound
	}
	result := idataRiverDB(ctx).Model(&IdataRiverOrder{}).Where("trade_no = ? AND platform_order_id IS NULL", tradeNo).Updates(map[string]any{"platform_order_id": platformID, "creation_state": "associated"})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var s IdataRiverOrder
	if err := idataRiverDB(ctx).Where("trade_no = ?", tradeNo).First(&s).Error; err != nil {
		return err
	}
	if s.PlatformOrderID != nil && *s.PlatformOrderID == platformID {
		return nil
	}
	return ErrTopUpStatusInvalid
}

func GetIdataRiverTradeNo(ctx context.Context, platformID string) (string, error) {
	var s IdataRiverOrder
	err := idataRiverDB(ctx).Where("platform_order_id = ?", platformID).First(&s).Error
	return s.TradeNo, err
}

func GetIdataRiverTradeNoByContact(ctx context.Context, contact string) (string, error) {
	var s IdataRiverOrder
	err := idataRiverDB(ctx).Where("contact_info = ?", contact).First(&s).Error
	return s.TradeNo, err
}

// Only call after the server client verifies the complete immutable snapshot.
// SQL CAS protects SQLite as well as row-locking MySQL/PostgreSQL. No external
// network call is made while holding these rows, except the wallet cache CAS.
func ReconcileIdataRiverOrder(ctx context.Context, tradeNo, platformID, status, callerIP string) error {
	if status != "NEW" && status != "DONE" && status != "EXPIRED" && status != "REFUND" {
		return ErrTopUpStatusInvalid
	}
	var t TopUp
	var s IdataRiverOrder
	credited, reclaimed, cacheReserved, refundRecorded := false, false, false, false
	err := idataRiverDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("trade_no = ? AND payment_provider = ?", tradeNo, PaymentProviderIdataRiver).First(&t).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("trade_no = ? AND platform_order_id = ? AND user_id = ?", tradeNo, platformID, t.UserId).First(&s).Error; err != nil {
			return err
		}
		if s.RefundStatus != IdataRiverRefundNone {
			return nil
		}
		if s.Quota <= 0 || s.Quota > common.MaxWalletQuota {
			return ErrInvalidTopUpQuota
		}
		switch status {
		case "DONE":
			if s.CreditedQuota > 0 {
				return nil
			}
			if t.Status != common.TopUpStatusPending && t.Status != common.TopUpStatusExpired {
				return ErrTopUpStatusInvalid
			}
			claim := tx.Model(&IdataRiverOrder{}).Where("trade_no = ? AND credited_quota = 0 AND refund_status = ?", tradeNo, IdataRiverRefundNone).Updates(map[string]any{"credited_quota": s.Quota, "platform_status": "DONE"})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected != 1 {
				return ErrTopUpStatusInvalid
			}
			if err := creditTopUpQuota(tx, t.UserId, s.Quota, nil); err != nil {
				return err
			}
			if err := tx.Model(&t).Updates(map[string]any{"status": common.TopUpStatusSuccess, "complete_time": common.GetTimestamp()}).Error; err != nil {
				return err
			}
			credited = true
		case "EXPIRED":
			if s.CreditedQuota > 0 {
				return nil
			}
			if err := tx.Model(&t).Update("status", common.TopUpStatusExpired).Error; err != nil {
				return err
			}
			return tx.Model(&s).Update("platform_status", "EXPIRED").Error
		case "NEW":
			if s.PlatformStatus == "" || s.PlatformStatus == "NEW" {
				return tx.Model(&s).Update("platform_status", "NEW").Error
			}
		case "REFUND":
			refundStatus := IdataRiverRefundBeforeCredit
			if s.CreditedQuota > 0 {
				if s.CreditedQuota != s.Quota {
					return ErrInvalidTopUpQuota
				}
				var user User
				if err := lockForUpdate(tx).Select("id").First(&user, t.UserId).Error; err != nil {
					return err
				}
				refundStatus = IdataRiverRefundManualRequired
				canRecover := true
				if common.RedisEnabled {
					result, cacheErr := common.RDB.Eval(ctx, userQuotaReserveScript, []string{getUserCacheKey(t.UserId)}, s.CreditedQuota, t.UserId, userCacheSchemaVersion).Int()
					if cacheErr != nil {
						return errors.New("wallet cache unavailable")
					}
					cacheReserved = result == 1
					canRecover = result != 0
					// With deferred DB consumption a missing cache is not evidence
					// of sufficient funds. Keep the refund retryable.
					if result == -1 && common.BatchUpdateEnabled {
						return errors.New("live wallet balance unavailable")
					}
				}
				if canRecover {
					debit := tx.Model(&User{}).Where("id = ? AND quota >= ?", t.UserId, s.CreditedQuota).Update("quota", gorm.Expr("quota - ?", s.CreditedQuota))
					if debit.Error != nil {
						return debit.Error
					}
					if debit.RowsAffected == 1 {
						refundStatus = IdataRiverRefundRecovered
						reclaimed = true
					}
				}
			}
			claim := tx.Model(&IdataRiverOrder{}).Where("trade_no = ? AND refund_status = ?", tradeNo, IdataRiverRefundNone).Updates(map[string]any{"refund_status": refundStatus, "refund_time": common.GetTimestamp(), "platform_status": "REFUND"})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected != 1 {
				return ErrTopUpStatusInvalid
			}
			refundRecorded = true
			s.RefundStatus = refundStatus
		}
		return nil
	})
	if cacheReserved && (err != nil || !reclaimed) {
		// Leave callback response time below five seconds even after its budget expires.
		compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		if restoreErr := common.RDB.Eval(compCtx, userQuotaDeltaScript, []string{getUserCacheKey(t.UserId)}, s.CreditedQuota, t.UserId, userCacheSchemaVersion).Err(); restoreErr != nil {
			common.SysError(common.LogText("iDataRiver wallet cache compensation failed user_id=%d", t.UserId))
		}
	}
	if err != nil {
		return err
	}
	if common.RedisEnabled && (credited || (reclaimed && !cacheReserved)) {
		delta := int64(s.Quota)
		if reclaimed {
			delta = -int64(s.CreditedQuota)
		}
		cacheCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		if cacheErr := common.RDB.Eval(cacheCtx, userQuotaDeltaScript, []string{getUserCacheKey(t.UserId)}, delta, t.UserId, userCacheSchemaVersion).Err(); cacheErr != nil {
			common.SysError(common.LogText("iDataRiver wallet cache update failed user_id=%d", t.UserId))
		}
	}
	// ponytail: audit logs follow upstream best-effort delivery; use an outbox if cross-database crash-safe logs become required. The refund ledger above is transactional.
	if credited || refundRecorded {
		content := common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"provider": "iDataRiver", "quota": s.QuotaUSD, "amount": s.PriceUSD})
		logType := LogTypeTopup
		if refundRecorded {
			logType = LogTypeRefund
			content = common.NewMessage("iDataRiver refund processed, order: {{order}}, result: {{result}}", map[string]any{"order": tradeNo, "result": s.RefundStatus})
		}
		var user User
		_ = idataRiverDB(ctx).Select("username").First(&user, t.UserId).Error
		other := NewLogOther()
		other.MergeAdmin(map[string]any{"payment_method": PaymentProviderIdataRiver, "caller_ip": callerIP, "trade_no": tradeNo})
		log := &Log{UserId: t.UserId, Username: user.Username, CreatedAt: common.GetTimestamp(), Type: logType, Content: other.setContent([]*common.Message{content}), Ip: callerIP, Other: other.JSONString()}
		ensureLogRequestId(log)
		if logErr := LOG_DB.WithContext(ctx).Create(log).Error; logErr != nil {
			common.SysError(common.LogText("iDataRiver audit log delivery failed user_id=%d", t.UserId))
		}
	}
	return nil
}
