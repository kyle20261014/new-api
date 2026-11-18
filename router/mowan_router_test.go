package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestIdataRiverCheckRoutesKeepAuthWithoutCriticalLimit(t *testing.T) {
	previous := common.CriticalRateLimitEnable
	common.CriticalRateLimitEnable = true
	t.Cleanup(func() { common.CriticalRateLimitEnable = previous })
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var handlers []string
	engine.Use(func(c *gin.Context) {
		handlers = c.HandlerNames()
		c.Next()
	})
	registerMowanRoutes(engine.Group("/api"))
	for _, tc := range []struct {
		path    string
		limited bool
	}{
		{"/api/mowan/idatariver/check", false},
		{"/api/user/idatariver/check", false},
		{"/api/mowan/idatariver/order", true},
		{"/api/mowan/idatariver/pay", true},
		{"/api/user/idatariver/pay", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, nil))
			assert.Equal(t, http.StatusUnauthorized, response.Code)
			limited := false
			for _, name := range handlers {
				limited = limited || strings.Contains(name, "RateLimit")
			}
			assert.Equal(t, tc.limited, limited, "registered handlers: %v", handlers)
		})
	}
}
