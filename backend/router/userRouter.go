package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
	"iot-gateway/middleware"
)

// UserRoutes 用户路由
func UserRoutes(r *gin.Engine, uc *controller.UserController) {

	publicGroup := r.Group("/user")
	// 公共路由
	publicGroup.POST("/login", uc.Login)

	// 需要认证的路由
	authed := r.Group("", middleware.AuthMiddleware())
	{
		r.POST("/createUser", uc.CreateUser)
		authed.GET("/users/:id", uc.GetUser)
		authed.PUT("/users/:id", uc.UpdateUser)
		authed.GET("/users", uc.PageUser)
		authed.GET("/profile", uc.GetProfile)
	}
}
