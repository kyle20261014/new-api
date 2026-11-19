package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// HomepageModel deliberately excludes prices, groups, channels and credentials.
type HomepageModel struct {
	ID                     string                  `json:"id"`
	Icon                   string                  `json:"icon,omitempty"`
	VendorID               int                     `json:"vendor_id,omitempty"`
	VendorName             string                  `json:"vendor_name,omitempty"`
	VendorIcon             string                  `json:"vendor_icon,omitempty"`
	CatalogCreatedTime     int64                   `json:"catalog_created_time,omitempty"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
	CanChat                bool                    `json:"can_chat"`
}

func homepageModels(pricing []model.Pricing, vendors []model.PricingVendor, publicGroups map[string]string) []HomepageModel {
	vendorMap := make(map[int]model.PricingVendor, len(vendors))
	for _, vendor := range vendors {
		vendorMap[vendor.ID] = vendor
	}
	public := filterPricingByUsableGroups(pricing, publicGroups)
	result := make([]HomepageModel, 0, len(public))
	for _, item := range public {
		vendor := vendorMap[item.VendorID]
		result = append(result, HomepageModel{
			ID: item.ModelName, Icon: item.Icon, VendorID: vendor.ID,
			VendorName: vendor.Name, VendorIcon: vendor.Icon,
			CatalogCreatedTime:     item.CatalogCreatedTime,
			SupportedEndpointTypes: item.SupportedEndpointTypes,
			CanChat:                item.DeclaredChatEndpoint,
		})
	}
	return result
}

func GetHomepageModels(c *gin.Context) {
	// Always use anonymous visibility, even when the request has an identity.
	pricing := model.GetPricing()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    homepageModels(pricing, model.GetVendors(), service.GetUserUsableGroups("")),
	})
}
