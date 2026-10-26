package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	redisserver "github.com/alicebob/miniredis/v2/server"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type idataRiverTransport func(*http.Request) (*http.Response, error)

func (transport idataRiverTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func idataRiverFixture(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN is not configured")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured")
		}
		driver = postgres.Open(dsn)
	default:
		driver = sqlite.Open(filepath.Join(t.TempDir(), "wallet.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldBatch := common.RedisEnabled, common.BatchUpdateEnabled
	oldClient := common.RDB
	oldOptions := maps.Clone(common.OptionMap)
	oldNewClient := newIdataRiverClient
	oldQuota := common.QuotaPerUnit
	common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	common.QuotaPerUnit = 500000
	common.OptionMap = setting.IdataRiverOptionDefaults()
	common.OptionMap["IdataRiverEnabled"] = "true"
	common.OptionMap["IdataRiverApiKey"] = "test-secret-do-not-leak"
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.IdataRiverOrder{}, &model.Log{}, &model.Option{}))
	require.NoError(t, db.AutoMigrate(&model.IdataRiverOrder{}))
	confirmPaymentComplianceForTest(t)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.BatchUpdateEnabled, common.RDB = oldRedis, oldBatch, oldClient
		common.OptionMap, newIdataRiverClient, common.QuotaPerUnit = oldOptions, oldNewClient, oldQuota
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func idataRiverUser(t *testing.T, db *gorm.DB, quota int) model.User {
	t.Helper()
	user := model.User{Username: "idr-" + common.GetRandomString(12), AffCode: common.GetRandomString(12), Quota: quota, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	t.Cleanup(func() {
		var trades []string
		require.NoError(t, db.Model(&model.TopUp{}).Where("user_id = ?", user.Id).Pluck("trade_no", &trades).Error)
		if len(trades) > 0 {
			require.NoError(t, db.Where("trade_no IN ?", trades).Delete(&model.IdataRiverOrder{}).Error)
		}
		require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.TopUp{}).Error)
		require.NoError(t, db.Where("user_id = ?", user.Id).Delete(&model.Log{}).Error)
		require.NoError(t, db.Unscoped().Delete(&user).Error)
	})
	return user
}

func idataRiverSavedOrder(t *testing.T, user model.User) *model.IdataRiverOrder {
	t.Helper()
	cfg := setting.GetIdataRiverConfig()
	id := common.GetRandomString(24)
	snapshot := &model.IdataRiverOrder{TradeNo: "IDR" + common.GetRandomString(32), PlatformOrderID: &id, MerchantID: cfg.MerchantID, ProjectID: cfg.ProjectID, SKUID: cfg.Products[0].SKUID, PriceUSD: "10", QuotaUSD: "10", Quota: 5_000_000, CallbackOrigin: cfg.CallbackOrigin}
	require.NoError(t, model.CreateIdataRiverOrder(context.Background(), &model.TopUp{UserId: user.Id, TradeNo: snapshot.TradeNo, Amount: int64(snapshot.Quota), Money: 10, Status: common.TopUpStatusPending, PaymentProvider: model.PaymentProviderIdataRiver}, snapshot))
	return snapshot
}

func idataRiverRequest(t *testing.T, handler gin.HandlerFunc, userID int, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", userID)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/idatariver/test", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(ctx)
	ctx.Writer.WriteHeaderNow()
	var result map[string]any
	if recorder.Body.Len() > 0 {
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	}
	return recorder, result
}

func TestIdataRiverCheckoutContract(t *testing.T) {
	db := idataRiverFixture(t, "sqlite")
	user := idataRiverUser(t, db, 0)
	cfg := setting.GetIdataRiverConfig()
	var saved model.IdataRiverOrder
	productFault := ""
	calls := 0
	newIdataRiverClient = func(key, language string) *service.IdataRiverClient {
		return &service.IdataRiverClient{APIKey: key, Locale: "en", HTTPClient: &http.Client{Transport: idataRiverTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			assert.Equal(t, "https", req.URL.Scheme)
			assert.Equal(t, "open.idatariver.com", req.URL.Host)
			assert.Equal(t, "Bearer test-secret-do-not-leak", req.Header.Get("Authorization"))
			assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
			var result any
			switch req.URL.Path {
			case "/mapi/project/detail":
				assert.Equal(t, cfg.ProjectID, req.URL.Query().Get("id"))
				sku := map[string]any{"id": cfg.Products[0].SKUID, "owner": cfg.MerchantID, "project": cfg.ProjectID, "status": "ONLINE", "stock": 1, "itemType": "reuse", "pricingScope": "specific"}
				pricing := map[string]any{"status": "ONLINE", "scope": "specific", "scopeItems": []string{cfg.Products[0].SKUID}, "policy": "fixed", "price": 10}
				switch productFault {
				case "zero stock":
					sku["stock"] = 0
				case "offline":
					sku["status"] = "OFFLINE"
				case "foreign owner":
					sku["owner"] = "other"
				case "foreign project":
					sku["project"] = "other"
				case "dynamic":
					pricing["policy"] = "dynamic"
				case "changed price":
					pricing["price"] = 20
				}
				result = map[string]any{"id": cfg.ProjectID, "owner": cfg.MerchantID, "type": "DIGITAL", "status": "ONLINE", "skus": []any{sku}, "pricings": []any{pricing}}
			case "/mapi/order/add":
				require.NoError(t, db.Where("merchant_id = ?", cfg.MerchantID).First(&saved).Error)
				assert.Nil(t, saved.PlatformOrderID)
				var body struct {
					ProjectID string `json:"projectId"`
					SKUID     string `json:"skuId"`
					OrderInfo struct {
						Quantity int    `json:"quantity"`
						Contact  string `json:"contactInfo"`
					} `json:"orderInfo"`
				}
				require.NoError(t, common.DecodeJson(req.Body, &body))
				assert.Equal(t, cfg.ProjectID, body.ProjectID)
				assert.Equal(t, cfg.Products[0].SKUID, body.SKUID)
				assert.Equal(t, 1, body.OrderInfo.Quantity)
				assert.Len(t, body.OrderInfo.Contact, 64)
				assert.NotContains(t, body.OrderInfo.Contact, saved.TradeNo)
				result = map[string]any{"orderId": "platform-order-1"}
			case "/mapi/order/info":
				require.NoError(t, db.Where("trade_no = ?", saved.TradeNo).First(&saved).Error)
				require.NotNil(t, saved.PlatformOrderID)
				result = map[string]any{"id": *saved.PlatformOrderID, "merchant": cfg.MerchantID, "project": map[string]any{"id": cfg.ProjectID}, "sku": map[string]any{"id": cfg.Products[0].SKUID}, "quantity": 1, "oriPrice": 10, "price": 10, "status": "NEW", "mPayments": []any{map[string]any{"method": "alipay", "name": "Alipay", "enabled": true}}}
			case "/mapi/order/pay":
				var body map[string]any
				require.NoError(t, common.DecodeJson(req.Body, &body))
				assert.Equal(t, *saved.PlatformOrderID, body["id"])
				assert.Equal(t, cfg.CallbackOrigin+"/api/user/idatariver/notify", body["callbackUrl"])
				result = map[string]any{"payUrl": "https://pay.example/checkout", "payCurrency": "CNY", "amount": 72.5}
			default:
				t.Errorf("unexpected endpoint %s", req.URL.Path)
			}
			raw, err := common.Marshal(map[string]any{"code": 0, "result": result})
			require.NoError(t, err)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
		})}}
	}
	_, products := idataRiverRequest(t, GetIdataRiverProducts, user.Id, "")
	assert.True(t, products["success"].(bool))
	assert.Len(t, products["data"].(map[string]any)["products"], 5)
	_, created := idataRiverRequest(t, CreateIdataRiverOrder, user.Id, fmt.Sprintf(`{"sku_id":%q,"price":1,"quota":99999999}`, cfg.Products[0].SKUID))
	require.Equal(t, true, created["success"], created)
	data := created["data"].(map[string]any)
	assert.Equal(t, float64(10), data["price"])
	assert.Equal(t, float64(10), data["quota"])
	assert.Equal(t, "USD", data["currency"])
	assert.Equal(t, "pending", data["status"])
	assert.Len(t, data["payment_methods"], 1)
	assert.NotContains(t, fmt.Sprint(created), "test-secret")
	before := calls
	_, otherOwner := idataRiverRequest(t, CheckIdataRiverOrder, user.Id+1000000, fmt.Sprintf(`{"trade_no":%q}`, saved.TradeNo))
	assert.Equal(t, false, otherOwner["success"])
	assert.Equal(t, before, calls)
	_, pay := idataRiverRequest(t, PayIdataRiverOrder, user.Id, fmt.Sprintf(`{"trade_no":%q,"method":"alipay"}`, saved.TradeNo))
	require.Equal(t, true, pay["success"], pay)
	assert.Equal(t, "https://pay.example/checkout", pay["data"].(map[string]any)["pay_url"])
	require.Error(t, model.ManualCompleteTopUp(saved.TradeNo, ""))
	for _, fault := range []string{"zero stock", "offline", "foreign owner", "foreign project", "dynamic", "changed price"} {
		t.Run(fault, func(t *testing.T) {
			productFault = fault
			require.Error(t, newIdataRiverClient(cfg.APIKey, "en").ValidateProduct(context.Background(), cfg, cfg.Products[0]))
		})
	}
	require.NoError(t, model.UpdateOption("IdataRiverApiKey", "   "))
	assert.Equal(t, cfg.APIKey, setting.GetIdataRiverConfig().APIKey)
	_, options := idataRiverRequest(t, GetOptions, user.Id, "")
	assert.NotContains(t, fmt.Sprint(options), cfg.APIKey)
	assert.NotContains(t, fmt.Sprint(options), "IdataRiverApiKey")
	require.Error(t, model.UpdateOption("IdataRiverProducts", `[{"sku_id":"bad","name":"bad","price":10,"quota":-1}]`))
	assert.Len(t, setting.GetIdataRiverConfig().Products, 5)
}

func TestIdataRiverVerifiedReconciliation(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := idataRiverFixture(t, dialect)
			for _, scenario := range []string{"duplicate done and refund", "concurrent duplicate done", "concurrent duplicate refund", "concurrent done and refund", "spent balance", "refund before done", "wallet overflow", "concurrent consumption", "batch without live cache", "batch concurrent consumption", "user missing"} {
				t.Run(scenario, func(t *testing.T) {
					user := idataRiverUser(t, db, 0)
					snapshot := idataRiverSavedOrder(t, user)
					reconcile := func(status string) error {
						return model.ReconcileIdataRiverOrder(context.Background(), snapshot.TradeNo, *snapshot.PlatformOrderID, status, "")
					}
					if scenario == "concurrent done and refund" {
						start := make(chan struct{})
						var wg sync.WaitGroup
						var doneErr, refundErr error
						wg.Go(func() { <-start; doneErr = reconcile("DONE") })
						wg.Go(func() { <-start; refundErr = reconcile("REFUND") })
						close(start)
						wg.Wait()
						require.NoError(t, doneErr)
						require.NoError(t, refundErr)
						require.NoError(t, reconcile("DONE"))
					} else if scenario == "refund before done" {
						require.NoError(t, reconcile("REFUND"))
						require.NoError(t, reconcile("DONE"))
					} else if scenario == "wallet overflow" {
						require.NoError(t, db.Model(&user).Update("quota", common.MaxWalletQuota).Error)
						require.Error(t, reconcile("DONE"))
						require.NoError(t, db.Model(&user).Update("quota", 0).Error)
						require.NoError(t, reconcile("DONE"))
					} else {
						if scenario == "concurrent duplicate done" {
							start := make(chan struct{})
							var wg sync.WaitGroup
							var first, second error
							wg.Go(func() { <-start; first = reconcile("DONE") })
							wg.Go(func() { <-start; second = reconcile("DONE") })
							close(start)
							wg.Wait()
							require.NoError(t, first)
							require.NoError(t, second)
						} else {
							require.NoError(t, reconcile("DONE"))
						}
						require.NoError(t, reconcile("DONE"))
						if scenario == "spent balance" {
							reserved, err := model.TryReserveUserQuota(user.Id, 1)
							require.NoError(t, err)
							require.True(t, reserved)
						}
						if scenario == "user missing" {
							require.NoError(t, db.Delete(&user).Error)
							require.Error(t, reconcile("REFUND"))
							_, current, err := model.GetIdataRiverOrder(context.Background(), snapshot.TradeNo, user.Id)
							require.NoError(t, err)
							assert.Equal(t, model.IdataRiverRefundNone, current.RefundStatus)
							require.NoError(t, db.Unscoped().Model(&user).Update("deleted_at", nil).Error)
						}
						if scenario == "batch without live cache" || scenario == "batch concurrent consumption" {
							common.BatchUpdateEnabled = true
							t.Cleanup(func() { common.BatchUpdateEnabled = false })
							require.NoError(t, model.IncreaseUserQuota(user.Id, 10, false))
							var live model.User
							require.NoError(t, db.First(&live, user.Id).Error)
							require.Equal(t, snapshot.Quota+10, live.Quota, "without Redis, batch mode must persist wallet credit immediately")
							require.NoError(t, model.DecreaseUserQuota(user.Id, 10, false))
							require.NoError(t, db.First(&live, user.Id).Error)
							require.Equal(t, snapshot.Quota, live.Quota, "without Redis, batch mode must persist wallet spending immediately")
						}
						if scenario == "concurrent consumption" || scenario == "batch concurrent consumption" || scenario == "concurrent duplicate refund" {
							start := make(chan struct{})
							var wg sync.WaitGroup
							var refundErr, spendErr error
							var spent bool
							wg.Go(func() { <-start; refundErr = reconcile("REFUND") })
							wg.Go(func() {
								<-start
								if scenario == "concurrent duplicate refund" {
									spendErr = reconcile("REFUND")
								} else {
									spent, spendErr = model.TryReserveUserQuota(user.Id, snapshot.Quota)
								}
							})
							close(start)
							wg.Wait()
							require.NoError(t, refundErr)
							require.NoError(t, spendErr)
							_, current, err := model.GetIdataRiverOrder(context.Background(), snapshot.TradeNo, user.Id)
							require.NoError(t, err)
							if spent {
								assert.Equal(t, model.IdataRiverRefundManualRequired, current.RefundStatus)
							} else {
								assert.Equal(t, model.IdataRiverRefundRecovered, current.RefundStatus)
							}
						} else {
							require.NoError(t, reconcile("REFUND"))
						}
						require.NoError(t, reconcile("REFUND"))
						require.NoError(t, reconcile("DONE"))
					}
					var reloaded model.User
					require.NoError(t, db.First(&reloaded, user.Id).Error)
					topup, current, err := model.GetIdataRiverOrder(context.Background(), snapshot.TradeNo, user.Id)
					require.NoError(t, err)
					switch scenario {
					case "spent balance":
						assert.Equal(t, snapshot.Quota-1, reloaded.Quota)
						assert.Equal(t, model.IdataRiverRefundManualRequired, current.RefundStatus)
					case "refund before done":
						assert.Zero(t, reloaded.Quota)
						assert.Zero(t, current.CreditedQuota)
						assert.Equal(t, model.IdataRiverRefundBeforeCredit, current.RefundStatus)
					case "wallet overflow":
						assert.Equal(t, snapshot.Quota, reloaded.Quota)
					case "batch without live cache":
						assert.Zero(t, reloaded.Quota)
						assert.Equal(t, model.IdataRiverRefundRecovered, current.RefundStatus)
					default:
						assert.Zero(t, reloaded.Quota)
					}
					var count int64
					require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&count).Error)
					if scenario == "concurrent done and refund" {
						require.Contains(t, []int{0, snapshot.Quota}, current.CreditedQuota)
						if current.CreditedQuota == 0 {
							assert.Equal(t, model.IdataRiverRefundBeforeCredit, current.RefundStatus)
							assert.Equal(t, common.TopUpStatusExpired, topup.Status)
						} else {
							assert.Equal(t, model.IdataRiverRefundRecovered, current.RefundStatus)
							assert.Equal(t, common.TopUpStatusSuccess, topup.Status)
						}
					}
					if scenario == "concurrent duplicate refund" {
						assert.Equal(t, snapshot.Quota, current.CreditedQuota)
						assert.Equal(t, common.TopUpStatusSuccess, topup.Status)
					}
					expectedLogs := 1
					if scenario == "refund before done" || (scenario == "concurrent done and refund" && current.CreditedQuota == 0) {
						expectedLogs = 0
					}
					assert.EqualValues(t, expectedLogs, count)
					if scenario == "concurrent duplicate refund" || scenario == "concurrent done and refund" {
						require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Count(&count).Error)
						assert.EqualValues(t, expectedLogs, count)
					}
					history := []*model.TopUp{{TradeNo: snapshot.TradeNo, PaymentProvider: model.PaymentProviderIdataRiver}}
					require.NoError(t, model.PopulateIdataRiverRefundStatus(history))
					assert.Equal(t, current.RefundStatus, history[0].RefundStatus)
				})
			}
		})
	}
}

func TestIdataRiverProtocolRejectsUnverifiedMoney(t *testing.T) {
	snapshot := &model.IdataRiverOrder{PlatformOrderID: new("order"), MerchantID: "merchant", ProjectID: "project", SKUID: "sku", PriceUSD: "10"}
	valid := service.IdataRiverOrderInfo{ID: "order", Merchant: "merchant", Quantity: 1, OriginalPrice: decimal.NewFromInt(10), Price: decimal.NewFromInt(10), Status: "DONE"}
	valid.Project.ID = "project"
	valid.SKU.ID = "sku"
	for _, field := range []string{"valid", "merchant", "project", "sku", "quantity", "original price", "paid price", "order"} {
		t.Run(field, func(t *testing.T) {
			order := valid
			switch field {
			case "merchant":
				order.Merchant = "other"
			case "project":
				order.Project.ID = "other"
			case "sku":
				order.SKU.ID = "other"
			case "quantity":
				order.Quantity = 2
			case "original price":
				order.OriginalPrice = decimal.NewFromInt(1)
			case "paid price":
				order.Price = decimal.NewFromInt(1)
			case "order":
				order.ID = "other"
			}
			if field == "valid" {
				require.NoError(t, order.Verify(snapshot))
			} else {
				require.Error(t, order.Verify(snapshot))
			}
		})
	}
	for _, response := range []struct {
		name    string
		status  int
		raw     string
		timeout bool
	}{
		{name: "missing code", status: 200, raw: `{"result":{"id":"x"}}`},
		{name: "code failure", status: 200, raw: `{"code":1,"msg":"test-secret","result":{"id":"x"}}`},
		{name: "null result", status: 200, raw: `{"code":0,"result":null}`},
		{name: "invalid json", status: 200, raw: `not json`},
		{name: "http error", status: 503, raw: `test-secret`},
		{name: "deadline", timeout: true},
	} {
		t.Run(response.name, func(t *testing.T) {
			client := service.NewIdataRiverClient("test-secret", "zhCN")
			client.HTTPClient = &http.Client{Transport: idataRiverTransport(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, "zh-cn", req.Header.Get("X-Idr-Locale"))
				if response.timeout {
					return nil, context.DeadlineExceeded
				}
				return &http.Response{StatusCode: response.status, Body: io.NopCloser(strings.NewReader(response.raw))}, nil
			})}
			_, err := client.GetOrder(context.Background(), "order")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "test-secret")
		})
	}
	for _, origin := range []string{"http://example.com", "https://127.0.0.1", "https://localhost", "https://example.com/path", "https://example.com?token=x", "https://user:pass@example.com"} {
		require.Error(t, setting.ValidateIdataRiverOrigin(origin))
	}
	require.NoError(t, setting.ValidateIdataRiverOrigin(setting.IdataRiverDefaultOrigin))
}

func TestIdataRiverCallbackAndDisabledOwnerCheck(t *testing.T) {
	db := idataRiverFixture(t, "sqlite")
	user := idataRiverUser(t, db, 0)
	snapshot := idataRiverSavedOrder(t, user)
	common.OptionMap["IdataRiverEnabled"] = "false"
	common.OptionMap["IdataRiverProducts"] = "[]"
	common.QuotaPerUnit = 1
	status, merchant, fail := "DONE", snapshot.MerchantID, false
	newIdataRiverClient = func(key, language string) *service.IdataRiverClient {
		return &service.IdataRiverClient{APIKey: key, HTTPClient: &http.Client{Transport: idataRiverTransport(func(req *http.Request) (*http.Response, error) {
			if fail {
				return nil, errors.New("remote-secret-must-not-escape")
			}
			raw, err := common.Marshal(map[string]any{"code": 0, "result": map[string]any{"id": *snapshot.PlatformOrderID, "merchant": merchant, "project": map[string]any{"id": snapshot.ProjectID}, "sku": map[string]any{"id": snapshot.SKUID}, "quantity": 1, "oriPrice": 10, "price": 10, "status": status}})
			require.NoError(t, err)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}}
	}
	merchant = "forged merchant"
	recorder, _ := idataRiverRequest(t, IdataRiverNotify, 0, fmt.Sprintf(`{"id":%q,"event":"ORDER_COMPLETED","status":"DONE","price":10}`, *snapshot.PlatformOrderID))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	assert.Zero(t, current.Quota)
	merchant = snapshot.MerchantID
	fail = true
	recorder, _ = idataRiverRequest(t, IdataRiverNotify, 0, fmt.Sprintf(`{"id":%q,"event":"ORDER_COMPLETED"}`, *snapshot.PlatformOrderID))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	fail = false
	_, result := idataRiverRequest(t, CheckIdataRiverOrder, user.Id, fmt.Sprintf(`{"trade_no":%q}`, snapshot.TradeNo))
	require.Equal(t, true, result["success"], result)
	assert.Equal(t, "success", result["data"].(map[string]any)["status"])
	require.NoError(t, db.First(&current, user.Id).Error)
	assert.Equal(t, snapshot.Quota, current.Quota, "saved quota survives settings and unit changes")
	recorder, _ = idataRiverRequest(t, IdataRiverNotify, 0, fmt.Sprintf(`{"id":%q,"event":"ORDER_COMPLETED"}`, *snapshot.PlatformOrderID))
	assert.Equal(t, http.StatusOK, recorder.Code)
	status = "REFUND"
	recorder, _ = idataRiverRequest(t, IdataRiverNotify, 0, fmt.Sprintf(`{"id":%q,"event":"ORDER_REFUND"}`, *snapshot.PlatformOrderID))
	assert.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, db.First(&current, user.Id).Error)
	assert.Zero(t, current.Quota)
}

func TestIdataRiverCallbackDeadline(t *testing.T) {
	for _, status := range []string{"DONE", "REFUND"} {
		t.Run(status, func(t *testing.T) {
			db := idataRiverFixture(t, "sqlite")
			user := idataRiverUser(t, db, 0)
			snapshot := idataRiverSavedOrder(t, user)
			if status == "REFUND" {
				require.NoError(t, model.ReconcileIdataRiverOrder(context.Background(), snapshot.TradeNo, *snapshot.PlatformOrderID, "DONE", ""))
			}
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			require.NoError(t, client.HSet(context.Background(), fmt.Sprintf("user:%d", user.Id), "Id", user.Id, "CacheSchema", 2, "Quota", 0).Err())
			cacheCalled := make(chan struct{}, 1)
			stop := make(chan struct{})
			t.Cleanup(func() { close(stop) })
			server.Server().SetPreHook(func(_ *redisserver.Peer, command string, _ ...string) bool {
				if command == "EVAL" {
					select {
					case cacheCalled <- struct{}{}:
					default:
					}
					// Fault injection: a valid cache reply arrives after the callback's remaining budget.
					select {
					case <-time.After(2 * time.Second):
					case <-stop:
					}
				}
				return false
			})
			common.RDB, common.RedisEnabled = client, true
			newIdataRiverClient = func(key, language string) *service.IdataRiverClient {
				return &service.IdataRiverClient{APIKey: key, HTTPClient: &http.Client{Transport: idataRiverTransport(func(req *http.Request) (*http.Response, error) {
					select {
					case <-time.After(3200 * time.Millisecond):
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
					raw, err := common.Marshal(map[string]any{"code": 0, "result": map[string]any{"id": *snapshot.PlatformOrderID, "merchant": snapshot.MerchantID, "project": map[string]any{"id": snapshot.ProjectID}, "sku": map[string]any{"id": snapshot.SKUID}, "quantity": 1, "oriPrice": 10, "price": 10, "status": status}})
					require.NoError(t, err)
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				})}}
			}
			router := gin.New()
			router.POST("/api/user/idatariver/notify", IdataRiverNotify)
			for attempt := range 2 {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/user/idatariver/notify", strings.NewReader(fmt.Sprintf(`{"id":%q}`, *snapshot.PlatformOrderID)))
				started := time.Now()
				router.ServeHTTP(response, request)
				elapsed := time.Since(started)
				t.Logf("callback %s attempt %d HTTP=%d elapsed=%s", status, attempt+1, response.Code, elapsed)
				assert.Less(t, elapsed, 5*time.Second)
				assert.Equal(t, http.StatusOK, response.Code)
			}
			select {
			case <-cacheCalled:
			default:
				t.Fatal("callback did not exercise the slow cache")
			}
			var current model.User
			require.NoError(t, db.First(&current, user.Id).Error)
			expectedQuota, logType := snapshot.Quota, model.LogTypeTopup
			if status == "REFUND" {
				expectedQuota, logType = 0, model.LogTypeRefund
			}
			assert.Equal(t, expectedQuota, current.Quota)
			var logs int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, logType).Count(&logs).Error)
			assert.EqualValues(t, 1, logs, "cache slowness must not lose or duplicate the accounting event")
		})
	}
}

func TestIdataRiverOwnerRefundAfterCacheRehydration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := idataRiverFixture(t, dialect)
			require.NoError(t, db.AutoMigrate(&model.UserSession{}))
			address := os.Getenv("TEST_REDIS_ADDR")
			if address == "" {
				address = miniredis.RunT(t).Addr()
			}
			client := redis.NewClient(&redis.Options{Addr: address, DB: 14, MaxRetries: -1})
			require.NoError(t, client.Ping(context.Background()).Err())
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			common.RDB = client
			user := idataRiverUser(t, db, 0)
			require.NoError(t, db.Model(&user).Updates(map[string]any{"role": common.RoleCommonUser, "group": "default"}).Error)
			sid := "idr-owner-" + common.GetRandomString(24)
			_, credential := createAccessTokenTestSession(t, user.Id, sid)
			t.Cleanup(func() { require.NoError(t, db.Where("sid = ?", sid).Delete(&model.UserSession{}).Error) })
			snapshot := idataRiverSavedOrder(t, user)
			require.NoError(t, model.ReconcileIdataRiverOrder(context.Background(), snapshot.TradeNo, *snapshot.PlatformOrderID, "DONE", ""))
			key := fmt.Sprintf("user:%d", user.Id)
			cacheKeys := []string{key, fmt.Sprintf("auth:user:fence:%d", user.Id), fmt.Sprintf("auth:user:version:%d", user.Id)}
			require.NoError(t, client.Del(context.Background(), cacheKeys...).Err())
			t.Cleanup(func() { client.Del(context.Background(), cacheKeys...); common.RedisEnabled = false })
			common.RedisEnabled, common.BatchUpdateEnabled = true, true
			_, err := model.GetUserCache(user.Id)
			require.NoError(t, err)
			reserved, err := model.TryReserveUserQuota(user.Id, snapshot.Quota)
			require.NoError(t, err)
			require.True(t, reserved)
			// Force the actual session middleware and language lookup to refill the cache.
			require.NoError(t, client.Del(context.Background(), key).Err())
			calls := 0
			newIdataRiverClient = func(key, language string) *service.IdataRiverClient {
				return &service.IdataRiverClient{APIKey: key, HTTPClient: &http.Client{Transport: idataRiverTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					raw, err := common.Marshal(map[string]any{"code": 0, "result": map[string]any{"id": *snapshot.PlatformOrderID, "merchant": snapshot.MerchantID, "project": map[string]any{"id": snapshot.ProjectID}, "sku": map[string]any{"id": snapshot.SKUID}, "quantity": 1, "oriPrice": 10, "price": 10, "status": "REFUND"}})
					require.NoError(t, err)
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				})}}
			}
			router := gin.New()
			router.POST("/api/user/idatariver/check", middleware.DisableCache(), middleware.UserAuth(), CheckIdataRiverOrder)
			body := fmt.Sprintf(`{"trade_no":%q}`, snapshot.TradeNo)
			denied := accessTokenRequest(router, http.MethodPost, "/api/user/idatariver/check", "", "", body)
			require.Equal(t, http.StatusUnauthorized, denied.Code)
			assert.Zero(t, calls, "unauthenticated requests must not query the platform")
			for range 2 {
				response := accessTokenRequest(router, http.MethodPost, "/api/user/idatariver/check", credential, "", body)
				require.Equal(t, http.StatusOK, response.Code)
				var result struct {
					Success bool `json:"success"`
					Data    struct {
						RefundStatus string `json:"refund_status"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
				require.True(t, result.Success, response.Body.String())
				assert.Equal(t, model.IdataRiverRefundManualRequired, result.Data.RefundStatus)
			}
			var current model.User
			require.NoError(t, db.First(&current, user.Id).Error)
			assert.Zero(t, current.Quota, "already-consumed funds must not be reclaimed again")
			quota, err := model.GetUserQuota(user.Id, false)
			require.NoError(t, err)
			assert.Zero(t, quota)
		})
	}
}

func TestIdataRiverRefundLiveCache(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := idataRiverFixture(t, dialect)
			address := os.Getenv("TEST_REDIS_ADDR")
			if address == "" {
				server := miniredis.RunT(t)
				address = server.Addr()
			}
			client := redis.NewClient(&redis.Options{Addr: address, DB: 14, MaxRetries: -1})
			require.NoError(t, client.Ping(context.Background()).Err())
			t.Cleanup(func() { require.NoError(t, client.Close()) })
			common.RDB = client
			for _, scenario := range []string{"recovered", "spent live cache", "cache miss", "stale schema", "invalid quota", "missing quota", "database behind cache", "database rollback", "redis unavailable", "reserve invalid quota", "reserve missing quota", "reserve cache miss", "reserve stale schema", "reserve redis unavailable"} {
				t.Run(scenario, func(t *testing.T) {
					common.RedisEnabled = false
					user := idataRiverUser(t, db, 0)
					snapshot := idataRiverSavedOrder(t, user)
					reconcile := func(status string) error {
						return model.ReconcileIdataRiverOrder(context.Background(), snapshot.TradeNo, *snapshot.PlatformOrderID, status, "")
					}
					require.NoError(t, reconcile("DONE"))
					key := fmt.Sprintf("user:%d", user.Id)
					require.NoError(t, client.HSet(context.Background(), key, "Id", user.Id, "CacheSchema", 2, "Quota", snapshot.Quota).Err())
					t.Cleanup(func() { client.Del(context.Background(), key); common.RedisEnabled = false; common.RDB = client })
					expectedDBQuota := snapshot.Quota
					switch strings.TrimPrefix(scenario, "reserve ") {
					case "spent live cache":
						reserved, err := model.TryReserveUserQuota(user.Id, 1)
						require.NoError(t, err)
						require.True(t, reserved)
						expectedDBQuota--
					case "cache miss":
						require.NoError(t, client.Del(context.Background(), key).Err())
					case "stale schema":
						require.NoError(t, client.HSet(context.Background(), key, "CacheSchema", 1).Err())
					case "invalid quota":
						require.NoError(t, client.HSet(context.Background(), key, "Quota", "invalid").Err())
					case "missing quota":
						require.NoError(t, client.HDel(context.Background(), key, "Quota").Err())
					case "database behind cache":
						expectedDBQuota--
						require.NoError(t, db.Model(&user).Update("quota", expectedDBQuota).Error)
					case "database rollback":
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register("idr-refund-failure", func(tx *gorm.DB) {
							if tx.Statement.Table == "idata_river_orders" {
								tx.AddError(errors.New("test rollback"))
							}
						}))
						t.Cleanup(func() { db.Callback().Update().Remove("idr-refund-failure") })
					case "redis unavailable":
						unavailable := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
						common.RDB = unavailable
						t.Cleanup(func() { unavailable.Close() })
					}
					common.RedisEnabled = true
					if strings.HasPrefix(scenario, "reserve ") {
						common.BatchUpdateEnabled = true
						t.Cleanup(func() { common.BatchUpdateEnabled = false })
						reserved, err := model.TryReserveUserQuota(user.Id, snapshot.Quota)
						require.NoError(t, err, "cache state cannot change a committed database decision")
						require.True(t, reserved)
						reserved, err = model.TryReserveUserQuota(user.Id, 1)
						require.NoError(t, err)
						assert.False(t, reserved)
						expectedDBQuota = 0
					}
					err := reconcile("REFUND")
					retryable := scenario == "database rollback"
					if retryable {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					_, current, loadErr := model.GetIdataRiverOrder(context.Background(), snapshot.TradeNo, user.Id)
					require.NoError(t, loadErr)
					var reloaded model.User
					require.NoError(t, db.First(&reloaded, user.Id).Error)
					if retryable {
						assert.Equal(t, expectedDBQuota, reloaded.Quota, "unverifiable balance must not be partially reclaimed")
						assert.Equal(t, model.IdataRiverRefundNone, current.RefundStatus)
						assert.Zero(t, current.RefundTime)
						assert.Equal(t, fmt.Sprint(snapshot.Quota), client.HGet(context.Background(), key, "Quota").Val(), "a rolled-back transaction must not update cache")
						if scenario == "database rollback" {
							require.NoError(t, db.Callback().Update().Remove("idr-refund-failure"))
						}
						common.RDB = client
						require.NoError(t, db.Model(&user).Update("quota", snapshot.Quota).Error)
						require.NoError(t, client.HSet(context.Background(), key, "Id", user.Id, "CacheSchema", 2, "Quota", snapshot.Quota).Err())
						require.NoError(t, reconcile("REFUND"), "restored authoritative state must allow automatic retry")
					}
					require.NoError(t, reconcile("REFUND"))
					require.NoError(t, reconcile("DONE"))
					_, current, loadErr = model.GetIdataRiverOrder(context.Background(), snapshot.TradeNo, user.Id)
					require.NoError(t, loadErr)
					require.NoError(t, db.First(&reloaded, user.Id).Error)
					if scenario == "spent live cache" || scenario == "database behind cache" || strings.HasPrefix(scenario, "reserve ") {
						assert.Equal(t, expectedDBQuota, reloaded.Quota)
						assert.Equal(t, model.IdataRiverRefundManualRequired, current.RefundStatus)
					} else {
						assert.Zero(t, reloaded.Quota)
						assert.Equal(t, model.IdataRiverRefundRecovered, current.RefundStatus)
					}
				})
			}
		})
	}
}
