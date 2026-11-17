package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenRoutingVersionRestoreAndAtomicity(t *testing.T) {
	truncateTables(t)
	require.NoError(t, MigrateTokenRouting(DB))
	require.NoError(t, MigrateTokenRouting(DB))
	require.NoError(t, DB.Create(&User{Id: 55, Username: "routing-owner", Status: 1}).Error)
	token := Token{UserId: 55, Key: "routing-atomic-key", Name: "old", Group: "auto", AutoGroups: `["revoked","vip"]`, CrossGroupRetry: true, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, token.Insert())
	row, err := GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	require.Nil(t, row, "no migration of existing keys")
	policy := smartrouting.DefaultPolicy()
	write := func(expected int64, p *smartrouting.Policy, restore bool) error {
		return DB.Transaction(func(tx *gorm.DB) error {
			_, err := SaveTokenRoutingTx(tx, &token, p, expected, nil, false, true, restore)
			return err
		})
	}
	require.NoError(t, write(0, &policy, false))
	require.ErrorIs(t, write(0, &policy, false), ErrRoutingVersionConflict)
	require.NoError(t, write(1, nil, true))
	row, err = GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), row.Version)
	assert.Equal(t, "legacy", row.Mode)
	assert.False(t, row.AllowSelfWrite)
	reloaded, err := GetTokenById(token.Id)
	require.NoError(t, err)
	assert.Equal(t, `["revoked","vip"]`, reloaded.AutoGroups)
	assert.True(t, reloaded.CrossGroupRetry)
	err = DB.Transaction(func(tx *gorm.DB) error {
		_, err := SaveTokenRoutingTx(tx, &token, &policy, 2, nil, false, true, false)
		if err != nil {
			return err
		}
		return errors.New("injected rollback after routing write")
	})
	require.Error(t, err)
	row, err = GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), row.Version)
	create := Token{UserId: 55, Key: "routing-create-rollback"}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := create.InsertTx(tx); err != nil {
			return err
		}
		_, err := SaveTokenRoutingTx(tx, &create, &policy, 1, nil, false, false, false)
		return err
	})
	require.ErrorIs(t, err, ErrRoutingVersionConflict)
	var count int64
	require.NoError(t, DB.Model(&Token{}).Where("key = ?", create.Key).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, token.Delete())
	row, err = GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.Nil(t, row)
	require.Error(t, write(0, &policy, false), "deleted key cannot regain a configuration")
}

func TestTokenRoutingLegacyToggleCaptureAndCorruption(t *testing.T) {
	truncateTables(t)
	require.NoError(t, MigrateTokenRouting(DB))
	require.NoError(t, DB.Create(&User{Id: 56, Username: "legacy-toggle-owner", AffCode: "legacy-toggle", Status: 1}).Error)
	token := Token{UserId: 56, Key: "legacy-toggle-key", Group: "auto", AutoGroups: `[]`, UnlimitedQuota: true, ExpiredTime: -1}
	require.NoError(t, token.Insert())
	enabled := true
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := SaveTokenRoutingTx(tx, &token, nil, 0, &enabled, false, false, false)
		return err
	}))
	row, err := GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.Empty(t, row.LegacySnapshot)
	policy := smartrouting.DefaultPolicy()
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := SaveTokenRoutingTx(tx, &token, &policy, 1, nil, false, true, false)
		return err
	}))
	row, err = GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.NotEmpty(t, row.LegacySnapshot)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, err := SaveTokenRoutingTx(tx, &token, nil, 2, nil, false, true, true)
		return err
	}))
	row, err = GetTokenRouting(DB, token.Id)
	require.NoError(t, err)
	assert.Equal(t, "legacy", row.Mode)
	assert.Equal(t, `[]`, token.AutoGroups)
	require.NoError(t, DB.Model(row).Update("policy_json", "{}").Error)
	_, err = GetTokenRouting(DB, token.Id)
	require.ErrorContains(t, err, "invalid stored legacy")
	require.NoError(t, DB.Model(row).Updates(map[string]any{"policy_json": "", "legacy_snapshot": `{"group":null,"auto_groups_raw":"","cross_group_retry":false}`}).Error)
	_, err = GetTokenRouting(DB, token.Id)
	require.ErrorContains(t, err, "invalid stored legacy")
}

func TestTokenRoutingAuthUsesDBBalances(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{UserId: 57, Key: "routing-db-authority", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, DB.Create(&token).Error)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	status, err := cacheTryReserveTokenQuota(token.Id, token.Key, 100)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, status)
	loaded, err := ValidateUserToken(token.Key)
	require.NoError(t, err, "an exhausted Redis snapshot must not reject a funded DB key")
	assert.Equal(t, 100, loaded.RemainQuota)
	require.NoError(t, DB.Model(&token).Update("remain_quota", 0).Error)
	_, err = ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NoError(t, DB.Model(&token).Updates(map[string]any{"remain_quota": 100, "status": common.TokenStatusDisabled}).Error)
	_, err = ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid, "cached enabled metadata must not bypass DB revocation")
}
