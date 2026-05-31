package handlers

import (
	"net/http"
	"strconv"

	"github.com/astra-go/astra/contract"

	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/internal/services"
)

// ChatHandler handles chat-related HTTP requests
type ChatHandler struct {
	chatService *services.ChatService
}

// NewChatHandler creates a new chat handler
func NewChatHandler(chatService *services.ChatService) *ChatHandler {
	return &ChatHandler{
		chatService: chatService,
	}
}

// SendMessage handles POST /api/chat/send
func (h *ChatHandler) SendMessage(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	var req struct {
		ToPlayer uint64                `json:"to_player,omitempty"`
		RoomID   uint64                `json:"room_id,omitempty"`
		GuildID  uint64                `json:"guild_id,omitempty"`
		Scope    models.ChatScope      `json:"scope" binding:"required"`
		Type     models.ChatMessageType `json:"type" binding:"required"`
		Content  string                `json:"content" binding:"required,max=500"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	msg := &models.ChatMessage{
		FromPlayer: playerID,
		ToPlayer:   req.ToPlayer,
		RoomID:     req.RoomID,
		GuildID:    req.GuildID,
		Scope:      req.Scope,
		Type:       req.Type,
		Content:    req.Content,
	}

	err := h.chatService.SendMessage(c.Request().Context(), msg)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "message sent", "id": msg.ID})
}

// GetRoomMessages handles GET /api/chat/room/:id
func (h *ChatHandler) GetRoomMessages(c contract.Context) error {
	roomID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid room id"})
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	messages, err := h.chatService.GetRoomMessages(c.Request().Context(), roomID, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"messages": messages})
}

// GetGuildMessages handles GET /api/chat/guild/:id
func (h *ChatHandler) GetGuildMessages(c contract.Context) error {
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	messages, err := h.chatService.GetGuildMessages(c.Request().Context(), guildID, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"messages": messages})
}

// GetPrivateMessages handles GET /api/chat/private/:id
func (h *ChatHandler) GetPrivateMessages(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	otherPlayerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid player id"})
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	messages, err := h.chatService.GetPrivateMessages(c.Request().Context(), playerID, otherPlayerID, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"messages": messages})
}

// MarkAsRead handles POST /api/chat/read
func (h *ChatHandler) MarkAsRead(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	var req struct {
		TargetID   uint64 `json:"target_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required,oneof=room guild private"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err := h.chatService.MarkMessagesAsRead(c.Request().Context(), playerID, req.TargetID, req.TargetType)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "marked as read"})
}

// GetUnreadCount handles GET /api/chat/unread
func (h *ChatHandler) GetUnreadCount(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	count, err := h.chatService.GetUnreadCount(c.Request().Context(), playerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"unread_count": count})
}
