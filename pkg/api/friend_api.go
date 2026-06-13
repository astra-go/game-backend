package api

import (
	"github.com/astra-go/astra"
	"github.com/astra-go/astra/log"
	"github.com/astra-go/game-backend/pkg/friend"
	"github.com/astra-go/game-backend/pkg/middleware"
)

// FriendAPI 好友系统API
type FriendAPI struct {
	fc     *friend.FriendComponent
	logger *log.Logger
}

// NewFriendAPI 创建好友API实例
func NewFriendAPI(fc *friend.FriendComponent, logger *log.Logger) *FriendAPI {
	return &FriendAPI{
		fc:     fc,
		logger: logger,
	}
}

// AuthRequired 认证中间件
func (api *FriendAPI) AuthRequired() astra.HandlerFunc {
	return middleware.AuthMiddleware(api.logger)
}

// RegisterRoutes 注册路由
func (api *FriendAPI) RegisterRoutes(app *astra.App) {
	app.POST("/api/v1/friend/request", api.AuthRequired(), api.SendRequest)
	app.POST("/api/v1/friend/accept", api.AuthRequired(), api.AcceptRequest)
	app.POST("/api/v1/friend/reject", api.AuthRequired(), api.RejectRequest)
	app.DELETE("/api/v1/friend/:friend_id", api.AuthRequired(), api.DeleteFriend)
	app.GET("/api/v1/friend/list", api.AuthRequired(), api.GetFriendList)
	app.GET("/api/v1/friend/requests", api.AuthRequired(), api.GetPendingRequests)
	app.GET("/api/v1/friend/:friend_id/online", api.AuthRequired(), api.GetOnlineStatus)
}

// SendRequest 发送好友请求
func (api *FriendAPI) SendRequest(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)

	var req struct {
		TargetID string `json:"target_id" binding:"required"`
		Message  string `json:"message"`
	}

	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误: "+err.Error())
	}

	err := api.fc.SendRequest(playerID.(string), req.TargetID, req.Message)
	if err != nil {
		api.logger.Warn("发送好友请求失败",
			"player_id", playerID,
			"target_id", req.TargetID,
			"error", err,
		)

		return ResponseError(c, 400, err.Error())
	}

	return ResponseOK(c, map[string]string{
		"message": "好友请求已发送",
	})
}

// AcceptRequest 接受好友请求
func (api *FriendAPI) AcceptRequest(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)

	var req struct {
		RequestID string `json:"request_id" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误: "+err.Error())
	}

	err := api.fc.AcceptRequest(req.RequestID, playerID.(string))
	if err != nil {
		api.logger.Warn("接受好友请求失败",
			"player_id", playerID,
			"request_id", req.RequestID,
			"error", err,
		)
		return ResponseError(c, 400, err.Error())
	}

	return ResponseOK(c, map[string]string{
		"message": "好友请求已接受",
	})
}

// RejectRequest 拒绝好友请求
func (api *FriendAPI) RejectRequest(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)

	var req struct {
		RequestID string `json:"request_id" binding:"required"`
	}

	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, 400, "参数错误: "+err.Error())
	}

	err := api.fc.RejectRequest(req.RequestID, playerID.(string))
	if err != nil {
		api.logger.Warn("拒绝好友请求失败",
			"player_id", playerID,
			"request_id", req.RequestID,
			"error", err,
		)
		return ResponseError(c, 400, err.Error())
	}

	return ResponseOK(c, map[string]string{
		"message": "好友请求已拒绝",
	})
}

// DeleteFriend 删除好友
func (api *FriendAPI) DeleteFriend(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	friendID := c.Param("friend_id")

	if friendID == "" {
		return ResponseError(c, 400, "friend_id 不能为空")
	}

	err := api.fc.DeleteFriend(playerID.(string), friendID)
	if err != nil {
		api.logger.Warn("删除好友失败",
			"player_id", playerID,
			"friend_id", friendID,
			"error", err,
		)
		return ResponseError(c, 400, err.Error())
	}

	return ResponseOK(c, map[string]string{
		"message": "好友已删除",
	})
}

// GetFriendList 获取好友列表
func (api *FriendAPI) GetFriendList(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)

	friends, err := api.fc.GetFriendList(c.Request().Context(), playerID.(string))
	if err != nil {
		api.logger.Error("获取好友列表失败",
			"player_id", playerID,
			"error", err,
		)
		return ResponseError(c, 500, "获取好友列表失败")
	}

	return ResponseOK(c, map[string]any{
		"friends": friends,
		"count":   len(friends),
	})
}

// GetPendingRequests 获取待处理的好友请求
func (api *FriendAPI) GetPendingRequests(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)

	requests, err := api.fc.GetPendingRequests(playerID.(string))
	if err != nil {
		api.logger.Error("获取好友请求失败",
			"player_id", playerID,
			"error", err,
		)
		return ResponseError(c, 500, "获取好友请求失败")
	}

	return ResponseOK(c, map[string]any{
		"requests": requests,
		"count":    len(requests),
	})
}

// GetOnlineStatus 获取好友在线状态
func (api *FriendAPI) GetOnlineStatus(c *astra.Ctx) error {
	playerID, _ := c.Get(middleware.ContextKeyPlayerID)
	friendID := c.Param("friend_id")

	if friendID == "" {
		return ResponseError(c, 400, "friend_id 不能为空")
	}

	// 验证是否为好友关系
	friends, err := api.fc.GetFriendList(c.Request().Context(), playerID.(string))
	if err != nil {
		api.logger.Error("获取好友列表失败",
			"player_id", playerID,
			"error", err,
		)
		return ResponseError(c, 500, "查询失败")
	}

	isFriend := false
	var onlineStatus bool
	for _, friend := range friends {
		if friend.PlayerID == friendID {
			isFriend = true
			onlineStatus = friend.Online
			break
		}
	}

	if !isFriend {
		return ResponseError(c, 403, "不是好友关系")
	}

	return ResponseOK(c, map[string]any{
		"friend_id": friendID,
		"online":    onlineStatus,
	})
}
