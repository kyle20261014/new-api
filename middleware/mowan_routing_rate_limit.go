package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// Routes which historically had no model limiter keep that behavior for legacy
// keys. New policies use the same request and final-group admission as /v1.
func RoutingModelRequestRateLimit() gin.HandlerFunc {
	limit := ModelRequestRateLimit()
	return func(c *gin.Context) {
		if service.IsSmartRouting(c) {
			limit(c)
			return
		}
		c.Next()
	}
}

func routingGroupRateLimits(c *gin.Context) func() {
	admitted := map[string]func(bool){}
	c.Set(service.RoutingGroupRateLimitContextKey, func(group string) error {
		if _, ok := admitted[group]; ok {
			return nil
		}
		total, success, found := setting.GetGroupRateLimit(group)
		if !found {
			return nil
		}
		done, err := reserveRoutingGroupRate(c, strconv.Itoa(c.GetInt("id"))+":"+group, total, success)
		if err != nil {
			return err
		}
		admitted[group] = done
		return nil
	})
	return func() {
		finalGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		for group, done := range admitted {
			done(group == finalGroup && modelRequestSucceeded(c))
		}
	}
}

func reserveRoutingGroupRate(c *gin.Context, subject string, total, success int) (func(bool), error) {
	duration := rateLimitDurationSeconds(setting.ModelRequestRateLimitDurationMinutes)
	reject := func(status int, key string, count int) error {
		message := key
		if status == http.StatusTooManyRequests {
			message = i18n.T(c, key, map[string]any{"Minutes": setting.ModelRequestRateLimitDurationMinutes, "Max": count})
		}
		return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeInvalidRequest, status, types.ErrOptionWithSkipRetry())
	}
	if common.RedisEnabled {
		if common.RDB == nil {
			return nil, reject(http.StatusServiceUnavailable, "rate_limit_check_failed", 0)
		}
		ctx := c.Request.Context()
		successKey := fmt.Sprintf("rateLimit:%s:%s", ModelRequestRateLimitSuccessCountMark, subject)
		// ponytail: reuse the existing Redis completed-request window; add a
		// cross-node reservation only if in-flight success admission is required.
		allowed, err := checkRedisRateLimit(ctx, common.RDB, successKey, success, duration)
		if err != nil {
			return nil, reject(http.StatusServiceUnavailable, "rate_limit_check_failed", 0)
		}
		if !allowed {
			return nil, reject(http.StatusTooManyRequests, i18n.MsgRateLimitReached, success)
		}
		if total > 0 {
			allowed, err = limiter.New(ctx, common.RDB).Allow(ctx, "rateLimit:"+subject,
				limiter.WithCapacity(rateLimitCapacity(total, duration)), limiter.WithRate(int64(total)), limiter.WithRequested(duration))
			if err != nil {
				return nil, reject(http.StatusServiceUnavailable, "rate_limit_check_failed", 0)
			}
			if !allowed {
				return nil, reject(http.StatusTooManyRequests, i18n.MsgRateLimitTotalReached, total)
			}
		}
		return func(ok bool) {
			if ok {
				recordRedisRequest(ctx, common.RDB, successKey, success)
			}
		}, nil
	}
	inMemoryRateLimiter.Init(time.Duration(setting.ModelRequestRateLimitDurationMinutes) * time.Minute)
	if total > 0 && !inMemoryRateLimiter.Request(ModelRequestRateLimitCountMark+subject, total, duration) {
		return nil, reject(http.StatusTooManyRequests, i18n.MsgRateLimitTotalReached, total)
	}
	var reservation *common.RateLimitReservation
	if success > 0 {
		reservation = inMemoryRateLimiter.Reserve(ModelRequestRateLimitSuccessCountMark+subject, success, duration)
		if reservation == nil {
			return nil, reject(http.StatusTooManyRequests, i18n.MsgRateLimitReached, success)
		}
	}
	return func(ok bool) { reservation.Complete(ok) }, nil
}
