package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/internal/mowan/idatariver"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerMowanRoutes(api *gin.RouterGroup) {
	api.GET("/mowan/homepage/models", middleware.DisableCache(), middleware.HeaderNavModuleAuth("pricing"), controller.GetHomepageModels)
	routing := api.Group("/mowan/smart-routing", middleware.DisableCache(), middleware.AnonymousRequestBodyLimit())
	self := routing.Group("/self", middleware.RoutingSelfAudit(), middleware.RoutingSelfAuth(), middleware.UserCriticalRateLimit("smart-routing-self"))
	self.GET("", controller.GetRoutingSelf)
	self.PUT("", controller.PutRoutingSelf)
	self.GET("/candidates", controller.GetRoutingCandidates)
	self.GET("/models", controller.GetRoutingModels)
	console := routing.Group("", middleware.UserAuth())
	console.GET("/candidates", controller.GetRoutingCandidates)
	console.GET("/models", controller.GetRoutingModels)
	console.GET("/submissions/:submission_id", controller.GetRoutingSubmissionDetail)
	console.POST("/preview", middleware.UserCriticalRateLimit("smart-routing-preview"), controller.PreviewRouting)
	admin := routing.Group("", middleware.TokenOperationAudit(), middleware.AdminAuth())
	handlePermissionRoute(admin, http.MethodPost, "/submissions/:submission_id/reconcile", authz.RoutingReconcile,
		middleware.UserCriticalRateLimit("smart-routing-reconcile"), controller.ReconcileRoutingSubmission)
	group := api.Group("/mowan/idatariver")
	group.POST("/notify", middleware.AnonymousRequestBodyLimit(), idatariver.Notify)
	user := group.Group("", middleware.DisableCache(), middleware.UserAuth())
	user.GET("/products", idatariver.Products)
	user.POST("/order", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("idatariver_order"), idatariver.CreateOrder)
	user.POST("/pay", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("idatariver_pay"), idatariver.Pay)
	user.POST("/check", idatariver.Check)
	// Hand off persisted checkouts from the previous gateway. Order creation
	// remains on the new endpoint, which requires an idempotency key.
	legacy := api.Group("/user/idatariver")
	legacy.POST("/notify", middleware.AnonymousRequestBodyLimit(), idatariver.LegacyNotify)
	legacyUser := legacy.Group("", middleware.DisableCache(), middleware.UserAuth())
	legacyUser.POST("/pay", middleware.CriticalRateLimit(), middleware.UserCriticalRateLimit("idatariver_pay"), idatariver.Pay)
	legacyUser.POST("/check", idatariver.Check)
}
