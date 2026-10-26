package model

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

type DashboardPeriodStats struct {
	TopUpQuota    float64 `json:"top_up_quota"`
	ConsumedQuota float64 `json:"consumed_quota"`
	RequestCount  int64   `json:"request_count"`
}

type DashboardStats struct {
	Today               DashboardPeriodStats `json:"today"`
	Month               DashboardPeriodStats `json:"month"`
	CurrentBalanceQuota float64              `json:"current_balance_quota"`
	UpdatedAt           int64                `json:"updated_at"`
	Timezone            string               `json:"timezone"`
}

// GetDashboardStats reads wallet credits from the main database and consumption
// from the configured log database. Balances are current, not period snapshots.
func GetDashboardStats(ctx context.Context, now time.Time) (*DashboardStats, error) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	now = now.In(location)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location).Unix()
	stats := &DashboardStats{UpdatedAt: now.Unix(), Timezone: location.String()}
	mainDB := DB.WithContext(ctx)
	logDB := LOG_DB.WithContext(ctx)

	if err := mainDB.Model(&User{}).Select("COALESCE(SUM(quota * 1.0), 0)").
		Scan(&stats.CurrentBalanceQuota).Error; err != nil {
		return nil, err
	}

	// Stream once and use the same per-order decimal conversion as wallet credits.
	// SQL ROUND on approximate money values rounds ties differently across engines.
	subscriptions := mainDB.Model(&SubscriptionOrder{}).Select("trade_no").Where("trade_no IS NOT NULL")
	rows, err := mainDB.Model(&TopUp{}).
		Select("payment_provider, amount, money, complete_time").
		Where("status = ? AND complete_time >= ? AND complete_time <= ?", common.TopUpStatusSuccess, monthStart, stats.UpdatedAt).
		Where("trade_no NOT IN (?)", subscriptions).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var order TopUp
		if err := rows.Scan(&order.PaymentProvider, &order.Amount, &order.Money, &order.CompleteTime); err != nil {
			return nil, err
		}
		credit := decimal.NewFromInt(order.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		switch order.PaymentProvider {
		case PaymentProviderStripe:
			credit = decimal.NewFromFloat(order.Money).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		case PaymentProviderCreem, PaymentProviderIdataRiver:
			credit = decimal.NewFromInt(order.Amount)
		}
		quota, err := common.WalletQuotaFromDecimalStrict(credit)
		if err != nil {
			return nil, err
		}
		stats.Month.TopUpQuota += float64(quota)
		if order.CompleteTime >= dayStart {
			stats.Today.TopUpQuota += float64(quota)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, period := range []struct {
		start int64
		stats *DashboardPeriodStats
	}{{dayStart, &stats.Today}, {monthStart, &stats.Month}} {
		var consumption DashboardPeriodStats
		if err := logDB.Model(&Log{}).
			Where("type = ? AND created_at >= ? AND created_at <= ?", LogTypeConsume, period.start, stats.UpdatedAt).
			Select("COALESCE(SUM(quota * 1.0), 0) AS consumed_quota, COUNT(*) AS request_count").
			Scan(&consumption).Error; err != nil {
			return nil, err
		}
		period.stats.ConsumedQuota = consumption.ConsumedQuota
		period.stats.RequestCount = consumption.RequestCount
	}
	return stats, nil
}
