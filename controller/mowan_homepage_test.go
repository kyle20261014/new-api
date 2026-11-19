package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestHomepageModelsOnlyExposeAnonymousCatalogFields(t *testing.T) {
	pricing := []model.Pricing{
		{ModelName: "public", EnableGroup: []string{"default"}, VendorID: 1, CatalogCreatedTime: 123, BillingExpr: "private-expression", DeclaredChatEndpoint: true},
		{ModelName: "private", EnableGroup: []string{"vip"}},
		{ModelName: "shared", EnableGroup: []string{"all"}},
	}
	result := homepageModels(pricing, []model.PricingVendor{{ID: 1, Name: "Vendor", Icon: "OpenAI"}}, map[string]string{"default": "Public"})
	require.Len(t, result, 2)
	require.Equal(t, "public", result[0].ID)
	require.Equal(t, int64(123), result[0].CatalogCreatedTime)
	require.Equal(t, "OpenAI", result[0].VendorIcon)
	require.True(t, result[0].CanChat)
	require.Equal(t, "shared", result[1].ID)
	require.False(t, result[1].CanChat)
	encoded, err := common.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"private", "enable_groups", "billing", "channel", "group_ratio"} {
		require.NotContains(t, string(encoded), secret)
	}
	require.Empty(t, homepageModels(pricing, nil, nil))
}
