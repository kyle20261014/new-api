package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutingPlaygroundPolicyIsTransientAndStripped(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}, &model.TokenRouting{}))
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "routing-playground", AffCode: "routing-playground", Group: "default", Status: common.UserStatusEnabled}).Error)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"group":"must-not-override","mowan_routing":{"schema_version":1,"mode":"smart","strategy":"price"}}`
	request := func(payload string, pat bool) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(BodyStorageCleanup(), func(c *gin.Context) { c.Set("id", 1); c.Set("use_access_token", pat); c.Next() })
		r.POST("/pg/chat/completions", RoutingPlaygroundPolicy(), func(c *gin.Context) {
			if c.GetBool("mowan_routing_playground") {
				require.Zero(t, c.GetInt("token_id"))
				row, ok := c.MustGet(smartrouting.ContextKey).(*model.TokenRouting)
				require.True(t, ok)
				assert.Contains(t, row.PolicyJSON, `"strategy":"price"`)
			}
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			data, err := storage.Bytes()
			require.NoError(t, err)
			c.Data(http.StatusOK, "application/json", data)
		})
		req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := request(body, false)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "mowan_routing")
	assert.NotContains(t, w.Body.String(), "group")
	assert.Contains(t, w.Body.String(), "hello")
	assert.Contains(t, w.Body.String(), "gpt-4o")
	legacy := `{"model":"gpt-4o","group":"default"}`
	w = request(legacy, false)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, legacy, w.Body.String())
	assert.Equal(t, http.StatusForbidden, request(body, true).Code)
	for _, invalid := range []string{
		`{"model":"gpt-4o","mowan_routing":null}`,
		`{"model":"gpt-4o","mowan_routing":{"schema_version":1,"mode":"fixed","fixed_group":""}}`,
		`{"model":"gpt-4o","mowan_routing":{},"mowan_routing":{"schema_version":1,"mode":"smart","strategy":"price"}}`,
	} {
		assert.Equal(t, http.StatusBadRequest, request(invalid, false).Code)
	}
	var count int64
	require.NoError(t, model.DB.Model(&model.Token{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, model.DB.Model(&model.TokenRouting{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = 1").Update("status", common.UserStatusDisabled).Error)
	assert.Equal(t, http.StatusForbidden, request(body, false).Code)
	require.NoError(t, model.DB.Migrator().DropTable(&model.User{}))
	assert.Equal(t, http.StatusServiceUnavailable, request(body, false).Code)
}
