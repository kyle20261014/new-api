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
	routing := api.Group("/mowan/smart-routing", middleware.DisableCache(), middleware.AnonymousRequestBodyLimit())
	self := routing.Group("/self", middleware.RoutingSelfAudit(), middleware.RoutingSelfAuth(), middleware.UserCriticalRateLimit("smart-routing-self"))
	self.GET("", controller.GetRoutingSelf)
	self.PUT("", controller.PutRoutingSelf)
	self.GET("/candidates", controller.GetRoutingCandidates)
	console := routing.Group("", middleware.UserAuth())
	console.GET("/candidates", controller.GetRoutingCandidates)
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
	user.POST("/check", middleware.UserCriticalRateLimit("idatariver_check"), idatariver.Check)
}
