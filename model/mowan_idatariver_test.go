package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func idataRiverWalletOrder(t *testing.T, user User) string {
	t.Helper()
	require.NoError(t, MigrateIdataRiver(DB))
	trade := "idr-wallet-" + common.GetRandomString(20)
	s := &IdataRiverOrder{TradeNo: trade, UserID: user.Id, RequestID: trade, ContactInfo: strings.Repeat("a", 32) + common.GetRandomString(32), Quantity: 1, Currency: "USD", Quota: 100, PriceUSD: "1", QuotaUSD: "0.0002"}
	created, err := CreateIdataRiverAttempt(t.Context(), &TopUp{UserId: user.Id, TradeNo: trade, PaymentProvider: PaymentProviderIdataRiver, PaymentMethod: PaymentProviderIdataRiver, Status: common.TopUpStatusPending}, s)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, AssociateIdataRiverOrder(t.Context(), trade, trade))
	t.Cleanup(func() { require.NoError(t, DB.Where("trade_no = ?", trade).Delete(&IdataRiverOrder{}).Error) })
	return trade
}

func TestIdataRiverRefundUsesLiveBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	user := createReserveTestUser(t, 0)
	require.NoError(t, populateUserCache(user))
	trade := idataRiverWalletOrder(t, user)
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "DONE", ""))
	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.Quota)
	common.BatchUpdateEnabled = true
	reserved, err := TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "SQL still has deferred consumption")
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "REFUND", ""))
	_, s, err := GetIdataRiverOrder(t.Context(), trade, "", user.Id)
	require.NoError(t, err)
	assert.Equal(t, IdataRiverRefundManualRequired, s.RefundStatus)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "insufficient live balance must not debit SQL")
	cached, err = GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 99, cached.Quota)
	batchUpdate()
	assert.Equal(t, 99, getUserQuotaFromDB(t, user.Id))
}

func TestIdataRiverRefundCacheCompensationAndMissingBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	user := createReserveTestUser(t, 0)
	require.NoError(t, populateUserCache(user))
	trade := idataRiverWalletOrder(t, user)
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "DONE", ""))
	const callback = "idata-refund-cache-fail"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "mowan_idatariver_orders" {
			tx.AddError(errors.New("injected refund persistence error"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callback) })
	require.Error(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "REFUND", ""))
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
	cached, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.Quota, "SQL rollback restores the cache reservation")
	require.NoError(t, DB.Callback().Update().Remove(callback))
	common.BatchUpdateEnabled = true
	server.Del(getUserCacheKey(user.Id))
	require.Error(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "REFUND", ""), "missing cache with deferred consumption remains retryable")
	_, s, err := GetIdataRiverOrder(t.Context(), trade, "", user.Id)
	require.NoError(t, err)
	assert.Equal(t, IdataRiverRefundNone, s.RefundStatus)
	_, err = GetUserCache(user.Id)
	require.NoError(t, err)
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "REFUND", ""))
	assert.Zero(t, getUserQuotaFromDB(t, user.Id))
	cached, err = GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Zero(t, cached.Quota)
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "REFUND", ""))
	require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "DONE", ""))
	assert.Zero(t, getUserQuotaFromDB(t, user.Id))
}

func TestIdataRiverRefundCompetesWithConsumption(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	for range 8 {
		user := createReserveTestUser(t, 0)
		require.NoError(t, populateUserCache(user))
		trade := idataRiverWalletOrder(t, user)
		require.NoError(t, ReconcileIdataRiverOrder(t.Context(), trade, trade, "DONE", ""))
		var wg sync.WaitGroup
		var consumed bool
		var consumeErr, refundErr error
		wg.Go(func() { consumed, consumeErr = TryReserveUserQuota(user.Id, 50) })
		wg.Go(func() { refundErr = ReconcileIdataRiverOrder(context.Background(), trade, trade, "REFUND", "") })
		wg.Wait()
		require.NoError(t, consumeErr)
		require.NoError(t, refundErr)
		cached, err := GetUserCache(user.Id)
		require.NoError(t, err)
		_, s, err := GetIdataRiverOrder(t.Context(), trade, "", user.Id)
		require.NoError(t, err)
		if consumed {
			assert.Equal(t, 50, cached.Quota)
			assert.Equal(t, 50, getUserQuotaFromDB(t, user.Id))
			assert.Equal(t, IdataRiverRefundManualRequired, s.RefundStatus)
		} else {
			assert.Zero(t, cached.Quota)
			assert.Zero(t, getUserQuotaFromDB(t, user.Id))
			assert.Equal(t, IdataRiverRefundRecovered, s.RefundStatus)
		}
	}
}
