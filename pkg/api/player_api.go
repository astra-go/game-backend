package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/astra-go/game-backend/pkg/middleware"
	"github.com/astra-go/game-backend/pkg/player"
	"github.com/astra-go/astra"
	"go.uber.org/zap"
)

// 统一响应结构
type apiResponse struct {
	Code int         `json:"code"`
	Data interface{} `json:"data,omitempty"`
	Msg  string      `json:"msg"`
}

// ResponseOK 返回成功响应
func ResponseOK(c *astra.Ctx, data interface{}) error {
	return c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: data,
		Msg:  "ok",
	})
}

// ResponseError 返回错误响应
func ResponseError(c *astra.Ctx, code int, msg string) error {
	// 根据业务错误码映射HTTP状态码
	httpCode := code
	if code >= 1000 {
		// 自定义业务错误码，统一返回400
		httpCode = http.StatusBadRequest
	}
	return c.JSON(httpCode, apiResponse{
		Code: code,
		Msg:  msg,
	})
}

// PlayerAPI 玩家API路由组
type PlayerAPI struct {
	pc     *player.PlayerComponent
	logger *zap.Logger
}

// NewPlayerAPI 创建玩家API实例
func NewPlayerAPI(pc *player.PlayerComponent, logger *zap.Logger) *PlayerAPI {
	return &PlayerAPI{
		pc:     pc,
		logger: logger,
	}
}

// RegisterRoutes 注册所有玩家相关路由
func (api *PlayerAPI) RegisterRoutes(app *astra.App) {
	// 公开路由（不需要认证）
	app.POST("/api/v1/player/register", api.Register)
	app.POST("/api/v1/player/login", api.Login)
	app.GET("/api/v1/player/leaderboard", api.Leaderboard)

	// 需要认证的路由
	app.GET("/api/v1/player/profile", api.AuthRequired(), api.GetProfile)
	app.PUT("/api/v1/player/profile", api.AuthRequired(), api.UpdateProfile)
	app.POST("/api/v1/player/logout", api.AuthRequired(), api.Logout)
	app.GET("/api/v1/player/:id", api.AuthRequired(), api.GetPlayerByID)
	app.POST("/api/v1/player/change-password", api.AuthRequired(), api.ChangePassword)
}

// AuthRequired 返回认证中间件（复用middleware包的JWT认证）
func (api *PlayerAPI) AuthRequired() astra.HandlerFunc {
	return middleware.AuthMiddleware(api.logger)
}

// Register 注册新玩家
// POST /api/v1/player/register
func (api *PlayerAPI) Register(c *astra.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("注册请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	// 参数校验
	if req.Username == "" || req.Password == "" {
		return ResponseError(c, http.StatusBadRequest, "用户名和密码不能为空")
	}

	p, err := api.pc.Register(req.Username, req.Password)
	if err != nil {
		api.logger.Warn("注册失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return c.JSON(http.StatusCreated, apiResponse{
		Code: 0,
		Data: p,
		Msg:  "ok",
	})
}

// Login 登录
// POST /api/v1/player/login
func (api *PlayerAPI) Login(c *astra.Ctx) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("登录请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	if req.Username == "" || req.Password == "" {
		return ResponseError(c, http.StatusBadRequest, "用户名和密码不能为空")
	}

	p, token, err := api.pc.Login(req.Username, req.Password)
	if err != nil {
		api.logger.Warn("登录失败", zap.String("username", req.Username), zap.Error(err))
		return ResponseError(c, http.StatusUnauthorized, err.Error())
	}

	return ResponseOK(c, map[string]interface{}{
		"player": p,
		"token":  token,
	})
}

// GetProfile 获取当前玩家个人信息
// GET /api/v1/player/profile
func (api *PlayerAPI) GetProfile(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	p, err := api.pc.GetByID(pid)
	if err != nil {
		api.logger.Warn("获取玩家信息失败", zap.String("player_id", pid), zap.Error(err))
		return ResponseError(c, http.StatusNotFound, "玩家不存在")
	}

	return ResponseOK(c, p)
}

// UpdateProfile 更新当前玩家个人信息
// PUT /api/v1/player/profile
func (api *PlayerAPI) UpdateProfile(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		Avatar   string `json:"avatar"`
		Nickname string `json:"nickname"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("更新资料请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	// 获取玩家信息
	p, err := api.pc.GetByID(pid)
	if err != nil {
		return ResponseError(c, http.StatusNotFound, "玩家不存在")
	}

	// 更新字段
	p.Avatar = req.Avatar
	p.Nickname = req.Nickname

	// 保存到数据库
	if err := api.pc.UpdatePlayer(p); err != nil {
		api.logger.Error("更新玩家资料失败", zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "更新失败")
	}

	return ResponseOK(c, p)
}

// Logout 登出
// POST /api/v1/player/logout
func (api *PlayerAPI) Logout(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	// 删除Redis中的token和在线状态
	ctx := context.Background()
	api.pc.DeleteRedisKeys(ctx, pid)

	return ResponseOK(c, map[string]string{"msg": "ok"})
}

// Leaderboard 获取排行榜
// GET /api/v1/player/leaderboard?limit=10
func (api *PlayerAPI) Leaderboard(c *astra.Ctx) error {
	limitStr := c.Query("limit")
	limit := 10 // 默认值
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	players, err := api.pc.GetLeaderboard(limit)
	if err != nil {
		api.logger.Error("获取排行榜失败", zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "获取排行榜失败")
	}

	return ResponseOK(c, players)
}

// GetPlayerByID 获取其他玩家信息
// GET /api/v1/player/:id
func (api *PlayerAPI) GetPlayerByID(c *astra.Ctx) error {
	id := c.Param("id")
	if id == "" {
		return ResponseError(c, http.StatusBadRequest, "玩家ID不能为空")
	}

	p, err := api.pc.GetByID(id)
	if err != nil {
		api.logger.Warn("获取玩家信息失败", zap.String("id", id), zap.Error(err))
		return ResponseError(c, http.StatusNotFound, "玩家不存在")
	}

	return ResponseOK(c, p)
}

// ChangePassword 修改密码
// POST /api/v1/player/change-password
func (api *PlayerAPI) ChangePassword(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	pid, ok := playerID.(string)
	if !ok || pid == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}

	if err := c.BindJSON(&req); err != nil {
		api.logger.Warn("修改密码请求参数解析失败", zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, "请求参数错误")
	}

	if req.OldPassword == "" || req.NewPassword == "" {
		return ResponseError(c, http.StatusBadRequest, "旧密码和新密码不能为空")
	}

	err := api.pc.ChangePassword(pid, req.OldPassword, req.NewPassword)
	if err != nil {
		api.logger.Warn("修改密码失败", zap.String("player_id", pid), zap.Error(err))
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]string{"msg": "密码修改成功"})
}

// FormatPlayerID 格式化玩家ID（内部使用）
func FormatPlayerID(id string) string {
	return fmt.Sprintf("player_%s", id)
}
