package idatariver

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

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testMerchant = "aaaaaaaaaaaaaaaaaaaaaaaa"
const testProject = "bbbbbbbbbbbbbbbbbbbbbbbb"
const testSKU = "cccccccccccccccccccccccc"
const testQuota = 5_125_000

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var driver gorm.Dialector
	switch dialect {
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN is not configured; MySQL matrix NOT RUN")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN is not configured; PostgreSQL matrix NOT RUN")
		}
		driver = postgres.Open(dsn)
	default:
		driver = sqlite.Open(filepath.Join(t.TempDir(), "wallet.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldBatch, oldQuota := common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit
	common.OptionMapRWMutex.RLock()
	oldOptions := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	oldPayment := *operation_setting.GetPaymentSetting()
	oldHTTP := httpClient
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
	common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = false, false, 500000
	common.OptionMapRWMutex.Lock()
	common.OptionMap = setting.IdataRiverOptionDefaults()
	common.OptionMap["IdataRiverEnabled"] = "true"
	common.OptionMap["IdataRiverApiKey"] = "secret-must-not-appear"
	common.OptionMap["IdataRiverMerchantId"] = testMerchant
	common.OptionMap["IdataRiverProjectId"] = testProject
	common.OptionMap["IdataRiverCallbackOrigin"] = "https://wallet.example.com"
	common.OptionMap["IdataRiverProducts"] = `[{"sku_id":"` + testSKU + `","name":"Fixed package","price":10,"quota":10.25,"currency":"USD"}]`
	common.OptionMapRWMutex.Unlock()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.Option{}))
	// Existing pre-feature schemas upgrade without adding fields to top_ups.
	require.NoError(t, model.MigrateIdataRiver(db))
	require.NoError(t, model.MigrateIdataRiver(db), "repeat startup must be safe")
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = oldRedis, oldBatch, oldQuota
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		*operation_setting.GetPaymentSetting() = oldPayment
		httpClient = oldHTTP
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func testUser(t *testing.T, db *gorm.DB, quota int) model.User {
	t.Helper()
	u := model.User{Username: "idr-" + common.GetRandomString(12), AffCode: common.GetRandomString(12), Quota: quota, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&u).Error)
	t.Cleanup(func() {
		require.NoError(t, db.Where("user_id = ?", u.Id).Delete(&model.IdataRiverOrder{}).Error)
		require.NoError(t, db.Where("user_id = ?", u.Id).Delete(&model.TopUp{}).Error)
		require.NoError(t, db.Where("user_id = ?", u.Id).Delete(&model.Log{}).Error)
		require.NoError(t, db.Unscoped().Delete(&u).Error)
	})
	return u
}

type remote struct {
	mu                                   sync.Mutex
	db                                   *gorm.DB
	orders                               map[string]map[string]any
	adds, pays, gets                     int
	unknown, failGet, ambiguous, failPay bool
	mutate                               func(map[string]any)
	projectFault                         string
	payURL                               string
	payCurrency, payAmount               any
	methodEnabled                        bool
}

func mockRemote(t *testing.T, db *gorm.DB) *remote {
	t.Helper()
	r := &remote{db: db, orders: map[string]map[string]any{}, payURL: "https://www.idatariver.com/pay/order", methodEnabled: true}
	httpClient = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }, Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		assert.Equal(t, "open.idatariver.com", req.URL.Host)
		assert.Equal(t, "Bearer secret-must-not-appear", req.Header.Get("Authorization"))
		if req.Method == http.MethodGet {
			assert.Empty(t, req.Header.Get("Content-Type"))
		}
		var result any
		switch req.URL.Path {
		case "/mapi/project/detail":
			sku := map[string]any{"id": testSKU, "owner": testMerchant, "project": testProject, "status": "ONLINE", "stock": 1, "itemType": "reuse", "pricingScope": "specific"}
			pricing := map[string]any{"status": "ONLINE", "scope": "specific", "scopeItems": []string{testSKU}, "policy": "fixed", "price": 10}
			switch r.projectFault {
			case "stock":
				sku["stock"] = 0
			case "owner":
				sku["owner"] = "foreign"
			case "offline":
				sku["status"] = "OFFLINE"
			case "price":
				pricing["price"] = 9
			case "policy":
				pricing["policy"] = "dynamic"
			}
			result = map[string]any{"id": testProject, "owner": testMerchant, "status": "ONLINE", "type": "DIGITAL", "skus": []any{sku}, "pricings": []any{pricing}}
		case "/mapi/order/add":
			r.adds++
			var body struct {
				ProjectID string `json:"projectId"`
				SKUID     string `json:"skuId"`
				OrderInfo struct {
					Quantity int    `json:"quantity"`
					Coupon   string `json:"coupon"`
					Contact  string `json:"contactInfo"`
				} `json:"orderInfo"`
			}
			require.NoError(t, common.DecodeJson(req.Body, &body))
			assert.Equal(t, 1, body.OrderInfo.Quantity)
			assert.Empty(t, body.OrderInfo.Coupon)
			assert.Equal(t, testSKU, body.SKUID)
			var saved model.IdataRiverOrder
			require.NoError(t, db.Where("contact_info = ?", body.OrderInfo.Contact).First(&saved).Error, "claim must be durable before remote creation")
			var topup model.TopUp
			require.NoError(t, db.Where("trade_no = ?", saved.TradeNo).First(&topup).Error)
			id := "remote-" + common.GetRandomString(16)
			r.orders[id] = map[string]any{"id": id, "status": "NEW", "merchant": testMerchant, "contactInfo": body.OrderInfo.Contact, "project": map[string]any{"id": testProject, "type": "DIGITAL"}, "projectType": "DIGITAL", "sku": map[string]any{"id": testSKU}, "quantity": 1, "oriPrice": 10, "price": 10, "digitalItemsStr": "private-goods-must-not-appear"}
			result = map[string]any{"orderId": id}
			if r.unknown {
				return nil, errors.New("secret-must-not-appear " + body.OrderInfo.Contact)
			}
		case "/mapi/order/info":
			r.gets++
			if r.failGet {
				return nil, errors.New("secret-must-not-appear")
			}
			order := r.orders[req.URL.Query().Get("id")]
			if order == nil {
				return nil, errors.New("not found")
			}
			copy := maps.Clone(order)
			copy["mPayments"] = []any{map[string]any{"method": "crypto", "name": "Crypto", "enabled": r.methodEnabled}, map[string]any{"method": "disabled", "name": "Disabled", "enabled": false}}
			if r.mutate != nil {
				r.mutate(copy)
			}
			result = copy
		case "/mapi/order/search":
			items := []any{}
			for id, o := range r.orders {
				if o["contactInfo"] == req.URL.Query().Get("query") {
					items = append(items, map[string]any{"id": id})
				}
			}
			if r.ambiguous && len(items) > 0 {
				first := items[0].(map[string]any)["id"].(string)
				copy := maps.Clone(r.orders[first])
				copy["id"] = "second-valid-order"
				r.orders["second-valid-order"] = copy
				items = append(items, map[string]any{"id": "second-valid-order"})
			}
			result = map[string]any{"items": items}
		case "/mapi/order/pay":
			r.pays++
			var body map[string]any
			require.NoError(t, common.DecodeJson(req.Body, &body))
			assert.Equal(t, "crypto", body["method"])
			if strings.HasPrefix(body["id"].(string), "legacy-") {
				assert.Contains(t, body["redirectUrl"], "https://wallet.example.com/wallet?trade_no=IDR")
				assert.Equal(t, "https://wallet.example.com/api/user/idatariver/notify", body["callbackUrl"])
			} else {
				assert.Contains(t, body["redirectUrl"], "https://wallet.example.com/wallet?trade_no=idr-")
				assert.Equal(t, "https://wallet.example.com/api/mowan/idatariver/notify", body["callbackUrl"])
			}
			if r.failPay {
				return nil, errors.New("secret-must-not-appear")
			}
			result = map[string]any{"payUrl": r.payURL, "payCurrency": r.payCurrency, "amount": r.payAmount}
		default:
			t.Errorf("unexpected upstream route %s", req.URL.Path)
			return nil, errors.New("unexpected request")
		}
		raw, err := common.Marshal(map[string]any{"code": 0, "result": result})
		require.NoError(t, err)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	return r
}

func call(handler gin.HandlerFunc, user int, body string) (*httptest.ResponseRecorder, map[string]any) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", user)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/mowan/idatariver/test", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	c.Writer.WriteHeaderNow()
	var result map[string]any
	if w.Body.Len() > 0 {
		_ = common.Unmarshal(w.Body.Bytes(), &result)
	}
	return w, result
}

func create(t *testing.T, user int, requestID string) (string, map[string]any) {
	t.Helper()
	w, result := call(CreateOrder, user, `{"sku_id":"`+testSKU+`","request_id":"`+requestID+`"}`)
	require.Equal(t, 200, w.Code)
	require.Equal(t, true, result["success"], w.Body.String())
	data, ok := result["data"].(map[string]any)
	require.True(t, ok, w.Body.String())
	for _, field := range []string{"request_id", "trade_no", "sku_id", "name", "price", "quota", "currency", "status", "creation_state", "platform_status", "credited", "refund_status", "payment_methods", "can_pay", "can_check", "can_recreate", "query_error"} {
		assert.Contains(t, data, field)
	}
	assert.IsType(t, []any{}, data["payment_methods"])
	assert.NotContains(t, w.Body.String(), "secret-must-not-appear")
	assert.NotContains(t, w.Body.String(), "private-goods-must-not-appear")
	assert.NotContains(t, w.Body.String(), "contactInfo")
	return data["trade_no"].(string), data
}

func check(t *testing.T, user int, trade string) map[string]any {
	t.Helper()
	w, result := call(Check, user, `{"trade_no":"`+trade+`"}`)
	require.Equal(t, true, result["success"], w.Body.String())
	return result["data"].(map[string]any)
}

func quota(t *testing.T, db *gorm.DB, user int) int {
	t.Helper()
	var u model.User
	require.NoError(t, db.First(&u, user).Error)
	return u.Quota
}
func setRemoteStatus(r *remote, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.orders {
		o["status"] = status
	}
}

func TestIdataRiverLegacyOrders(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := fixture(t, dialect)
			require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS idata_river_orders (
 trade_no varchar(64) PRIMARY KEY, platform_order_id varchar(64) UNIQUE,
 merchant_id varchar(24), project_id varchar(24), sk_uid varchar(24),
 price_usd varchar(64), quota_usd varchar(64), quota bigint, credited_quota bigint,
 callback_origin varchar(255), refund_status varchar(32), refund_time bigint)`).Error)
			owner := testUser(t, db, 0)
			other := testUser(t, db, 0)
			r := mockRemote(t, db)
			prefix := common.GetRandomString(12)
			trade, id := "IDR"+prefix, "legacy-"+prefix
			seed := func(trade string, platform *string) {
				t.Helper()
				order := &model.TopUp{UserId: owner.Id, TradeNo: trade, PaymentProvider: model.PaymentProviderIdataRiver, PaymentMethod: model.PaymentProviderIdataRiver, Status: common.TopUpStatusPending}
				require.NoError(t, db.Create(order).Error)
				require.NoError(t, db.Table("idata_river_orders").Create(map[string]any{"trade_no": trade, "platform_order_id": platform, "merchant_id": testMerchant, "project_id": testProject, "sk_uid": testSKU, "price_usd": "10", "quota_usd": "10.25", "quota": testQuota, "credited_quota": 0, "callback_origin": "https://wallet.example.com", "refund_status": "none", "refund_time": 0}).Error)
				t.Cleanup(func() { require.NoError(t, db.Exec("DELETE FROM idata_river_orders WHERE trade_no = ?", trade).Error) })
			}
			seed(trade, &id)
			seed(trade+"unbound", nil)
			seed(trade+"unpaid", new(id+"-unpaid"))
			seed(trade+"credited", new(id+"-credited"))
			require.NoError(t, db.Table("idata_river_orders").Where("trade_no = ?", trade+"credited").Update("credited_quota", testQuota).Error)
			require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", trade+"credited").Update("status", common.TopUpStatusSuccess).Error)
			newTrade, _ := create(t, owner.Id, "new-"+prefix)
			var history []*model.TopUp
			require.NoError(t, db.Where("user_id = ?", owner.Id).Find(&history).Error)
			require.Len(t, history, 5)
			require.NoError(t, model.PopulateIdataRiverSummaries(t.Context(), history))
			for _, row := range history {
				require.NotNil(t, row.IdataRiver)
			}
			r.orders[id] = map[string]any{"id": id, "status": "NEW", "merchant": testMerchant, "contactInfo": "legacy-private", "project": map[string]any{"id": testProject}, "sku": map[string]any{"id": testSKU}, "quantity": 1, "oriPrice": 10, "price": 10}
			r.orders[id+"-unpaid"] = maps.Clone(r.orders[id])
			r.orders[id+"-unpaid"]["id"] = id + "-unpaid"
			r.orders[id+"-unpaid"]["status"] = "REFUND"
			r.orders[id+"-credited"] = maps.Clone(r.orders[id])
			r.orders[id+"-credited"]["id"] = id + "-credited"
			r.orders[id+"-credited"]["status"] = "DONE"
			body := `{"trade_no":"` + trade + `"}`
			gets := r.gets
			_, denied := call(Check, other.Id, body)
			assert.Equal(t, false, denied["success"])
			unbound := check(t, owner.Id, trade+"unbound")
			assert.Equal(t, false, unbound["can_recreate"])
			assert.Equal(t, false, unbound["can_check"])
			assert.Equal(t, gets, r.gets)
			assert.Equal(t, true, check(t, owner.Id, trade)["can_pay"])
			w, pay := call(Pay, owner.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
			require.Equal(t, true, pay["success"], w.Body.String())
			assert.Contains(t, w.Body.String(), r.payURL)
			assert.NotContains(t, w.Body.String(), "legacy-private")
			notify := func(want int, body string) {
				t.Helper()
				w, _ := call(LegacyNotify, 0, body)
				require.Equal(t, want, w.Code, w.Body.String())
			}
			notify(400, `{"code":1,"result":{"id":"`+id+`"}}`)
			notify(200, `{"id":"`+id+`","status":"DONE"}`)
			assert.Zero(t, quota(t, db, owner.Id), "callback status is not authoritative")
			notify(200, `{"id":"`+id+`-credited"}`)
			assert.Zero(t, quota(t, db, owner.Id), "opening balance does not get a second historical credit")
			require.NoError(t, db.Model(&model.TopUp{}).Where("trade_no = ?", trade+"credited").Update("status", common.TopUpStatusPending).Error)
			notify(503, `{"id":"`+id+`-credited"}`)
			assert.Zero(t, quota(t, db, owner.Id), "inconsistent history is not repaired by an anonymous callback")
			r.orders[id]["status"] = "DONE"
			r.orders[id]["price"] = 9
			notify(503, `{"id":"`+id+`"}`)
			assert.Zero(t, quota(t, db, owner.Id))
			r.orders[id]["price"] = 10
			for range 2 {
				notify(200, `{"code":0,"result":{"id":"`+id+`"}}`)
			}
			assert.Equal(t, testQuota, quota(t, db, owner.Id))
			r.orders[id]["status"] = "REFUND"
			for range 2 {
				notify(200, `{"id":"`+id+`","event":"ORDER_REFUND"}`)
			}
			notify(200, `{"id":"`+id+`-unpaid"}`)
			r.orders[id+"-unpaid"]["status"] = "DONE"
			notify(200, `{"id":"`+id+`-unpaid"}`)
			assert.Zero(t, quota(t, db, owner.Id))
			assert.Equal(t, model.IdataRiverRefundRecovered, check(t, owner.Id, trade)["refund_status"])
			assert.Equal(t, model.IdataRiverRefundBeforeCredit, check(t, owner.Id, trade+"unpaid")["refund_status"])
			assert.Error(t, model.AssociateIdataRiverOrder(t.Context(), newTrade, id), "a platform ID cannot belong to both order generations")
			assert.Equal(t, 1, r.adds, "legacy paths never create platform orders")
			assert.Equal(t, 1, r.pays)
			require.NoError(t, model.MigrateIdataRiver(db))
			assert.False(t, db.Migrator().HasColumn("idata_river_orders", "request_id"))
			assert.False(t, db.Migrator().HasColumn("idata_river_orders", "contact_info"))
		})
	}
}

func TestIdataRiverCheckoutAndRecovery(t *testing.T) {
	db := fixture(t, "sqlite")
	u := testUser(t, db, 0)
	other := testUser(t, db, 0)
	r := mockRemote(t, db)
	_, products := call(Products, u.Id, "")
	assert.IsType(t, []any{}, products["data"].(map[string]any)["products"])
	trade, data := create(t, u.Id, "request-checkout")
	assert.Equal(t, "NEW", data["platform_status"])
	assert.Equal(t, true, data["can_pay"])
	assert.Equal(t, 10.25, data["quota"])
	assert.Equal(t, "USD", data["currency"])
	var histories []*model.TopUp
	require.NoError(t, db.Where("user_id = ?", u.Id).Find(&histories).Error)
	require.NoError(t, model.PopulateIdataRiverSummaries(context.Background(), histories))
	require.Len(t, histories, 1)
	assert.Equal(t, trade, histories[0].IdataRiver.TradeNo)
	require.Error(t, model.ManualCompleteTopUp(trade, "127.0.0.1"))
	assert.Zero(t, quota(t, db, u.Id))
	before := r.gets
	_, foreign := call(Check, other.Id, `{"trade_no":"`+trade+`"}`)
	assert.Equal(t, false, foreign["success"])
	_, foreign = call(Pay, other.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	assert.Equal(t, false, foreign["success"])
	assert.Equal(t, before, r.gets)
	_, paid := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	pay := paid["data"].(map[string]any)
	assert.Equal(t, r.payURL, pay["pay_url"])
	assert.Nil(t, pay["pay_currency"])
	assert.Contains(t, pay, "amount")
	assert.Nil(t, pay["amount"])
	for _, link := range []string{"https://d1e3d.payerbridge.com/pay/order", "https://checkout.example.net/pay/order"} {
		r.payURL = link
		_, result := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
		assert.Equal(t, link, result["data"].(map[string]any)["pay_url"])
		assert.Equal(t, false, result["data"].(map[string]any)["query_error"])
	}
	r.payCurrency, r.payAmount = "CNY", "75.25"
	_, paid = call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	pay = paid["data"].(map[string]any)
	assert.Equal(t, 75.25, pay["amount"])
	assert.Equal(t, "CNY", pay["pay_currency"])
	assert.Equal(t, "USD", pay["currency"])
	assert.Equal(t, float64(10), pay["price"])
	r.payCurrency, r.payAmount = "BTC", "0.0002"
	_, paid = call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	pay = paid["data"].(map[string]any)
	assert.Equal(t, 0.0002, pay["amount"])
	assert.Equal(t, "BTC", pay["pay_currency"])
	r.payAmount = "1e1000000000"
	_, invalidAmount := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	assert.Equal(t, true, invalidAmount["data"].(map[string]any)["query_error"])
	r.payCurrency, r.payAmount = nil, nil
	r.failPay = true
	_, failedPay := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	assert.Equal(t, true, failedPay["data"].(map[string]any)["query_error"])
	assert.Equal(t, 1, r.adds)
	r.failPay = false
	r.methodEnabled = false
	calls := r.pays
	_, disabled := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
	assert.Equal(t, false, disabled["data"].(map[string]any)["can_pay"])
	assert.Equal(t, calls, r.pays)
	r.methodEnabled = true
	r.failGet = true
	failed := check(t, u.Id, trade)
	assert.Equal(t, true, failed["query_error"])
	assert.Equal(t, "NEW", failed["platform_status"])
	assert.Equal(t, "pending", failed["status"])
	assert.Equal(t, false, failed["can_pay"])
	r.failGet = false
	// Current config changes do not rewrite the original wallet snapshot.
	common.OptionMapRWMutex.Lock()
	common.OptionMap["IdataRiverProducts"] = "[]"
	common.OptionMap["IdataRiverEnabled"] = "false"
	common.OptionMapRWMutex.Unlock()
	setRemoteStatus(r, "DONE")
	done := check(t, u.Id, trade)
	assert.Equal(t, "success", done["status"])
	assert.Equal(t, 10.25, done["quota"])
	assert.Equal(t, testQuota, quota(t, db, u.Id))
	check(t, u.Id, trade)
	assert.Equal(t, testQuota, quota(t, db, u.Id))
	setRemoteStatus(r, "REFUND")
	refunded := check(t, u.Id, trade)
	assert.Equal(t, "recovered", refunded["refund_status"])
	assert.Zero(t, quota(t, db, u.Id))
	check(t, u.Id, trade)
	assert.Zero(t, quota(t, db, u.Id))
}

func TestIdataRiverRejectedAttemptCanRetrySameRequest(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := fixture(t, dialect)
			u := testUser(t, db, 0)
			r := mockRemote(t, db)
			const requestID = "request-rejected-retry"
			for _, fault := range []string{"disabled", "stock"} {
				require.NoError(t, model.UpdateOption("IdataRiverEnabled", fmt.Sprint(fault != "disabled")))
				r.projectFault = fault
				_, result := call(CreateOrder, u.Id, `{"sku_id":"`+testSKU+`","request_id":"`+requestID+`"}`)
				require.Equal(t, false, result["success"], fault)
				_, _, err := model.GetIdataRiverOrder(context.Background(), "", requestID, u.Id)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				var topups int64
				require.NoError(t, db.Model(&model.TopUp{}).Where("user_id = ?", u.Id).Count(&topups).Error)
				assert.Zero(t, topups)
				assert.Zero(t, r.adds)
			}
			r.projectFault = ""
			trade, view := create(t, u.Id, requestID)
			assert.Equal(t, "associated", view["creation_state"])
			assert.Equal(t, true, view["can_pay"])
			again, _ := create(t, u.Id, requestID)
			assert.Equal(t, trade, again)
			assert.Equal(t, 1, r.adds)
			var attempts int64
			require.NoError(t, db.Model(&model.IdataRiverOrder{}).Where("user_id = ?", u.Id).Count(&attempts).Error)
			assert.EqualValues(t, 1, attempts)
			assert.Zero(t, quota(t, db, u.Id))
		})
	}
}

func TestIdataRiverUnknownNeverCreatesAgain(t *testing.T) {
	for _, mode := range []string{"recover", "ambiguous", "notify"} {
		t.Run(mode, func(t *testing.T) {
			db := fixture(t, "sqlite")
			u := testUser(t, db, 0)
			r := mockRemote(t, db)
			r.unknown = true
			trade, data := create(t, u.Id, "request-unknown")
			assert.Equal(t, "unknown", data["creation_state"])
			assert.Equal(t, true, data["query_error"])
			assert.Equal(t, false, data["can_pay"])
			if mode == "ambiguous" {
				r.ambiguous = true
			}
			if mode == "notify" {
				setRemoteStatus(r, "DONE")
				var id string
				for key := range r.orders {
					id = key
				}
				w, _ := call(Notify, 0, `{"result":{"id":"`+id+`","status":"REFUND"}}`)
				assert.Equal(t, 200, w.Code)
				assert.Equal(t, testQuota, quota(t, db, u.Id))
			} else {
				again, view := create(t, u.Id, "request-unknown")
				assert.Equal(t, trade, again)
				if mode == "recover" {
					assert.Equal(t, "associated", view["creation_state"])
					assert.Equal(t, false, view["query_error"])
				} else {
					assert.Equal(t, "unknown", view["creation_state"])
					assert.Equal(t, true, view["query_error"])
				}
			}
			assert.Equal(t, 1, r.adds)
		})
	}
}

func TestIdataRiverTrustBoundaryAndUnavailableProducts(t *testing.T) {
	db := fixture(t, "sqlite")
	u := testUser(t, db, 0)
	r := mockRemote(t, db)
	for _, fault := range []string{"stock", "owner", "offline", "price", "policy"} {
		r.projectFault = fault
		_, result := call(CreateOrder, u.Id, `{"sku_id":"`+testSKU+`","request_id":"request-unavailable"}`)
		assert.Equal(t, false, result["success"], fault)
	}
	assert.Zero(t, r.adds)
	r.projectFault = ""
	trade, _ := create(t, u.Id, "request-integrity")
	setRemoteStatus(r, "DONE")
	faults := []func(map[string]any){
		func(o map[string]any) { o["price"] = "1e1000000000" },
		func(o map[string]any) { o["merchant"] = "foreign" }, func(o map[string]any) { o["project"] = map[string]any{"id": "foreign", "type": "DIGITAL"} }, func(o map[string]any) { o["sku"] = map[string]any{"id": "foreign"} }, func(o map[string]any) { o["quantity"] = 2 }, func(o map[string]any) { o["oriPrice"] = 11 }, func(o map[string]any) { o["price"] = 9 }, func(o map[string]any) { o["currency"] = "CNY" }, func(o map[string]any) { o["contactInfo"] = "foreign" }, func(o map[string]any) { delete(o, "oriPrice") }, func(o map[string]any) { o["projectType"] = "API" }, func(o map[string]any) { o["status"] = "PAID" },
	}
	for i, f := range faults {
		r.mutate = f
		view := check(t, u.Id, trade)
		assert.Equal(t, true, view["query_error"], i)
		assert.Zero(t, quota(t, db, u.Id))
		var id string
		for key := range r.orders {
			id = key
		}
		w, _ := call(Notify, 0, `{"id":"`+id+`"}`)
		assert.Equal(t, 503, w.Code, i)
	}
	r.mutate = nil
	for _, link := range []string{"http://idatariver.com/pay", "javascript:alert(1)", "https://idatariver.com@evil.example/pay", "https://idatariver.com:8443/pay", "https://idatariver.com/pay#fragment", "https:///pay", ""} {
		setRemoteStatus(r, "NEW")
		r.payURL = link
		_, result := call(Pay, u.Id, `{"trade_no":"`+trade+`","method":"crypto"}`)
		assert.Empty(t, result["data"].(map[string]any)["pay_url"])
	}
	setRemoteStatus(r, "DONE")
	check(t, u.Id, trade)
	assert.Equal(t, testQuota, quota(t, db, u.Id))
}

func TestIdataRiverDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := fixture(t, dialect)
			u := testUser(t, db, 0)
			r := mockRemote(t, db)
			trade, _ := create(t, u.Id, "request-matrix-"+common.GetRandomString(12))
			connection, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, connection.Close())
			reopened, err := gorm.Open(db.Dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			*db = *reopened
			require.NoError(t, model.MigrateIdataRiver(db))
			assert.Equal(t, trade, check(t, u.Id, trade)["trade_no"])
			setRemoteStatus(r, "EXPIRED")
			expired := check(t, u.Id, trade)
			assert.Equal(t, "expired", expired["status"])
			assert.Equal(t, true, expired["can_recreate"])
			setRemoteStatus(r, "DONE")
			check(t, u.Id, trade)
			assert.Equal(t, testQuota, quota(t, db, u.Id), "late verified DONE credits after EXPIRED")
			// Refund failures roll back both the debit and durable refund marker.
			setRemoteStatus(r, "REFUND")
			callback := "idr-refund-failure"
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "mowan_idatariver_orders" {
					tx.AddError(errors.New("injected persistence failure"))
				}
			}))
			view := check(t, u.Id, trade)
			assert.Equal(t, true, view["query_error"])
			assert.Equal(t, "none", view["refund_status"])
			assert.Equal(t, testQuota, quota(t, db, u.Id))
			require.NoError(t, db.Callback().Update().Remove(callback))
			view = check(t, u.Id, trade)
			assert.Equal(t, "recovered", view["refund_status"])
			assert.Zero(t, quota(t, db, u.Id))
			setRemoteStatus(r, "DONE")
			check(t, u.Id, trade)
			assert.Zero(t, quota(t, db, u.Id), "refund marker prevents recredit")
			// New uncredited refunds are terminal, even if DONE arrives later.
			trade, _ = create(t, u.Id, "request-before-"+common.GetRandomString(12))
			setRemoteStatus(r, "REFUND")
			view = check(t, u.Id, trade)
			assert.Equal(t, "before_credit", view["refund_status"])
			setRemoteStatus(r, "DONE")
			check(t, u.Id, trade)
			assert.Zero(t, quota(t, db, u.Id))
			// Insufficient funds: zero debit, terminal manual recovery, no auto-retry.
			trade, _ = create(t, u.Id, "request-manual-"+common.GetRandomString(12))
			setRemoteStatus(r, "DONE")
			check(t, u.Id, trade)
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", u.Id).Update("quota", testQuota-1).Error)
			setRemoteStatus(r, "REFUND")
			view = check(t, u.Id, trade)
			assert.Equal(t, "manual_required", view["refund_status"])
			assert.Equal(t, testQuota-1, quota(t, db, u.Id))
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", u.Id).Update("quota", testQuota).Error)
			check(t, u.Id, trade)
			assert.Equal(t, testQuota, quota(t, db, u.Id))
			var snapshot model.IdataRiverOrder
			require.NoError(t, db.Where("trade_no = ?", trade).First(&snapshot).Error)
			assert.Equal(t, testQuota, snapshot.CreditedQuota)
			assert.Equal(t, "10.25", snapshot.QuotaUSD)
			t.Logf("%s: fresh schema, existing-schema upgrade, connection restart, repeat migration, durable read, late credit, refund rollback/retry and terminal markers passed", dialect)
		})
	}
}

func TestIdataRiverConcurrentCreateAndSettlement(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := fixture(t, dialect)
			u := testUser(t, db, 0)
			r := mockRemote(t, db)
			const concurrency = 12
			var wg sync.WaitGroup
			for range concurrency {
				wg.Go(func() {
					_, result := call(CreateOrder, u.Id, `{"sku_id":"`+testSKU+`","request_id":"request-concurrent"}`)
					assert.Equal(t, true, result["success"])
				})
			}
			wg.Wait()
			assert.Equal(t, 1, r.adds)
			var snapshots []model.IdataRiverOrder
			require.NoError(t, db.Where("user_id = ?", u.Id).Find(&snapshots).Error)
			require.Len(t, snapshots, 1)
			trade := snapshots[0].TradeNo
			setRemoteStatus(r, "DONE")
			for range concurrency {
				wg.Go(func() { call(Check, u.Id, `{"trade_no":"`+trade+`"}`) })
			}
			wg.Wait()
			assert.Equal(t, testQuota, quota(t, db, u.Id))
			setRemoteStatus(r, "REFUND")
			for range concurrency {
				wg.Go(func() { call(Check, u.Id, `{"trade_no":"`+trade+`"}`) })
			}
			wg.Wait()
			assert.Zero(t, quota(t, db, u.Id))
			var logs int64
			require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", u.Id, model.LogTypeTopup).Count(&logs).Error)
			assert.EqualValues(t, 1, logs)
		})
	}
}

func TestIdataRiverOverflowRollsBackAndNotifyBudget(t *testing.T) {
	db := fixture(t, "sqlite")
	u := testUser(t, db, 0)
	r := mockRemote(t, db)
	trade, _ := create(t, u.Id, "request-overflow")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", u.Id).Update("quota", common.MaxWalletQuota-testQuota+1).Error)
	setRemoteStatus(r, "DONE")
	view := check(t, u.Id, trade)
	assert.Equal(t, true, view["query_error"])
	assert.Equal(t, false, view["credited"])
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", u.Id).Update("quota", 0).Error)
	check(t, u.Id, trade)
	assert.Equal(t, testQuota, quota(t, db, u.Id))
	httpClient = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	start := time.Now()
	w, _ := call(Notify, 0, `{"id":"remote-timeout"}`)
	assert.Equal(t, 503, w.Code)
	assert.Less(t, time.Since(start), 4500*time.Millisecond)
}

func TestIdataRiverSettingsValidation(t *testing.T) {
	db := fixture(t, "sqlite")
	_ = db
	for key, value := range map[string]string{"IdataRiverMerchantID": testMerchant, "IdataRiverKeyConfigured": "true", "IdataRiverEnabled": "yes", "IdataRiverCallbackOrigin": "http://wallet.example.com", "IdataRiverApiKey": "bad\r\nkey"} {
		require.Error(t, model.UpdateOption(key, value), key)
	}
	for _, origin := range []string{"https://127.0.0.1", "https://localhost", "https://example.com/extra", "https://user@example.com", "https://example.com?x=1", "https://example.com:123"} {
		assert.Error(t, setting.ValidateIdataRiverOrigin(origin), origin)
	}
	for _, products := range []string{"null", `[{"sku_id":"` + setting.IdataRiverTestSKU + `","name":"test","price":0.1,"quota":1}]`, `[{"sku_id":"` + testSKU + `","name":"fraction","price":1,"quota":0.0000001}]`, `[{"sku_id":"` + testSKU + `","name":"foreign","price":1,"quota":1,"currency":"CNY"}]`} {
		assert.Error(t, model.UpdateOption("IdataRiverProducts", products))
	}
	require.NoError(t, model.UpdateOption("IdataRiverApiKey", "   "))
	assert.Equal(t, "secret-must-not-appear", setting.GetIdataRiverConfig().APIKey)
	require.NoError(t, model.UpdateOption("IdataRiverEnabled", "false"))
	require.NoError(t, model.UpdateOption("IdataRiverProducts", "[]"))
	require.Error(t, model.UpdateOption("IdataRiverEnabled", "true"))
	_, result := call(Products, 1, "")
	assert.Equal(t, []any{}, result["data"].(map[string]any)["products"])
	t.Log(fmt.Sprintf("settings fail closed; empty key preserves saved secret; empty lists stay arrays"))
}

func TestIdataRiverLegacyConfigurationRequiresExplicitIdentityAndCompliance(t *testing.T) {
	fixture(t, "sqlite")
	common.OptionMapRWMutex.Lock()
	for _, key := range []string{"IdataRiverMerchantId", "IdataRiverProjectId", "IdataRiverCallbackOrigin"} {
		delete(common.OptionMap, key)
	}
	common.OptionMapRWMutex.Unlock()
	require.False(t, paymentEnabled(setting.GetIdataRiverConfig()), "legacy three-key options must not silently acquire a production identity")
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"IdataRiverEnabled":        "false",
		"IdataRiverMerchantId":     testMerchant,
		"IdataRiverProjectId":      testProject,
		"IdataRiverCallbackOrigin": "https://wallet.example.com",
	}))
	require.NoError(t, model.UpdateOption("IdataRiverEnabled", "true"))
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	require.False(t, paymentEnabled(setting.GetIdataRiverConfig()))
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = "outdated"
	require.False(t, paymentEnabled(setting.GetIdataRiverConfig()))
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	require.True(t, paymentEnabled(setting.GetIdataRiverConfig()))
}
