package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

const RoutingGroupRateLimitContextKey = "mowan_routing_group_rate_limit"

// The request middleware owns the limiter and its success reservations; the
// shared transport gate supplies the final, authorized billing group.
func CheckRoutingGroupRateLimit(c *gin.Context, group string) error {
	if !setting.ModelRequestRateLimitEnabled {
		return nil
	}
	if _, _, found := setting.GetGroupRateLimit(group); !found {
		return nil
	}
	if value, ok := c.Get(RoutingGroupRateLimitContextKey); ok {
		if check, ok := value.(func(string) error); ok {
			return check(group)
		}
	}
	return types.NewErrorWithStatusCode(errors.New("routing_group_rate_limit_unavailable"), types.ErrorCodeInvalidRequest, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}
