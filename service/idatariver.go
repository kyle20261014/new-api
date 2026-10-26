package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
)

var idataRiverHTTPClient = &http.Client{
	Timeout:       8 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

type IdataRiverClient struct {
	APIKey     string
	Locale     string
	HTTPClient *http.Client
}

func NewIdataRiverClient(key, language string) *IdataRiverClient {
	locale := "en"
	switch strings.ToLower(language) {
	case "zh", "zhcn", "zh-cn":
		locale = "zh-cn"
	case "zhtw", "zh-tw", "zh-hk":
		locale = "zh-hk"
	case "en", "fr", "ja", "ru":
		locale = strings.ToLower(language)
	}
	return &IdataRiverClient{APIKey: key, Locale: locale, HTTPClient: idataRiverHTTPClient}
}

// request deliberately excludes upstream messages and bodies from errors: orders
// contain digital goods and contactInfo is a usable anonymous query credential.
func (client *IdataRiverClient) request(ctx context.Context, method, path string, body any, result any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = common.Marshal(body)
		if err != nil {
			return common.NewMessage("iDataRiver request failed")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://open.idatariver.com"+path, bytes.NewReader(encoded))
	if err != nil || strings.TrimSpace(client.APIKey) == "" {
		return common.NewMessage("iDataRiver is not configured")
	}
	req.Header.Set("Authorization", "Bearer "+client.APIKey)
	// The platform answers {"code":1000,"msg":"exception"} to a GET that declares
	// a JSON body but sends none, so bound GETs must not set Content-Type.
	if len(encoded) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Idr-Locale", client.Locale)
	response, err := client.HTTPClient.Do(req)
	if err != nil {
		return common.NewMessage("iDataRiver request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return common.NewMessage("iDataRiver request failed")
	}
	const maxResponse = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return common.NewMessage("Invalid iDataRiver response")
	}
	var envelope struct {
		Code   *int            `json:"code"`
		Result json.RawMessage `json:"result"`
	}
	if common.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
		return common.NewMessage("Invalid iDataRiver response")
	}
	if *envelope.Code != 0 {
		return common.NewMessage("iDataRiver request failed")
	}
	if len(envelope.Result) == 0 || string(envelope.Result) == "null" || common.Unmarshal(envelope.Result, result) != nil {
		return common.NewMessage("Invalid iDataRiver response")
	}
	return nil
}

type IdataRiverPaymentMethod struct {
	Method  string `json:"method"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type IdataRiverOrderInfo struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Merchant string `json:"merchant"`
	Project  struct {
		ID string `json:"id"`
	} `json:"project"`
	SKU struct {
		ID string `json:"id"`
	} `json:"sku"`
	Quantity      int                       `json:"quantity"`
	OriginalPrice decimal.Decimal           `json:"oriPrice"`
	Price         decimal.Decimal           `json:"price"`
	Payments      []IdataRiverPaymentMethod `json:"mPayments"`
}

func (order *IdataRiverOrderInfo) Verify(snapshot *model.IdataRiverOrder) error {
	price, err := decimal.NewFromString(snapshot.PriceUSD)
	if err != nil || !price.IsPositive() || snapshot.PlatformOrderID == nil || order.ID != *snapshot.PlatformOrderID || order.Merchant != snapshot.MerchantID || order.Project.ID != snapshot.ProjectID || order.SKU.ID != snapshot.SKUID || order.Quantity != 1 || !order.OriginalPrice.Equal(price) || !order.Price.Equal(price) || order.Status == "" {
		return common.NewMessage("iDataRiver order does not match the saved order")
	}
	return nil
}

func (client *IdataRiverClient) GetOrder(ctx context.Context, id string) (*IdataRiverOrderInfo, error) {
	var order IdataRiverOrderInfo
	err := client.request(ctx, http.MethodGet, "/mapi/order/info?id="+url.QueryEscape(id), nil, &order)
	return &order, err
}

func (client *IdataRiverClient) ValidateProduct(ctx context.Context, cfg setting.IdataRiverConfig, product setting.IdataRiverProduct) error {
	var project struct {
		ID     string `json:"id"`
		Owner  string `json:"owner"`
		Type   string `json:"type"`
		Status string `json:"status"`
		SKUs   []struct {
			ID           string `json:"id"`
			Owner        string `json:"owner"`
			Project      string `json:"project"`
			Status       string `json:"status"`
			Stock        *int   `json:"stock"`
			PricingScope string `json:"pricingScope"`
			ItemType     string `json:"itemType"`
		} `json:"skus"`
		Pricings []struct {
			Status     string          `json:"status"`
			Scope      string          `json:"scope"`
			ScopeItems []string        `json:"scopeItems"`
			Policy     string          `json:"policy"`
			Price      decimal.Decimal `json:"price"`
		} `json:"pricings"`
	}
	if err := client.request(ctx, http.MethodGet, "/mapi/project/detail?id="+url.QueryEscape(cfg.ProjectID), nil, &project); err != nil {
		return err
	}
	if project.ID != cfg.ProjectID || project.Owner != cfg.MerchantID || project.Type != "DIGITAL" || project.Status != "ONLINE" {
		return common.NewMessage("iDataRiver product is unavailable or its price has changed")
	}
	for _, sku := range project.SKUs {
		if sku.ID != product.SKUID {
			continue
		}
		if sku.Owner != cfg.MerchantID || sku.Project != cfg.ProjectID || sku.Status != "ONLINE" || sku.Stock == nil || *sku.Stock < 1 || (sku.ItemType != "reuse" && sku.ItemType != "consumable") || (sku.PricingScope != "global" && sku.PricingScope != "specific") {
			return common.NewMessage("iDataRiver product is unavailable or its price has changed")
		}
		matches := 0
		for _, pricing := range project.Pricings {
			if pricing.Status != "ONLINE" || pricing.Scope != sku.PricingScope || (pricing.Scope == "specific" && !slices.Contains(pricing.ScopeItems, sku.ID)) {
				continue
			}
			if pricing.Policy != "fixed" || !pricing.Price.IsPositive() || !pricing.Price.Equal(decimal.NewFromFloat(product.Price)) {
				return common.NewMessage("iDataRiver product is unavailable or its price has changed")
			}
			matches++
		}
		if matches == 1 {
			return nil
		}
		break
	}
	return common.NewMessage("iDataRiver product is unavailable or its price has changed")
}

func (client *IdataRiverClient) CreateOrder(ctx context.Context, projectID, skuID, contact string) (string, error) {
	var result struct {
		OrderID string `json:"orderId"`
	}
	err := client.request(ctx, http.MethodPost, "/mapi/order/add", map[string]any{
		"projectId": projectID, "skuId": skuID,
		"orderInfo": map[string]any{"quantity": 1, "coupon": "", "contactInfo": contact, "affCode": ""},
	}, &result)
	if err != nil {
		return "", err
	}
	if result.OrderID == "" || len(result.OrderID) > 64 {
		return "", common.NewMessage("Invalid iDataRiver response")
	}
	return result.OrderID, nil
}

type IdataRiverPayInfo struct {
	URL      string          `json:"payUrl"`
	Currency string          `json:"payCurrency"`
	Amount   decimal.Decimal `json:"amount"`
}

func (client *IdataRiverClient) Pay(ctx context.Context, snapshot *model.IdataRiverOrder, method string) (*IdataRiverPayInfo, error) {
	var result IdataRiverPayInfo
	err := client.request(ctx, http.MethodPost, "/mapi/order/pay", map[string]any{
		"id": *snapshot.PlatformOrderID, "method": method,
		"redirectUrl": snapshot.CallbackOrigin + "/wallet?trade_no=" + url.QueryEscape(snapshot.TradeNo),
		"callbackUrl": snapshot.CallbackOrigin + "/api/user/idatariver/notify",
	}, &result)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(result.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(result.Currency) > 20 {
		return nil, common.NewMessage("Invalid iDataRiver payment link")
	}
	// Crypto checkout reports payCurrency and amount as null; both are display
	// values only (settlement stays on the verified order snapshot), so check
	// them when the platform supplies them instead of requiring them.
	if result.Currency != "" || !result.Amount.IsZero() {
		if result.Currency == "" || !result.Amount.IsPositive() || result.Amount.GreaterThan(decimal.NewFromInt(1e12)) {
			return nil, common.NewMessage("Invalid iDataRiver payment link")
		}
	}
	return &result, nil
}
