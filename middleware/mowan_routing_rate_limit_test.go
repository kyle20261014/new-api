package middleware

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/internal/mowan/smartrouting"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRoutingRateLimitUsesFinalGroupOnceAndOnlyFinalSuccess(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			prevEnabled, prevMinutes := setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes
			prevTotal, prevSuccess, prevGroups, prevRedis := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitGroup, common.RedisEnabled
			t.Cleanup(func() {
				setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = prevEnabled, prevMinutes
				setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount, setting.ModelRequestRateLimitGroup, common.RedisEnabled = prevTotal, prevSuccess, prevGroups, prevRedis
			})
			setting.ModelRequestRateLimitEnabled, setting.ModelRequestRateLimitDurationMinutes = true, 1
			setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 0, 0
			setting.ModelRequestRateLimitGroup = map[string][2]int{"a": {0, 1}, "b": {0, 1}}
			common.RedisEnabled = backend == "redis"
			if common.RedisEnabled {
				useRateLimitMiniRedis(t)
			}
			userID := 7300000 + int(modelRateLimitTestUsers.Add(1))
			router := gin.New()
			router.GET("/:group", func(c *gin.Context) {
				c.Set("id", userID)
				c.Set(smartrouting.ContextKey, &model.TokenRouting{Mode: "smart"})
				common.SetContextKey(c, constant.ContextKeyUserGroup, "owner")
			}, ModelRequestRateLimit(), func(c *gin.Context) {
				groups := []string{c.Param("group")}
				if groups[0] == "unknown" {
					groups = []string{"a"}
				}
				if groups[0] == "fallback" {
					groups = []string{"a", "b"}
				}
				for _, group := range groups {
					for range 2 { // One group admission covers retries in that group.
						err := service.CheckRoutingGroupRateLimit(c, group)
						if err != nil {
							var apiErr *types.NewAPIError
							require.True(t, errors.As(err, &apiErr))
							c.Status(apiErr.StatusCode)
							return
						}
					}
					common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
				}
				if c.Param("group") == "unknown" {
					_, err := c.Writer.WriteString("data: partial\n\n")
					require.NoError(t, err)
					service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("submission unknown after stream disconnect"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway))
					return
				}
				c.Status(http.StatusOK)
			})
			require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/fallback", "127.0.0.1:1000").Code)
			require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/b", "127.0.0.1:1000").Code)
			require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/a", "127.0.0.1:1000").Code, "failed first group must not consume its success slot")
			require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/a", "127.0.0.1:1000").Code)
			userID = 7300000 + int(modelRateLimitTestUsers.Add(1))
			setting.ModelRequestRateLimitSuccessCount = 1
			require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/unknown", "127.0.0.1:1000").Code)
			require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/a", "127.0.0.1:1000").Code, "an already-written unknown stream must not consume either successful-request window")
			require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/b", "127.0.0.1:1000").Code, "a real success must consume the global slot")
			setting.ModelRequestRateLimitSuccessCount = 0
			require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/a", "127.0.0.1:1000").Code, "a real success must consume the final-group slot")
			if backend == "memory" {
				userID = 7300000 + int(modelRateLimitTestUsers.Add(1))
				setting.ModelRequestRateLimitCount = 1
				setting.ModelRequestRateLimitGroup = map[string][2]int{"a": {0, 1000}, "b": {0, 1000}}
				require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/fallback", "127.0.0.1:1000").Code, "switching groups must not consume the global gate twice")
				require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/a", "127.0.0.1:1000").Code)
			}
		})
	}
}
