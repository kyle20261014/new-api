package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrandDefaultsAndPersistedOverrides(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)
	previousName, previousLogo, previousOptions := common.SystemName, common.Logo, common.OptionMap
	previousPasskey := *system_setting.GetPasskeySettings()
	t.Cleanup(func() {
		common.SystemName, common.Logo, common.OptionMap = previousName, previousLogo, previousOptions
		*system_setting.GetPasskeySettings() = previousPasskey
	})

	InitOptionMap()
	assert.Equal(t, "魔丸AI", common.OptionMap["SystemName"])
	assert.Equal(t, "/logo.png", common.OptionMap["Logo"])
	assert.Equal(t, "魔丸AI", system_setting.PasskeySettingsSnapshot().RPDisplayName)
	var count int64
	require.NoError(t, db.Model(&Option{}).Count(&count).Error)
	assert.Zero(t, count, "defaults must not overwrite persisted settings")

	for _, key := range []string{"SystemName", "passkey.rp_display_name"} {
		require.NoError(t, db.Create(&Option{Key: key, Value: "New API"}).Error)
	}
	InitOptionMap()
	assert.Equal(t, "魔丸AI", common.SystemName)
	assert.Equal(t, "魔丸AI", common.OptionMap["SystemName"])
	assert.Equal(t, "魔丸AI", common.OptionMap["passkey.rp_display_name"])
	assert.Equal(t, "魔丸AI", system_setting.PasskeySettingsSnapshot().RPDisplayName)
	assert.Equal(t, "New API", requireOptionValue(t, db, "SystemName"))
	assert.Equal(t, "New API", requireOptionValue(t, db, "passkey.rp_display_name"))

	for key, value := range map[string]string{
		"SystemName": "Custom Brand", "Logo": "https://example.com/custom.png", "passkey.rp_display_name": "Custom Passkey",
	} {
		require.NoError(t, UpdateOption(key, value))
	}
	common.SystemName, common.Logo = "魔丸AI", "/logo.png"
	InitOptionMap()
	assert.Equal(t, "Custom Brand", common.SystemName)
	assert.Equal(t, "https://example.com/custom.png", common.Logo)
	assert.Equal(t, "Custom Passkey", system_setting.PasskeySettingsSnapshot().RPDisplayName)
	assert.Equal(t, "Custom Brand", common.OptionMap["SystemName"])
	assert.Equal(t, "https://example.com/custom.png", common.OptionMap["Logo"])
	assert.Equal(t, "Custom Brand", requireOptionValue(t, db, "SystemName"))

	require.NoError(t, UpdateOption("Logo", ""))
	common.Logo = "/logo.png"
	InitOptionMap()
	assert.Empty(t, common.Logo, "an explicitly empty logo must remain configurable")
	assert.Empty(t, common.OptionMap["Logo"])
}
