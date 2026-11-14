package setting

import (
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

const IdataRiverTestSKU = "6ae41bd5a5befc799a109d36"

var idataRiverIDPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)
var idataRiverDNSPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

type IdataRiverProduct struct {
	SKUID    string  `json:"sku_id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Quota    float64 `json:"quota"`
	Currency string  `json:"currency"`
}

type IdataRiverConfig struct {
	Enabled        bool
	APIKey         string
	MerchantID     string
	ProjectID      string
	CallbackOrigin string
	Products       []IdataRiverProduct
}

func IdataRiverOptionDefaults() map[string]string {
	return map[string]string{
		"IdataRiverEnabled": "false", "IdataRiverApiKey": "", "IdataRiverMerchantId": "",
		"IdataRiverProjectId": "", "IdataRiverCallbackOrigin": "", "IdataRiverProducts": "[]",
	}
}

func GetIdataRiverConfig() IdataRiverConfig {
	values := IdataRiverOptionDefaults()
	common.OptionMapRWMutex.RLock()
	for key := range values {
		if value, ok := common.OptionMap[key]; ok {
			values[key] = value
		}
	}
	common.OptionMapRWMutex.RUnlock()
	cfg := IdataRiverConfig{Enabled: values["IdataRiverEnabled"] == "true", APIKey: values["IdataRiverApiKey"], MerchantID: values["IdataRiverMerchantId"], ProjectID: values["IdataRiverProjectId"], CallbackOrigin: values["IdataRiverCallbackOrigin"]}
	for key, value := range values {
		if ValidateIdataRiverOption(key, value) != nil {
			cfg.Enabled = false
			return cfg
		}
	}
	_ = common.UnmarshalJsonStr(values["IdataRiverProducts"], &cfg.Products)
	for i := range cfg.Products {
		cfg.Products[i].Currency = "USD"
	}
	return cfg
}

func (cfg IdataRiverConfig) Configured() bool {
	return strings.TrimSpace(cfg.APIKey) != "" && idataRiverIDPattern.MatchString(cfg.MerchantID) && idataRiverIDPattern.MatchString(cfg.ProjectID) && ValidateIdataRiverOrigin(cfg.CallbackOrigin) == nil && len(cfg.Products) > 0
}

func ValidateIdataRiverOrigin(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.Port() != "" {
		return common.NewMessage("iDataRiver callback origin must be an HTTPS domain origin")
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || len(host) > 253 || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return common.NewMessage("iDataRiver callback origin must be an HTTPS domain origin")
	}
	for label := range strings.SplitSeq(host, ".") {
		if !idataRiverDNSPattern.MatchString(label) {
			return common.NewMessage("iDataRiver callback origin must be an HTTPS domain origin")
		}
	}
	return nil
}

func IdataRiverProductQuota(product IdataRiverProduct) (int, error) {
	if math.IsNaN(product.Quota) || math.IsInf(product.Quota, 0) || product.Quota <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || common.QuotaPerUnit <= 0 {
		return 0, common.NewMessage("Invalid iDataRiver product configuration")
	}
	value := decimal.NewFromFloat(product.Quota).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if len(decimal.NewFromFloat(product.Quota).String()) > 64 {
		return 0, common.NewMessage("Invalid iDataRiver product configuration")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(value)
	if err != nil || quota <= 0 || !value.Equal(decimal.NewFromInt(int64(quota))) {
		return 0, common.NewMessage("Invalid iDataRiver product configuration")
	}
	return quota, nil
}

func ValidateIdataRiverOption(key, value string) error {
	switch key {
	case "IdataRiverEnabled":
		if value != "true" && value != "false" {
			return common.NewMessage("Invalid parameters")
		}
	case "IdataRiverApiKey":
		if len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return common.NewMessage("Invalid iDataRiver API key")
		}
	case "IdataRiverMerchantId", "IdataRiverProjectId":
		if value != "" && !idataRiverIDPattern.MatchString(value) {
			return common.NewMessage("Invalid iDataRiver merchant or project ID")
		}
	case "IdataRiverCallbackOrigin":
		if value != "" {
			return ValidateIdataRiverOrigin(value)
		}
	case "IdataRiverProducts":
		var products []IdataRiverProduct
		if len(value) > 65536 || common.UnmarshalJsonStr(value, &products) != nil || products == nil || len(products) > 100 {
			return common.NewMessage("Invalid iDataRiver product configuration")
		}
		seen := make(map[string]bool, len(products))
		for _, p := range products {
			if !idataRiverIDPattern.MatchString(p.SKUID) || p.SKUID == IdataRiverTestSKU || seen[p.SKUID] || strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 || math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price <= 0 || p.Price > 1e9 || (p.Currency != "" && p.Currency != "USD") {
				return common.NewMessage("Invalid iDataRiver product configuration")
			}
			if _, err := IdataRiverProductQuota(p); err != nil {
				return err
			}
			if len(decimal.NewFromFloat(p.Price).String()) > 64 {
				return common.NewMessage("Invalid iDataRiver product configuration")
			}
			seen[p.SKUID] = true
		}
	default:
		if strings.HasPrefix(key, "IdataRiver") {
			return common.NewMessage("Invalid parameters")
		}
	}
	return nil
}
