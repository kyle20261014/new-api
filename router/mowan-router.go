package router

import (
	"github.com/QuantumNous/new-api/internal/mowan/idatariver"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func registerMowanRoutes(api *gin.RouterGroup) {
	group := api.Group("/mowan/idatariver")
	group.POST("/notify", middleware.AnonymousRequestBodyLimit(), idatariver.Notify)
	user := group.Group("", middleware.DisableCache(), middleware.UserAuth())
	user.GET("/products", idatariver.Products)
	user.POST("/order", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("idatariver_order"), idatariver.CreateOrder)
	user.POST("/pay", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("idatariver_pay"), idatariver.Pay)
	user.POST("/check", middleware.UserCriticalRateLimit("idatariver_check"), idatariver.Check)
}
