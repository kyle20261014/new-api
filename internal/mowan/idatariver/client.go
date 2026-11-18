package idatariver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
)

var httpClient = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
var platformIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type client struct{ key string }

// Upstream messages, bodies, URLs and transport errors may contain contactInfo
// (a goods-retrieval credential). Return only local message keys.
func (c client) request(ctx context.Context, method, path string, body, result any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = common.Marshal(body)
		if err != nil {
			return common.NewMessage("iDataRiver request failed")
		}
	}
	if strings.TrimSpace(c.key) == "" {
		return common.NewMessage("iDataRiver is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://open.idatariver.com"+path, bytes.NewReader(encoded))
	if err != nil {
		return common.NewMessage("iDataRiver request failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("X-Idr-Locale", "en")
	if len(encoded) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return common.NewMessage("iDataRiver request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return common.NewMessage("iDataRiver request failed")
	}
	const maxResponse = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return common.NewMessage("Invalid iDataRiver response")
	}
	var envelope struct {
		Code   *int            `json:"code"`
		Result json.RawMessage `json:"result"`
	}
	if common.Unmarshal(raw, &envelope) != nil || envelope.Code == nil || *envelope.Code != 0 || len(envelope.Result) == 0 || string(envelope.Result) == "null" || common.Unmarshal(envelope.Result, result) != nil {
		return common.NewMessage("Invalid iDataRiver response")
	}
	return nil
}

type paymentMethod struct {
	Method  string `json:"method"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type orderInfo struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Merchant    string `json:"merchant"`
	ContactInfo string `json:"contactInfo"`
	Project     struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"project"`
	ProjectType string `json:"projectType"`
	SKU         struct {
		ID string `json:"id"`
	} `json:"sku"`
	Quantity      int              `json:"quantity"`
	OriginalPrice *decimal.Decimal `json:"oriPrice"`
	Price         *decimal.Decimal `json:"price"`
	Currency      string           `json:"currency"`
	Payments      []paymentMethod  `json:"mPayments"`
}

// Bound exponent and coefficient before comparisons can expand hostile numbers.
func validDecimal(value *decimal.Decimal) bool {
	return value != nil && value.Exponent() >= -64 && value.Exponent() <= 12 && value.Coefficient().BitLen() <= 256 && value.IsPositive()
}

func (o *orderInfo) verify(s *model.IdataRiverOrder) error {
	price, err := decimal.NewFromString(s.PriceUSD)
	validBinding := o.ContactInfo == s.ContactInfo && o.ProjectType == "DIGITAL" && o.Project.Type == "DIGITAL"
	if s.IsLegacy() {
		// The old merchant client did not persist contactInfo or require type fields.
		validBinding = s.PlatformOrderID != nil && (o.ProjectType == "" || o.ProjectType == "DIGITAL") && (o.Project.Type == "" || o.Project.Type == "DIGITAL")
	}
	if err != nil || !validDecimal(&price) || !validBinding || s.Currency != "USD" || s.Quantity != 1 || !platformIDPattern.MatchString(o.ID) || (s.PlatformOrderID != nil && o.ID != *s.PlatformOrderID) || s.MerchantID == "" || s.ProjectID == "" || s.SKUID == "" || o.Merchant != s.MerchantID || o.Project.ID != s.ProjectID || o.SKU.ID != s.SKUID || o.Quantity != s.Quantity || !validDecimal(o.OriginalPrice) || !validDecimal(o.Price) || !o.OriginalPrice.Equal(price) || !o.Price.Equal(price) || (o.Currency != "" && o.Currency != "USD") || !slices.Contains([]string{"NEW", "DONE", "EXPIRED", "REFUND"}, o.Status) {
		return common.NewMessage("iDataRiver order does not match the saved order")
	}
	// The merchant API defines fixed DIGITAL product prices in USD; currency
	// is absent in its documented order payload. Explicit conflicts are rejected.
	return nil
}

func (c client) getOrder(ctx context.Context, id string) (*orderInfo, error) {
	var order orderInfo
	err := c.request(ctx, http.MethodGet, "/mapi/order/info?id="+url.QueryEscape(id), nil, &order)
	return &order, err
}

func (c client) validateProduct(ctx context.Context, cfg setting.IdataRiverConfig, product setting.IdataRiverProduct) error {
	var project struct {
		ID       string `json:"id"`
		Owner    string `json:"owner"`
		Type     string `json:"type"`
		Status   string `json:"status"`
		Currency string `json:"currency"`
		SKUs     []struct {
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
			Currency   string          `json:"currency"`
		} `json:"pricings"`
	}
	if err := c.request(ctx, http.MethodGet, "/mapi/project/detail?id="+url.QueryEscape(cfg.ProjectID), nil, &project); err != nil {
		return err
	}
	unavailable := common.NewMessage("iDataRiver product is unavailable or its price has changed")
	if project.ID != cfg.ProjectID || project.Owner != cfg.MerchantID || project.Type != "DIGITAL" || project.Status != "ONLINE" || (project.Currency != "" && project.Currency != "USD") {
		return unavailable
	}
	for _, sku := range project.SKUs {
		if sku.ID != product.SKUID {
			continue
		}
		if sku.Owner != cfg.MerchantID || sku.Project != cfg.ProjectID || sku.Status != "ONLINE" || sku.Stock == nil || *sku.Stock < 1 || (sku.ItemType != "reuse" && sku.ItemType != "consumable") || (sku.PricingScope != "global" && sku.PricingScope != "specific") {
			return unavailable
		}
		matches := 0
		for _, p := range project.Pricings {
			if p.Status != "ONLINE" || p.Scope != sku.PricingScope || (p.Scope == "specific" && !slices.Contains(p.ScopeItems, sku.ID)) {
				continue
			}
			if p.Policy != "fixed" || !validDecimal(&p.Price) || !p.Price.Equal(decimal.NewFromFloat(product.Price)) || (p.Currency != "" && p.Currency != "USD") {
				return unavailable
			}
			matches++
		}
		if matches == 1 {
			return nil
		}
	}
	return unavailable
}

func (c client) createOrder(ctx context.Context, s *model.IdataRiverOrder) (string, error) {
	var result struct {
		OrderID string `json:"orderId"`
	}
	err := c.request(ctx, http.MethodPost, "/mapi/order/add", map[string]any{"projectId": s.ProjectID, "skuId": s.SKUID, "orderInfo": map[string]any{"quantity": 1, "coupon": "", "contactInfo": s.ContactInfo, "affCode": ""}}, &result)
	if err != nil {
		return "", err
	}
	if !platformIDPattern.MatchString(result.OrderID) {
		return "", common.NewMessage("Invalid iDataRiver response")
	}
	return result.OrderID, nil
}

func (c client) recoverOrder(ctx context.Context, s *model.IdataRiverOrder) (*orderInfo, error) {
	var result struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total *int `json:"total"`
	}
	if err := c.request(ctx, http.MethodGet, "/mapi/order/search?query="+url.QueryEscape(s.ContactInfo), nil, &result); err != nil {
		return nil, err
	}
	// ponytail: cap recovery at 20 candidates; add documented paging before supporting larger search results. Incomplete searches never associate or re-create an order.
	if len(result.Items) == 0 || len(result.Items) > 20 || (result.Total != nil && *result.Total > len(result.Items)) {
		return nil, common.NewMessage("iDataRiver order association is not confirmed")
	}
	var match *orderInfo
	seen := make(map[string]bool, len(result.Items))
	for _, item := range result.Items {
		if !platformIDPattern.MatchString(item.ID) {
			return nil, common.NewMessage("Invalid iDataRiver response")
		}
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		order, err := c.getOrder(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		if order.ID != item.ID || order.verify(s) != nil {
			continue
		}
		if match != nil {
			return nil, common.NewMessage("iDataRiver order association is not confirmed")
		}
		match = order
	}
	if match == nil {
		return nil, common.NewMessage("iDataRiver order association is not confirmed")
	}
	return match, nil
}

type payInfo struct {
	URL      string           `json:"payUrl"`
	Currency *string          `json:"payCurrency"`
	Amount   *decimal.Decimal `json:"amount"`
}

func (c client) pay(ctx context.Context, s *model.IdataRiverOrder, method string) (*payInfo, error) {
	if s.PlatformOrderID == nil || setting.ValidateIdataRiverOrigin(s.CallbackOrigin) != nil {
		return nil, common.NewMessage("Invalid iDataRiver payment link")
	}
	callback := "/api/mowan/idatariver/notify"
	if s.IsLegacy() {
		callback = "/api/user/idatariver/notify"
	}
	var result payInfo
	err := c.request(ctx, http.MethodPost, "/mapi/order/pay", map[string]any{"id": *s.PlatformOrderID, "method": method, "redirectUrl": s.CallbackOrigin + "/wallet?trade_no=" + url.QueryEscape(s.TradeNo), "callbackUrl": s.CallbackOrigin + callback}, &result)
	if err != nil {
		return nil, err
	}
	u, parseErr := url.Parse(result.URL)
	if parseErr != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Hostname() == "" || u.Fragment != "" {
		return nil, common.NewMessage("Invalid iDataRiver payment link")
	}
	if result.Currency != nil && (*result.Currency == "" || len(*result.Currency) > 20) {
		return nil, common.NewMessage("Invalid iDataRiver payment link")
	}
	if result.Amount != nil && (!validDecimal(result.Amount) || result.Amount.GreaterThan(decimal.NewFromInt(1e12))) {
		return nil, common.NewMessage("Invalid iDataRiver payment link")
	}
	return &result, nil
}
