package setting

import (
	"errors"
	"math"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

const IdataRiverDefaultProducts = `[{"sku_id":"6adb0f2e7cea3a5b4c41745e","name":"支付10U","price":10,"quota":10},{"sku_id":"6adb0f69c5c1b963f68624a4","name":"支付20U","price":20,"quota":20},{"sku_id":"6adb0f83c5c1b963f68624a5","name":"支付30U","price":30,"quota":30},{"sku_id":"6adc23b47cea3a5b4c418832","name":"支付50U","price":50,"quota":50},{"sku_id":"6adc244cc5c1b963f68638ba","name":"100U","price":100,"quota":100}]`
const IdataRiverDefaultOrigin = "https://mw.vistalineglobal.com"

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
		"IdataRiverEnabled": "false", "IdataRiverApiKey": "",
		"IdataRiverMerchantId": "6adaf86e4a9d53fa7ffbfa10", "IdataRiverProjectId": "6adb0ecdc5c1b963f6862493",
		"IdataRiverProducts": IdataRiverDefaultProducts, "IdataRiverCallbackOrigin": IdataRiverDefaultOrigin,
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
	cfg := IdataRiverConfig{
		Enabled: values["IdataRiverEnabled"] == "true", APIKey: values["IdataRiverApiKey"],
		MerchantID: values["IdataRiverMerchantId"], ProjectID: values["IdataRiverProjectId"], CallbackOrigin: values["IdataRiverCallbackOrigin"],
	}
	if ValidateIdataRiverOption("IdataRiverProducts", values["IdataRiverProducts"]) == nil {
		_ = common.UnmarshalJsonStr(values["IdataRiverProducts"], &cfg.Products)
	}
	for i := range cfg.Products {
		cfg.Products[i].Currency = "USD"
	}
	return cfg
}

func (cfg IdataRiverConfig) Configured() bool {
	return strings.TrimSpace(cfg.APIKey) != "" && idataRiverIDPattern.MatchString(cfg.MerchantID) &&
		idataRiverIDPattern.MatchString(cfg.ProjectID) && ValidateIdataRiverOrigin(cfg.CallbackOrigin) == nil && len(cfg.Products) > 0
}

func ValidateIdataRiverOrigin(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() != "" {
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
		return ValidateIdataRiverOrigin(value)
	case "IdataRiverProducts":
		var products []IdataRiverProduct
		if len(value) > 65536 || common.UnmarshalJsonStr(value, &products) != nil || products == nil || len(products) > 100 {
			return common.NewMessage("Invalid iDataRiver product configuration")
		}
		seen := make(map[string]bool, len(products))
		for _, product := range products {
			if !idataRiverIDPattern.MatchString(product.SKUID) || seen[product.SKUID] || strings.TrimSpace(product.Name) == "" || len(product.Name) > 200 || math.IsNaN(product.Price) || math.IsInf(product.Price, 0) || product.Price <= 0 || product.Price > 1e9 {
				return common.NewMessage("Invalid iDataRiver product configuration")
			}
			if _, err := IdataRiverProductQuota(product); err != nil {
				return err
			}
			seen[product.SKUID] = true
		}
	default:
		if strings.HasPrefix(key, "IdataRiver") {
			return errors.New("unknown iDataRiver option")
		}
	}
	return nil
}
