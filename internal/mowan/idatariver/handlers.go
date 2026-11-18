package idatariver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_:-]{8,128}$`)

func paymentEnabled(cfg setting.IdataRiverConfig) bool {
	return operation_setting.IsPaymentComplianceConfirmed() && cfg.Enabled && cfg.Configured()
}

func Products(c *gin.Context) {
	cfg := setting.GetIdataRiverConfig()
	products := []setting.IdataRiverProduct{}
	enabled := paymentEnabled(cfg)
	if enabled {
		products = cfg.Products
	}
	common.ApiSuccess(c, gin.H{"enabled": enabled, "products": products})
}

func projection(t *model.TopUp, s *model.IdataRiverOrder, info *orderInfo, queryError bool) *model.IdataRiverSummary {
	view := model.IdataRiverOrderSummary(t, s)
	view.QueryError = queryError
	if queryError || info == nil {
		return view
	}
	if s.IsLegacy() && t.Status == common.TopUpStatusPending && s.RefundStatus == model.IdataRiverRefundNone && s.CreditedQuota == 0 && info.Status == "NEW" {
		view.PlatformStatus = "NEW"
	}
	cfg := setting.GetIdataRiverConfig()
	view.CanPay = paymentEnabled(cfg) && t.Status == common.TopUpStatusPending && s.PlatformOrderID != nil && s.CreationState == "associated" && view.PlatformStatus == "NEW" && s.CreditedQuota == 0 && s.RefundStatus == model.IdataRiverRefundNone
	if view.CanPay {
		for _, p := range info.Payments {
			if p.Enabled && p.Method != "" && len(p.Method) <= 64 && len(p.Name) <= 200 {
				view.PaymentMethods = append(view.PaymentMethods, model.IdataRiverPaymentMethod{Method: p.Method, Name: p.Name})
			}
		}
		view.CanPay = len(view.PaymentMethods) > 0
	}
	return view
}

func applyVerified(ctx context.Context, t *model.TopUp, s *model.IdataRiverOrder, info *orderInfo, ip string) error {
	if err := info.verify(s); err != nil {
		return err
	}
	if s.PlatformOrderID == nil {
		if err := model.AssociateIdataRiverOrder(ctx, s.TradeNo, info.ID); err != nil {
			return err
		}
	}
	return model.ReconcileIdataRiverOrder(ctx, t.TradeNo, info.ID, info.Status, ip)
}

func refresh(ctx context.Context, cfg setting.IdataRiverConfig, t *model.TopUp, s *model.IdataRiverOrder, ip string) (*orderInfo, error) {
	api := client{key: cfg.APIKey}
	var info *orderInfo
	var err error
	if s.IsLegacy() && s.CreationState != "associated" {
		return nil, common.NewMessage("iDataRiver order not found")
	}
	if s.PlatformOrderID == nil {
		info, err = api.recoverOrder(ctx, s)
	} else {
		info, err = api.getOrder(ctx, *s.PlatformOrderID)
	}
	if err != nil {
		return nil, err
	}
	if err = applyVerified(ctx, t, s, info, ip); err != nil {
		return nil, err
	}
	return info, nil
}

// Even failed upstream calls return the persisted local attempt. A timeout is
// not evidence of an unpaid/expired order and must never trigger order/add.
func respondOrder(c *gin.Context, t *model.TopUp, s *model.IdataRiverOrder, info *orderInfo, queryError bool) {
	freshT, freshS, err := model.GetIdataRiverOrder(c.Request.Context(), t.TradeNo, "", t.UserId)
	if err == nil {
		t, s = freshT, freshS
	} else {
		queryError = true
	}
	common.ApiSuccess(c, projection(t, s, info, queryError))
}

func CreateOrder(c *gin.Context) {
	var req struct {
		SKUID     string `json:"sku_id"`
		RequestID string `json:"request_id"`
	}
	if common.DecodeJson(c.Request.Body, &req) != nil || !requestIDPattern.MatchString(req.RequestID) || c.GetInt("id") <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetInt("id")
	t, s, err := model.GetIdataRiverOrder(ctx, "", req.RequestID, userID)
	cfg := setting.GetIdataRiverConfig()
	if err == nil {
		if s.SKUID != req.SKUID {
			common.ApiErrorT(c, "Request ID is already used for another product")
			return
		}
		info, queryErr := refresh(ctx, cfg, t, s, c.ClientIP())
		respondOrder(c, t, s, info, queryErr != nil)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorT(c, "Failed to load iDataRiver order")
		return
	}
	if !paymentEnabled(cfg) {
		common.ApiErrorT(c, "iDataRiver is not configured")
		return
	}
	var product *setting.IdataRiverProduct
	for i := range cfg.Products {
		if cfg.Products[i].SKUID == req.SKUID {
			product = &cfg.Products[i]
			break
		}
	}
	if product == nil || req.SKUID == setting.IdataRiverTestSKU {
		common.ApiErrorT(c, "Invalid iDataRiver product configuration")
		return
	}
	quota, err := setting.IdataRiverProductQuota(*product)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.ValidateTopUpQuotaCapacity(userID, quota); err != nil {
		common.ApiError(c, err)
		return
	}
	api := client{key: cfg.APIKey}
	if err = api.validateProduct(ctx, cfg, *product); err != nil {
		common.ApiError(c, err)
		return
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		common.ApiErrorT(c, "Failed to create iDataRiver order")
		return
	}
	tradeNo := "idr-" + uuid.NewString()
	s = &model.IdataRiverOrder{TradeNo: tradeNo, RequestID: req.RequestID, UserID: userID, ContactInfo: hex.EncodeToString(secret[:]), MerchantID: cfg.MerchantID, ProjectID: cfg.ProjectID, SKUID: product.SKUID, Name: product.Name, PriceUSD: decimal.NewFromFloat(product.Price).String(), QuotaUSD: decimal.NewFromFloat(product.Quota).String(), Currency: "USD", Quantity: 1, Quota: quota, CallbackOrigin: cfg.CallbackOrigin}
	// Amount is a legacy integer column. iDataRiver history displays QuotaUSD
	// from its summary; all settlement uses the bounded quota-unit snapshot.
	t = &model.TopUp{UserId: userID, TradeNo: tradeNo, Amount: int64(quota), Money: product.Price, PaymentMethod: model.PaymentProviderIdataRiver, PaymentProvider: model.PaymentProviderIdataRiver, CreateTime: common.GetTimestamp(), Status: common.TopUpStatusPending}
	created, err := model.CreateIdataRiverAttempt(ctx, t, s)
	if err != nil {
		common.ApiErrorT(c, "Failed to create iDataRiver order")
		return
	}
	if !created {
		t, s, err = model.GetIdataRiverOrder(ctx, "", req.RequestID, userID)
		if err != nil {
			common.ApiErrorT(c, "Failed to load iDataRiver order")
			return
		}
		if s.SKUID != req.SKUID {
			common.ApiErrorT(c, "Request ID is already used for another product")
			return
		}
		info, queryErr := refresh(ctx, cfg, t, s, c.ClientIP())
		respondOrder(c, t, s, info, queryErr != nil)
		return
	}
	platformID, createErr := api.createOrder(ctx, s)
	var info *orderInfo
	if createErr == nil {
		info, createErr = api.getOrder(ctx, platformID)
		if createErr == nil && info.ID != platformID {
			createErr = common.NewMessage("Invalid iDataRiver response")
		}
		if createErr == nil {
			createErr = applyVerified(ctx, t, s, info, c.ClientIP())
		}
	}
	if createErr != nil {
		persistCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = model.MarkIdataRiverUnknown(persistCtx, tradeNo)
	}
	respondOrder(c, t, s, info, createErr != nil)
}

func Check(c *gin.Context) {
	var req struct {
		TradeNo   string `json:"trade_no"`
		RequestID string `json:"request_id"`
	}
	if common.DecodeJson(c.Request.Body, &req) != nil || (req.TradeNo == "") == (req.RequestID == "") || len(req.TradeNo) > 64 || (req.RequestID != "" && !requestIDPattern.MatchString(req.RequestID)) || c.GetInt("id") <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	t, s, err := model.GetIdataRiverOrder(c.Request.Context(), req.TradeNo, req.RequestID, c.GetInt("id"))
	if err != nil {
		common.ApiErrorT(c, "iDataRiver order not found")
		return
	}
	info, err := refresh(c.Request.Context(), setting.GetIdataRiverConfig(), t, s, c.ClientIP())
	respondOrder(c, t, s, info, err != nil)
}

func Pay(c *gin.Context) {
	var req struct {
		TradeNo string `json:"trade_no"`
		Method  string `json:"method"`
	}
	if common.DecodeJson(c.Request.Body, &req) != nil || req.TradeNo == "" || len(req.TradeNo) > 64 || req.Method == "" || len(req.Method) > 64 || c.GetInt("id") <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	ctx := c.Request.Context()
	t, s, err := model.GetIdataRiverOrder(ctx, req.TradeNo, "", c.GetInt("id"))
	if err != nil {
		common.ApiErrorT(c, "iDataRiver order not found")
		return
	}
	cfg := setting.GetIdataRiverConfig()
	if !paymentEnabled(cfg) {
		respondOrder(c, t, s, nil, false)
		return
	}
	info, err := refresh(ctx, cfg, t, s, c.ClientIP())
	if err != nil {
		respondOrder(c, t, s, nil, true)
		return
	}
	t, s, err = model.GetIdataRiverOrder(ctx, req.TradeNo, "", c.GetInt("id"))
	if err != nil {
		common.ApiErrorT(c, "Failed to load iDataRiver order")
		return
	}
	view := projection(t, s, info, false)
	allowed := false
	for _, p := range view.PaymentMethods {
		if p.Method == req.Method {
			allowed = true
			break
		}
	}
	if !view.CanPay || !allowed {
		common.ApiSuccess(c, view)
		return
	}
	payment, err := (client{key: cfg.APIKey}).pay(ctx, s, req.Method)
	if err != nil {
		respondOrder(c, t, s, nil, true)
		return
	}
	var amount *float64
	if payment.Amount != nil {
		value := payment.Amount.InexactFloat64()
		amount = &value
	}
	common.ApiSuccess(c, struct {
		*model.IdataRiverSummary
		PayURL      string   `json:"pay_url"`
		PayCurrency *string  `json:"pay_currency"`
		Amount      *float64 `json:"amount"`
	}{view, payment.URL, payment.Currency, amount})
}

func Notify(c *gin.Context) {
	notify(c, false)
}

func LegacyNotify(c *gin.Context) {
	notify(c, true)
}

func notify(c *gin.Context, legacyEndpoint bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	// Notify is public. Read only a bounded ID; status and every other field in
	// this unauthenticated payload have no authority over the wallet.
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 65537))
	var payload struct {
		ID     string `json:"id"`
		Code   *int   `json:"code"`
		Event  string `json:"event"`
		Result *struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err != nil || len(raw) > 65536 || common.Unmarshal(raw, &payload) != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	id := payload.ID
	if (payload.Code != nil && (*payload.Code != 0 || payload.Result == nil)) || (legacyEndpoint && payload.Result != nil && payload.Code == nil) {
		c.Status(http.StatusBadRequest)
		return
	}
	if payload.Result != nil {
		if id != "" && id != payload.Result.ID {
			c.Status(http.StatusBadRequest)
			return
		}
		id = payload.Result.ID
	}
	if !platformIDPattern.MatchString(id) {
		c.Status(http.StatusBadRequest)
		return
	}
	if legacyEndpoint && payload.Event != "" && payload.Event != "ORDER_COMPLETED" && payload.Event != "ORDER_REFUND" {
		c.Status(http.StatusOK)
		return
	}
	info, err := (client{key: setting.GetIdataRiverConfig().APIKey}).getOrder(ctx, id)
	if err != nil || info.ID != id {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	tradeNo, err := model.GetIdataRiverTradeNo(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) && strings.TrimSpace(info.ContactInfo) != "" {
		tradeNo, err = model.GetIdataRiverTradeNoByContact(ctx, info.ContactInfo)
	}
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	t, s, err := model.GetIdataRiverOrder(ctx, tradeNo, "", 0)
	if err != nil || applyVerified(ctx, t, s, info, c.ClientIP()) != nil || ctx.Err() != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusOK)
}
