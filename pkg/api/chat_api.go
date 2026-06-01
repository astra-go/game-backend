package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/pkg/middleware"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ChatServiceInterface 聊天服务接口（用于依赖注入）
type ChatServiceInterface interface {
	SendMessage(ctx context.Context, msg *models.ChatMessage) error
	GetPrivateMessages(ctx context.Context, player1, player2 uint64, limit int) ([]models.ChatMessage, error)
	GetGuildMessages(ctx context.Context, guildID uint64, limit int) ([]models.ChatMessage, error)
	GetWorldMessages(ctx context.Context, limit int) ([]models.ChatMessage, error)
	GetRoomMessages(ctx context.Context, roomID uint64, limit int) ([]models.ChatMessage, error)
	MarkMessagesAsRead(ctx context.Context, playerID, targetID uint64, targetType string) error
	GetUnreadCount(ctx context.Context, playerID uint64) (int, error)
	MutePlayer(ctx context.Context, playerID uint64, duration time.Duration) error
	UnmutePlayer(ctx context.Context, playerID uint64) error
	IsPlayerMuted(ctx context.Context, playerID uint64) (bool, error)
	DB() *gorm.DB
}

// ChatAPI 聊天系统API
type ChatAPI struct {
	chatService ChatServiceInterface
	logger     *zap.Logger
}

// NewChatAPI 创建聊天API实例
func NewChatAPI(chatService ChatServiceInterface, logger *zap.Logger) *ChatAPI {
	return &ChatAPI{
		chatService: chatService,
		logger:      logger,
	}
}

// RegisterRoutes 注册路由
func (api *ChatAPI) RegisterRoutes(app *astra.App, authMiddleware astra.HandlerFunc) {
	// 私聊
	app.POST("/api/v1/chat/private", authMiddleware, api.SendPrivateMessage)
	app.GET("/api/v1/chat/private/:user_id", authMiddleware, api.GetPrivateMessages)

	// 公会聊天
	app.POST("/api/v1/chat/guild", authMiddleware, api.SendGuildMessage)
	app.GET("/api/v1/chat/guild/:guild_id", authMiddleware, api.GetGuildMessages)

	// 世界聊天
	app.POST("/api/v1/chat/world", authMiddleware, api.SendWorldMessage)
	app.GET("/api/v1/chat/world", authMiddleware, api.GetWorldMessages)

	// 房间聊天
	app.POST("/api/v1/chat/room", authMiddleware, api.SendRoomMessage)
	app.GET("/api/v1/chat/room/:room_id", authMiddleware, api.GetRoomMessages)

	// 消息管理
	app.POST("/api/v1/chat/mark-read", authMiddleware, api.MarkMessagesAsRead)
	app.GET("/api/v1/chat/unread-count", authMiddleware, api.GetUnreadCount)

	// 禁言管理（管理员）
	app.POST("/api/v1/chat/mute", authMiddleware, api.MutePlayer)
	app.DELETE("/api/v1/chat/mute/:player_id", authMiddleware, api.UnmutePlayer)
	app.GET("/api/v1/chat/mute/:player_id/status", authMiddleware, api.GetMuteStatus)
}

// SendPrivateMessageRequest 发送私聊消息请求
type SendPrivateMessageRequest struct {
	ToPlayerID uint64                `json:"to_player_id" binding:"required"`
	Type       models.ChatMessageType `json:"type" binding:"required,oneof=text emoji system"`
	Content    string                `json:"content" binding:"required,max=500"`
}

// SendPrivateMessage 发送私聊消息
func (api *ChatAPI) SendPrivateMessage(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req SendPrivateMessageRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	message := &models.ChatMessage{
		FromPlayer: playerID,
		ToPlayer:   req.ToPlayerID,
		Scope:      models.ChatScopePrivate,
		Type:       req.Type,
		Content:    req.Content,
	}

	ctx := c.Request().Context()
	if err := api.chatService.SendMessage(ctx, message); err != nil {
		api.logger.Warn("发送私聊消息失败",
			zap.Uint64("from_player", playerID),
			zap.Uint64("to_player", req.ToPlayerID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"message": "消息已发送",
		"data":    message,
	})
}

// GetPrivateMessages 获取私聊消息历史
func (api *ChatAPI) GetPrivateMessages(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	targetIDStr := c.Param("user_id")
	if targetIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "目标用户ID不能为空")
	}

	targetID, err := strconv.ParseUint(targetIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的目标用户ID")
	}

	// 获取分页参数
	limitStr := c.Query("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, _ := strconv.Atoi(limitStr)

	if limit < 1 || limit > 100 {
		limit = 50
	}

	ctx := c.Request().Context()
	messages, err := api.chatService.GetPrivateMessages(ctx, playerID, targetID, limit)
	if err != nil {
		api.logger.Error("获取私聊消息失败",
			zap.Uint64("player_id", playerID),
			zap.Uint64("target_id", targetID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "获取消息失败")
	}

	return ResponseOK(c, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

// SendGuildMessageRequest 发送公会消息请求
type SendGuildMessageRequest struct {
	GuildID uint64                `json:"guild_id" binding:"required"`
	Type    models.ChatMessageType `json:"type" binding:"required,oneof=text emoji system"`
	Content string                `json:"content" binding:"required,max=500"`
}

// SendGuildMessage 发送公会消息
func (api *ChatAPI) SendGuildMessage(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req SendGuildMessageRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	message := &models.ChatMessage{
		FromPlayer: playerID,
		GuildID:    req.GuildID,
		Scope:      models.ChatScopeGuild,
		Type:       req.Type,
		Content:    req.Content,
	}

	ctx := c.Request().Context()
	if err := api.chatService.SendMessage(ctx, message); err != nil {
		api.logger.Warn("发送公会消息失败",
			zap.Uint64("from_player", playerID),
			zap.Uint64("guild_id", req.GuildID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"message": "消息已发送",
		"data":    message,
	})
}

// GetGuildMessages 获取公会消息历史
func (api *ChatAPI) GetGuildMessages(c *astra.Ctx) error {
	guildIDStr := c.Param("guild_id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	// 获取分页参数
	limitStr := c.Query("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, _ := strconv.Atoi(limitStr)

	if limit < 1 || limit > 100 {
		limit = 50
	}

	ctx := c.Request().Context()
	messages, err := api.chatService.GetGuildMessages(ctx, guildID, limit)
	if err != nil {
		api.logger.Error("获取公会消息失败",
			zap.Uint64("guild_id", guildID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "获取消息失败")
	}

	return ResponseOK(c, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

// SendWorldMessageRequest 发送世界消息请求
type SendWorldMessageRequest struct {
	Type    models.ChatMessageType `json:"type" binding:"required,oneof=text emoji system"`
	Content string                `json:"content" binding:"required,max=500"`
}

// SendWorldMessage 发送世界消息
func (api *ChatAPI) SendWorldMessage(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req SendWorldMessageRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	message := &models.ChatMessage{
		FromPlayer: playerID,
		Scope:      models.ChatScopeWorld,
		Type:       req.Type,
		Content:    req.Content,
	}

	ctx := c.Request().Context()
	if err := api.chatService.SendMessage(ctx, message); err != nil {
		api.logger.Warn("发送世界消息失败",
			zap.Uint64("from_player", playerID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"message": "消息已发送",
		"data":    message,
	})
}

// GetWorldMessages 获取世界消息历史
func (api *ChatAPI) GetWorldMessages(c *astra.Ctx) error {
	// 获取分页参数
	limitStr := c.Query("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, _ := strconv.Atoi(limitStr)

	if limit < 1 || limit > 100 {
		limit = 50
	}

	var messages []models.ChatMessage
	err := api.chatService.DB().Where("scope = ?", models.ChatScopeWorld).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	if err != nil {
		api.logger.Error("获取世界消息失败", zap.Error(err))
		return ResponseError(c, http.StatusInternalServerError, "获取消息失败")
	}

	// 反转消息顺序（从旧到新）
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return ResponseOK(c, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

// SendRoomMessageRequest 发送房间消息请求
type SendRoomMessageRequest struct {
	RoomID  uint64                `json:"room_id" binding:"required"`
	Type    models.ChatMessageType `json:"type" binding:"required,oneof=text emoji system"`
	Content string                `json:"content" binding:"required,max=500"`
}

// SendRoomMessage 发送房间消息
func (api *ChatAPI) SendRoomMessage(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req SendRoomMessageRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	message := &models.ChatMessage{
		FromPlayer: playerID,
		RoomID:     req.RoomID,
		Scope:      models.ChatScopeRoom,
		Type:       req.Type,
		Content:    req.Content,
	}

	ctx := c.Request().Context()
	if err := api.chatService.SendMessage(ctx, message); err != nil {
		api.logger.Warn("发送房间消息失败",
			zap.Uint64("from_player", playerID),
			zap.Uint64("room_id", req.RoomID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"message": "消息已发送",
		"data":    message,
	})
}

// GetRoomMessages 获取房间消息历史
func (api *ChatAPI) GetRoomMessages(c *astra.Ctx) error {
	roomIDStr := c.Param("room_id")
	if roomIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "房间ID不能为空")
	}

	roomID, err := strconv.ParseUint(roomIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的房间ID")
	}

	// 获取分页参数
	limitStr := c.Query("limit")
	if limitStr == "" {
		limitStr = "50"
	}
	limit, _ := strconv.Atoi(limitStr)

	if limit < 1 || limit > 100 {
		limit = 50
	}

	ctx := c.Request().Context()
	messages, err := api.chatService.GetRoomMessages(ctx, roomID, limit)
	if err != nil {
		api.logger.Error("获取房间消息失败",
			zap.Uint64("room_id", roomID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "获取消息失败")
	}

	return ResponseOK(c, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

// MarkMessagesAsReadRequest 标记消息已读请求
type MarkMessagesAsReadRequest struct {
	TargetID   uint64 `json:"target_id" binding:"required"`
	TargetType string `json:"target_type" binding:"required,oneof=private guild room world"`
}

// MarkMessagesAsRead 标记消息已读
func (api *ChatAPI) MarkMessagesAsRead(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req MarkMessagesAsReadRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	ctx := c.Request().Context()
	if err := api.chatService.MarkMessagesAsRead(ctx, playerID, req.TargetID, req.TargetType); err != nil {
		api.logger.Error("标记消息已读失败",
			zap.Uint64("player_id", playerID),
			zap.Uint64("target_id", req.TargetID),
			zap.String("target_type", req.TargetType),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "标记已读失败")
	}

	return ResponseOK(c, map[string]any{
		"message": "消息已标记为已读",
	})
}

// GetUnreadCount 获取未读消息数量
func (api *ChatAPI) GetUnreadCount(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	ctx := c.Request().Context()
	count, err := api.chatService.GetUnreadCount(ctx, playerID)
	if err != nil {
		api.logger.Error("获取未读消息数量失败",
			zap.Uint64("player_id", playerID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "获取未读消息失败")
	}

	return ResponseOK(c, map[string]any{
		"unread_count": count,
	})
}

// MutePlayerRequest 禁言玩家请求
type MutePlayerRequest struct {
	PlayerID uint64        `json:"player_id" binding:"required"`
	Duration time.Duration `json:"duration" binding:"required"` // 禁言时长（秒）
}

// MutePlayer 禁言玩家
func (api *ChatAPI) MutePlayer(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	// TODO: 检查是否为管理员

	var req MutePlayerRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	ctx := c.Request().Context()
	if err := api.chatService.MutePlayer(ctx, req.PlayerID, req.Duration); err != nil {
		api.logger.Error("禁言玩家失败",
			zap.Uint64("target_player_id", req.PlayerID),
			zap.Duration("duration", req.Duration),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "禁言失败")
	}

	return ResponseOK(c, map[string]any{
		"message": "玩家已被禁言",
		"duration": req.Duration.Seconds(),
	})
}

// UnmutePlayer 解除禁言
func (api *ChatAPI) UnmutePlayer(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	// TODO: 检查是否为管理员

	targetIDStr := c.Param("player_id")
	if targetIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "玩家ID不能为空")
	}

	targetID, err := strconv.ParseUint(targetIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的玩家ID")
	}

	ctx := c.Request().Context()
	if err := api.chatService.UnmutePlayer(ctx, targetID); err != nil {
		api.logger.Error("解除禁言失败",
			zap.Uint64("target_player_id", targetID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "解除禁言失败")
	}

	return ResponseOK(c, map[string]any{
		"message": "玩家禁言已解除",
	})
}

// GetMuteStatus 获取玩家禁言状态
func (api *ChatAPI) GetMuteStatus(c *astra.Ctx) error {
	targetIDStr := c.Param("player_id")
	if targetIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "玩家ID不能为空")
	}

	targetID, err := strconv.ParseUint(targetIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的玩家ID")
	}

	ctx := c.Request().Context()
	isMuted, err := api.chatService.IsPlayerMuted(ctx, targetID)
	if err != nil {
		api.logger.Error("获取禁言状态失败",
			zap.Uint64("target_player_id", targetID),
			zap.Error(err),
		)
		return ResponseError(c, http.StatusInternalServerError, "获取禁言状态失败")
	}

	return ResponseOK(c, map[string]any{
		"player_id":  targetID,
		"is_muted":   isMuted,
	})
}
