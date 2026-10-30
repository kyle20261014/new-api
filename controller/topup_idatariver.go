package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

var newIdataRiverClient = service.NewIdataRiverClient

func GetIdataRiverProducts(c *gin.Context) {
	cfg := setting.GetIdataRiverConfig()
	products := cfg.Products
	if products == nil {
		products = []setting.IdataRiverProduct{}
	}
	common.ApiSuccess(c, gin.H{"enabled": isIdataRiverTopUpEnabled(), "products": products})
}

func idataRiverOrderProjection(topup *model.TopUp, snapshot *model.IdataRiverOrder, order *service.IdataRiverOrderInfo) gin.H {
	methods := make([]gin.H, 0)
	if order != nil && order.Status == "NEW" && snapshot.RefundStatus == model.IdataRiverRefundNone && topup.Status == common.TopUpStatusPending {
		for _, payment := range order.Payments {
			if payment.Enabled && payment.Method != "" && payment.Name != "" {
				methods = append(methods, gin.H{"method": payment.Method, "name": payment.Name})
			}
		}
	}
	price, _ := decimal.NewFromString(snapshot.PriceUSD)
	quota, _ := decimal.NewFromString(snapshot.QuotaUSD)
	return gin.H{"trade_no": topup.TradeNo, "status": topup.Status, "price": price.InexactFloat64(), "quota": quota.InexactFloat64(), "currency": "USD", "payment_methods": methods, "refund_status": snapshot.RefundStatus}
}

func CreateIdataRiverOrder(c *gin.Context) {
	var request struct {
		SKUID string `json:"sku_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if common.DecodeJson(c.Request.Body, &request) != nil || request.SKUID == "" {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	cfg := setting.GetIdataRiverConfig()
	if !isIdataRiverTopUpEnabled() {
		common.ApiErrorT(c, "iDataRiver top-up is unavailable")
		return
	}
	var product *setting.IdataRiverProduct
	for i := range cfg.Products {
		if cfg.Products[i].SKUID == request.SKUID {
			product = &cfg.Products[i]
			break
		}
	}
	if product == nil {
		common.ApiErrorT(c, "Invalid iDataRiver product")
		return
	}
	quota, err := setting.IdataRiverProductQuota(*product)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.ValidateTopUpQuotaCapacity(c.GetInt("id"), quota); err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	client := newIdataRiverClient(cfg.APIKey, model.GetUserLanguage(c.GetInt("id")))
	if err = client.ValidateProduct(ctx, cfg, *product); err != nil {
		common.ApiError(c, err)
		return
	}
	// Independent secrets: the public local trade number must not reveal the
	// anonymous contactInfo credential that can retrieve digital goods.
	var entropy [48]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		common.ApiErrorT(c, "Failed to create order")
		return
	}
	tradeNo := "IDR" + hex.EncodeToString(entropy[:16])
	topup := &model.TopUp{UserId: c.GetInt("id"), Amount: int64(quota), Money: product.Price, TradeNo: tradeNo, PaymentMethod: model.PaymentProviderIdataRiver, PaymentProvider: model.PaymentProviderIdataRiver, CreateTime: common.GetTimestamp(), Status: common.TopUpStatusPending}
	snapshot := &model.IdataRiverOrder{TradeNo: tradeNo, MerchantID: cfg.MerchantID, ProjectID: cfg.ProjectID, SKUID: product.SKUID, PriceUSD: decimal.NewFromFloat(product.Price).String(), QuotaUSD: decimal.NewFromFloat(product.Quota).String(), Quota: quota, CallbackOrigin: cfg.CallbackOrigin}
	if err = model.CreateIdataRiverOrder(ctx, topup, snapshot); err != nil {
		common.ApiErrorT(c, "Failed to create order")
		return
	}
	platformID, err := client.CreateOrder(ctx, cfg.ProjectID, product.SKUID, hex.EncodeToString(entropy[16:]))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err = model.AssociateIdataRiverOrder(ctx, tradeNo, platformID); err != nil {
		common.ApiErrorT(c, "Failed to save iDataRiver order")
		return
	}
	snapshot.PlatformOrderID = &platformID
	order, err := client.GetOrder(ctx, platformID)
	if err == nil {
		err = order.Verify(snapshot)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, idataRiverOrderProjection(topup, snapshot, order))
}

func loadOwnedIdataRiverOrder(c *gin.Context) (*model.TopUp, *model.IdataRiverOrder, bool) {
	var request struct {
		TradeNo string `json:"trade_no"`
		Method  string `json:"method"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if common.DecodeJson(c.Request.Body, &request) != nil || request.TradeNo == "" || len(request.TradeNo) > 64 || c.GetInt("id") <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, nil, false
	}
	topup, snapshot, err := model.GetIdataRiverOrder(c.Request.Context(), request.TradeNo, c.GetInt("id"))
	if err != nil || snapshot.PlatformOrderID == nil {
		common.ApiErrorT(c, "iDataRiver order not found")
		return nil, nil, false
	}
	c.Set("idatariver_payment_method", request.Method)
	return topup, snapshot, true
}

// Both public callbacks and owner checks use this verification/settlement path.
func reconcileIdataRiver(ctx context.Context, snapshot *model.IdataRiverOrder, client *service.IdataRiverClient, callerIP string) (*model.TopUp, *model.IdataRiverOrder, *service.IdataRiverOrderInfo, error) {
	order, err := client.GetOrder(ctx, *snapshot.PlatformOrderID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = order.Verify(snapshot); err != nil {
		return nil, nil, nil, err
	}
	if err = model.ReconcileIdataRiverOrder(ctx, snapshot.TradeNo, *snapshot.PlatformOrderID, order.Status, callerIP); err != nil {
		return nil, nil, nil, common.NewMessage("Failed to reconcile iDataRiver order")
	}
	topup, current, err := model.GetIdataRiverOrder(ctx, snapshot.TradeNo, 0)
	return topup, current, order, err
}

func CheckIdataRiverOrder(c *gin.Context) {
	_, snapshot, ok := loadOwnedIdataRiverOrder(c)
	if !ok {
		return
	}
	cfg := setting.GetIdataRiverConfig()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	client := newIdataRiverClient(cfg.APIKey, model.GetUserLanguage(c.GetInt("id")))
	topup, current, order, err := reconcileIdataRiver(ctx, snapshot, client, c.ClientIP())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, idataRiverOrderProjection(topup, current, order))
}

func PayIdataRiverOrder(c *gin.Context) {
	topup, snapshot, ok := loadOwnedIdataRiverOrder(c)
	if !ok {
		return
	}
	cfg := setting.GetIdataRiverConfig()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	client := newIdataRiverClient(cfg.APIKey, model.GetUserLanguage(c.GetInt("id")))
	order, err := client.GetOrder(ctx, *snapshot.PlatformOrderID)
	if err == nil {
		err = order.Verify(snapshot)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if topup.Status != common.TopUpStatusPending || snapshot.RefundStatus != model.IdataRiverRefundNone || order.Status != "NEW" {
		common.ApiErrorT(c, "iDataRiver order is not payable")
		return
	}
	method := c.GetString("idatariver_payment_method")
	allowed := false
	for _, payment := range order.Payments {
		if payment.Enabled && payment.Method != "" && payment.Method == method {
			allowed = true
			break
		}
	}
	if !allowed {
		common.ApiErrorT(c, "Payment method does not exist")
		return
	}
	pay, err := client.Pay(ctx, snapshot, method)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"pay_url": pay.URL, "pay_currency": pay.Currency, "amount": pay.Amount.InexactFloat64()})
}

func IdataRiverNotify(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var notification struct {
		ID     string `json:"id"`
		Event  string `json:"event"`
		Code   *int   `json:"code"`
		Result *struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if common.DecodeJson(c.Request.Body, &notification) != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	// Payment callbacks wrap the order in result; retain flat event-hook support.
	// Only the ID is used here. Status and amounts still come from GetOrder.
	if notification.Code != nil || notification.Result != nil {
		if notification.Code == nil || *notification.Code != 0 || notification.Result == nil || (notification.ID != "" && notification.ID != notification.Result.ID) {
			c.Status(http.StatusBadRequest)
			return
		}
		notification.ID = notification.Result.ID
	}
	if notification.ID == "" || len(notification.ID) > 64 {
		c.Status(http.StatusBadRequest)
		return
	}
	if notification.Event != "" && notification.Event != "ORDER_COMPLETED" && notification.Event != "ORDER_REFUND" {
		c.Status(http.StatusOK)
		return
	}
	tradeNo, err := model.GetIdataRiverTradeNo(ctx, notification.ID)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	_, snapshot, err := model.GetIdataRiverOrder(ctx, tradeNo, 0)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	client := newIdataRiverClient(setting.GetIdataRiverConfig().APIKey, "en")
	if _, _, _, err = reconcileIdataRiver(ctx, snapshot, client, c.ClientIP()); err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Status(http.StatusOK)
}
