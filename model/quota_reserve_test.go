package model

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createReserveTestUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{
		Username:    "reserve-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       quota,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createReserveTestToken(t *testing.T, remainQuota int) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         "reserve-token-" + common.GetRandomString(8),
		Name:        "reserve-test",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: remainQuota,
	}
	require.NoError(t, token.Insert())
	return token
}

func getUserQuotaFromDB(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

func getTokenFromDB(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func resetBatchUpdateTestState(t *testing.T) {
	t.Helper()
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestTryReserveQuotaWithoutRedis(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	user := createReserveTestUser(t, 100)
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	token := createReserveTestToken(t, 80)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 25, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reloaded := getTokenFromDB(t, token.Id)
	assert.Equal(t, 55, reloaded.RemainQuota)
	assert.Equal(t, 25, reloaded.UsedQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 56, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 55, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRedisBatchReservePersistsWalletBeforeCacheRehydration(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	reserved, err := TryReserveUserQuota(user.Id, 8)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id), "wallet debit must be durable before returning")

	require.NoError(t, invalidateUserCache(user.Id))
	_ = GetUserLanguage(user.Id) // Owner checks and authentication can rehydrate the hash.
	cachedUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, cachedUser.Quota)

	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "Quota", 100).Err())
	for _, fromDB := range []bool{false, true} {
		quota, err := GetUserQuota(user.Id, fromDB)
		require.NoError(t, err)
		assert.Equal(t, 2, quota, "cached funds cannot authorize spending")
	}
	reserved, err = TryReserveUserQuota(user.Id, 3)
	require.NoError(t, err)
	assert.False(t, reserved)

	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "Quota", 0).Err())
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.True(t, reserved, "a low cached balance cannot reject available DB funds")
	assert.Equal(t, 1, getUserQuotaFromDB(t, user.Id))
	UpdateUserUsedQuotaAndRequestCount(user.Id, 9)
	var usage User
	require.NoError(t, DB.First(&usage, user.Id).Error)
	assert.Zero(t, usage.UsedQuota)
	assert.Zero(t, usage.RequestCount)

	token := createReserveTestToken(t, 9)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 3, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 9, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 1, getUserQuotaFromDB(t, user.Id), "flush must not replay wallet mutations")
	require.NoError(t, DB.First(&usage, user.Id).Error)
	assert.Equal(t, 9, usage.UsedQuota)
	assert.Equal(t, 1, usage.RequestCount)
	reloadedToken := getTokenFromDB(t, token.Id)
	assert.Equal(t, 2, reloadedToken.RemainQuota)
	assert.Equal(t, 7, reloadedToken.UsedQuota)
}

func TestBatchUpdateAccumulatesTwoMaximumRequestCharges(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, common.MaxQuota*2+100)
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
	require.NoError(t, IncreaseUserQuota(user.Id, 5, false))
	assert.Equal(t, 105, getUserQuotaFromDB(t, user.Id))

	batchUpdate()
	assert.Equal(t, 105, getUserQuotaFromDB(t, user.Id))
}

func TestBatchUpdateAccumulatorSaturatesOverflow(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUsedQuota, 1, math.MaxInt)
	addNewRecord(BatchUpdateTypeUsedQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUsedQuota].Lock()
	assert.Equal(t, math.MaxInt, batchUpdateStores[BatchUpdateTypeUsedQuota][1])
	batchUpdateLocks[BatchUpdateTypeUsedQuota].Unlock()

	batchUpdateLocks[BatchUpdateTypeUsedQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUsedQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUsedQuota].Unlock()
	addNewRecord(BatchUpdateTypeUsedQuota, 1, math.MinInt)
	addNewRecord(BatchUpdateTypeUsedQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUsedQuota].Lock()
	assert.Equal(t, math.MinInt, batchUpdateStores[BatchUpdateTypeUsedQuota][1])
	batchUpdateLocks[BatchUpdateTypeUsedQuota].Unlock()
}

func TestReserveFallsBackToDatabaseWhenRedisIsUnavailable(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 20)
	require.NoError(t, populateUserCache(user))
	server.Close()
	common.BatchUpdateEnabled = true

	// Redis 故障不改变钱包的数据库条件更新。
	reserved, err := TryReserveUserQuota(user.Id, 5)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 16)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))
}

func TestSynchronousReserveCompensatesCacheWhenPersistenceFails(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 6)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota)

	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cachedToken, cacheErr := cacheGetTokenByKey(token.Key)
	require.NoError(t, cacheErr)
	assert.Equal(t, 12, cachedToken.RemainQuota)
	assert.Zero(t, cachedToken.UsedQuota)
}

func TestUserReservePreservesValidationDeletionAndSettlementDebt(t *testing.T) {
	for _, mode := range []struct {
		name  string
		redis bool
		batch bool
	}{
		{"database", false, false}, {"batch", false, true},
		{"redis", true, false}, {"redis batch", true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			truncateTables(t)
			resetBatchUpdateTestState(t)
			if mode.redis {
				useUserCacheMiniRedis(t)
			}
			common.BatchUpdateEnabled = mode.batch
			user := createReserveTestUser(t, 10)
			if mode.redis {
				require.NoError(t, populateUserCache(user))
			}
			reserved, err := TryReserveUserQuota(user.Id, -1)
			assert.Error(t, err)
			assert.False(t, reserved)
			reserved, err = TryReserveUserQuota(user.Id, 0)
			require.NoError(t, err)
			assert.True(t, reserved)
			reserved, err = TryReserveUserQuota(user.Id, 11)
			require.NoError(t, err)
			assert.False(t, reserved)
			assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id))

			require.NoError(t, DecreaseUserQuota(user.Id, 15, false))
			assert.Equal(t, -5, getUserQuotaFromDB(t, user.Id), "settlement must preserve legitimate debt")
			reserved, err = TryReserveUserQuota(user.Id, 1)
			require.NoError(t, err)
			assert.False(t, reserved)
			require.NoError(t, IncreaseUserQuota(user.Id, 5, false))
			assert.Zero(t, getUserQuotaFromDB(t, user.Id))

			for _, hardDelete := range []bool{false, true} {
				deleted := createReserveTestUser(t, 10)
				if mode.redis {
					require.NoError(t, populateUserCache(deleted))
				}
				query := DB
				if hardDelete {
					query = query.Unscoped()
				}
				require.NoError(t, query.Delete(&deleted).Error)
				reserved, err = TryReserveUserQuota(deleted.Id, 1)
				assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
				assert.False(t, reserved)
				assert.ErrorIs(t, IncreaseUserQuota(deleted.Id, 1, false), gorm.ErrRecordNotFound)
				assert.ErrorIs(t, DecreaseUserQuota(deleted.Id, 1, false), gorm.ErrRecordNotFound)
			}
			batchUpdate()
			assert.Zero(t, getUserQuotaFromDB(t, user.Id))
		})
	}
}

func TestConcurrentUserReserveDoesNotDoubleSpend(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			reserved, err := TryReserveUserQuota(user.Id, 60)
			results <- reserved
			failures <- err
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var accepted int
	for reserved := range results {
		if reserved {
			accepted++
		}
	}
	assert.Equal(t, 1, accepted)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
	batchUpdate()
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
}

func TestUserReserveAndCreditKeepCacheAfterDatabaseFailure(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true
	user := createReserveTestUser(t, common.MaxWalletQuota-1)
	require.NoError(t, populateUserCache(user))
	require.ErrorIs(t, IncreaseUserQuota(user.Id, 2, false), ErrWalletQuotaLimitExceeded)
	assert.Equal(t, common.MaxWalletQuota-1, getUserQuotaFromDB(t, user.Id))
	require.NoError(t, IncreaseUserQuota(user.Id, 1, false))
	assert.Equal(t, common.MaxWalletQuota, getUserQuotaFromDB(t, user.Id))
	require.ErrorIs(t, IncreaseUserQuota(user.Id, 1, true), ErrWalletQuotaLimitExceeded)
	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.MaxWalletQuota, cached.Quota)

	const callback = "test:wallet_database_failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(gorm.ErrInvalidTransaction)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(callback) })
	assert.ErrorIs(t, DecreaseUserQuota(user.Id, 1, false), gorm.ErrInvalidTransaction)
	assert.ErrorIs(t, IncreaseUserQuota(user.Id, 1, false), gorm.ErrInvalidTransaction)
	reserved, err := TryReserveUserQuota(user.Id, 1)
	assert.ErrorIs(t, err, gorm.ErrInvalidTransaction)
	assert.False(t, reserved)
	assert.Equal(t, common.MaxWalletQuota, getUserQuotaFromDB(t, user.Id))
	cached, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.MaxWalletQuota, cached.Quota, "failed DB mutations must not change cache")
}

func TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	loaded, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	stale := *loaded

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// 已存在的哈希只刷新 TTL：数据库快照不得覆盖已被原子预扣的余额。
	code, err := cacheInitToken(stale)
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 30, cached.RemainQuota)

	// 变更期间：fence 删除缓存并拦截并发读者手中的过期快照。
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	code, err = cacheInitToken(stale)
	require.NoError(t, err)
	assert.Zero(t, code, "the pre-mutation snapshot must not be published while fenced")
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	// fence 过期后可重新从数据库水合。
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	cached, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
}
