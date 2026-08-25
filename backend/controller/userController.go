package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// UserController 用户控制器
type UserController struct {
	userService *service.UserService
}

// NewUserController 创建用户控制器
func NewUserController(userService *service.UserService) *UserController {
	return &UserController{userService: userService}
}

// CreateUser 创建用户
func (ctr *UserController) CreateUser(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateUserDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	err := ctr.userService.CreateUser(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetUser 获取用户
func (ctr *UserController) GetUser(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	data, err := ctr.userService.GetUser(ctx, id)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// UpdateUser 更新用户
func (ctr *UserController) UpdateUser(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	var req dto.UpdateUserDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.userService.UpdateUser(ctx, id, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// Login 用户登录
func (ctr *UserController) Login(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.LoginDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.userService.Login(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageUser 用户分页查询
func (ctr *UserController) PageUser(c *gin.Context) {
	ctx := c.Request.Context()
	var pageDTO dto.PageDTO
	if err := c.ShouldBindQuery(&pageDTO); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.userService.PageUser(ctx, &pageDTO)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// GetProfile 获取当前用户信息
func (ctr *UserController) GetProfile(c *gin.Context) {
	ctx := c.Request.Context()
	userId, _ := c.Get("userId")
	data, err := ctr.userService.GetUser(ctx, userId.(string))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
