package model

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestDashboardStatsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			mainDB, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "dashboard_main_"}})
			require.NoError(t, err)
			sqlDB, err := mainDB.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			logDB, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "dashboard_log_"}})
			require.NoError(t, err)
			logSQLDB, err := logDB.DB()
			require.NoError(t, err)
			logSQLDB.SetMaxOpenConns(1)
			previousDB, previousLogDB := DB, LOG_DB
			DB, LOG_DB = mainDB, logDB
			t.Cleanup(func() {
				require.NoError(t, logDB.Migrator().DropTable(&Log{}))
				require.NoError(t, mainDB.Migrator().DropTable(&User{}, &TopUp{}, &SubscriptionOrder{}, &Log{}))
				DB, LOG_DB = previousDB, previousLogDB
				require.NoError(t, sqlDB.Close())
				require.NoError(t, logSQLDB.Close())
			})
			require.NoError(t, mainDB.AutoMigrate(&User{}, &TopUp{}, &SubscriptionOrder{}, &Log{}))
			require.NoError(t, logDB.AutoMigrate(&Log{}))
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, mainDB.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			location, err := time.LoadLocation("Asia/Shanghai")
			require.NoError(t, err)

			for _, date := range []string{"2026-06-15T09:30:00+08:00", "2026-06-01T00:00:00+08:00", "2027-01-01T00:00:00+08:00"} {
				t.Run(date, func(t *testing.T) {
					for _, record := range []any{&User{}, &TopUp{}, &SubscriptionOrder{}, &Log{}} {
						require.NoError(t, mainDB.Unscoped().Where("1 = 1").Delete(record).Error)
					}
					require.NoError(t, logDB.Where("1 = 1").Delete(&Log{}).Error)
					now, err := time.Parse(time.RFC3339, date)
					require.NoError(t, err)
					// Passing UTC verifies that the function converts to Shanghai first.
					now = now.UTC()
					local := now.In(location)
					dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).Unix()
					monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location).Unix()
					empty, err := GetDashboardStats(context.Background(), now)
					require.NoError(t, err)
					assert.Equal(t, &DashboardStats{UpdatedAt: now.Unix(), Timezone: "Asia/Shanghai"}, empty)

					users := []User{
						{Username: "enabled", Password: "unused", AffCode: "enabled", Quota: 101, Status: common.UserStatusEnabled},
						{Username: "disabled", Password: "unused", AffCode: "disabled", Quota: 53, Status: common.UserStatusDisabled},
						{Username: "deleted", Password: "unused", AffCode: "deleted", Quota: 999},
					}
					require.NoError(t, mainDB.Create(&users).Error)
					require.NoError(t, mainDB.Delete(&users[2]).Error)
					topups := []TopUp{
						{TradeNo: "month-start", Amount: 2, CompleteTime: monthStart, Status: common.TopUpStatusSuccess},
						{TradeNo: "stripe", Amount: 999, Money: 2.123457, PaymentProvider: PaymentProviderStripe, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
						{TradeNo: "creem", Amount: 25, Money: 999, PaymentProvider: PaymentProviderCreem, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
						{TradeNo: "epay", Amount: 3, Money: 999, PaymentProvider: PaymentProviderEpay, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
						{TradeNo: "waffo", Amount: 1, PaymentProvider: PaymentProviderWaffo, CompleteTime: now.Unix(), Status: common.TopUpStatusSuccess},
						{TradeNo: "yesterday", Amount: 5, CompleteTime: dayStart - 1, Status: common.TopUpStatusSuccess},
						{TradeNo: "previous-month", Amount: 99, CompleteTime: monthStart - 1, Status: common.TopUpStatusSuccess},
						{TradeNo: "future", Amount: 99, CompleteTime: now.Unix() + 1, Status: common.TopUpStatusSuccess},
						{TradeNo: "pending", Amount: 99, CompleteTime: dayStart, Status: common.TopUpStatusPending},
						{TradeNo: "expired", Amount: 99, CompleteTime: dayStart, Status: common.TopUpStatusExpired},
						{TradeNo: "failed", Amount: 99, CompleteTime: dayStart, Status: "failed"},
						{TradeNo: "subscription-mirror", Amount: 0, Money: 99, PaymentMethod: PaymentMethodStripe, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
						{TradeNo: "legacy-mirror", Amount: 0, Money: 99, PaymentProvider: PaymentProviderStripe, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
					}
					require.NoError(t, mainDB.Create(&topups).Error)
					require.NoError(t, mainDB.Create(&[]SubscriptionOrder{{TradeNo: "subscription-mirror"}, {TradeNo: "legacy-mirror"}}).Error)
					logs := []Log{
						{Type: LogTypeConsume, CreatedAt: monthStart, Quota: 7},
						{Type: LogTypeConsume, CreatedAt: dayStart, Quota: 11},
						{Type: LogTypeConsume, CreatedAt: dayStart, Quota: 0},
						{Type: LogTypeConsume, CreatedAt: now.Unix(), Quota: 3, Other: `{"billing_source":"subscription"}`},
						{Type: LogTypeConsume, CreatedAt: dayStart - 1, Quota: 5},
						{Type: LogTypeConsume, CreatedAt: monthStart - 1, Quota: 999},
						{Type: LogTypeConsume, CreatedAt: now.Unix() + 1, Quota: 999},
						{Type: LogTypeError, CreatedAt: dayStart, Quota: 999},
						{Type: LogTypeRefund, CreatedAt: dayStart, Quota: 999},
						{Type: LogTypeTopup, CreatedAt: dayStart, Quota: 999},
					}
					require.NoError(t, logDB.Create(&logs).Error)
					// The decoy catches accidentally reading consumption from DB.
					require.NoError(t, mainDB.Create(&Log{Type: LogTypeConsume, CreatedAt: dayStart, Quota: 9999}).Error)
					stats, err := GetDashboardStats(context.Background(), now)
					require.NoError(t, err)
					stripeQuota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromFloat(2.123457).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
					require.NoError(t, err)
					expectedToday := DashboardPeriodStats{TopUpQuota: float64(stripeQuota) + 25 + 4*common.QuotaPerUnit, ConsumedQuota: 14, RequestCount: 3}
					expectedMonth := DashboardPeriodStats{TopUpQuota: float64(stripeQuota) + 25 + 6*common.QuotaPerUnit, ConsumedQuota: 21, RequestCount: 4}
					if dayStart == monthStart {
						expectedToday.TopUpQuota += 2 * common.QuotaPerUnit
						expectedToday.ConsumedQuota += 7
						expectedToday.RequestCount++
					} else {
						expectedMonth.TopUpQuota += 5 * common.QuotaPerUnit
						expectedMonth.ConsumedQuota += 5
						expectedMonth.RequestCount++
					}
					assert.Equal(t, expectedToday, stats.Today)
					assert.Equal(t, expectedMonth, stats.Month)
					assert.Equal(t, float64(154), stats.CurrentBalanceQuota)
					assert.Equal(t, now.Unix(), stats.UpdatedAt)
					assert.Equal(t, "Asia/Shanghai", stats.Timezone)
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = GetDashboardStats(ctx, time.Now())
			assert.ErrorIs(t, err, context.Canceled)
			require.NoError(t, logDB.Callback().Row().Before("gorm:row").Register("dashboard_failure", func(tx *gorm.DB) {
				tx.AddError(errors.New("log database unavailable"))
			}))
			_, err = GetDashboardStats(context.Background(), time.Now())
			assert.EqualError(t, err, "log database unavailable", "database errors must not look like zero totals")
			require.NoError(t, logDB.Callback().Row().Remove("dashboard_failure"))
			t.Logf("%s: empty, credits, balances, consumption, boundaries, separate log database and failures verified", dialect)
		})
	}
}
