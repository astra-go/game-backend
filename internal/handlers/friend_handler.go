package handlers

import (
	"net/http"
	"strconv"

	"github.com/astra-go/astra/contract"

	"github.com/astra-go/game-backend/internal/services"
)

// FriendHandler handles friend-related HTTP requests
type FriendHandler struct {
	friendService *services.FriendService
}

// NewFriendHandler creates a new friend handler
func NewFriendHandler(friendService *services.FriendService) *FriendHandler {
	return &FriendHandler{
		friendService: friendService,
	}
}

// SendFriendRequest handles POST /api/friends/request
func (h *FriendHandler) SendFriendRequest(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	var req struct {
		ToPlayer uint64 `json:"to_player" binding:"required"`
		Message  string `json:"message"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err := h.friendService.SendFriendRequest(c.Request().Context(), playerID, req.ToPlayer, req.Message)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "friend request sent"})
}

// AcceptFriendRequest handles POST /api/friends/accept/:id
func (h *FriendHandler) AcceptFriendRequest(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	requestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid request id"})
	}

	err = h.friendService.AcceptFriendRequest(c.Request().Context(), requestID, playerID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "friend request accepted"})
}

// RejectFriendRequest handles POST /api/friends/reject/:id
func (h *FriendHandler) RejectFriendRequest(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	requestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid request id"})
	}

	err = h.friendService.RejectFriendRequest(c.Request().Context(), requestID, playerID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "friend request rejected"})
}

// RemoveFriend handles DELETE /api/friends/:id
func (h *FriendHandler) RemoveFriend(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	friendID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid friend id"})
	}

	err = h.friendService.RemoveFriend(c.Request().Context(), playerID, friendID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "friend removed"})
}

// GetFriendList handles GET /api/friends
func (h *FriendHandler) GetFriendList(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	friends, err := h.friendService.GetFriendList(c.Request().Context(), playerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"friends": friends})
}

// GetPendingRequests handles GET /api/friends/requests
func (h *FriendHandler) GetPendingRequests(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	requests, err := h.friendService.GetPendingRequests(c.Request().Context(), playerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"requests": requests})
}

// BlockPlayer handles POST /api/friends/block
func (h *FriendHandler) BlockPlayer(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	var req struct {
		BlockedID uint64 `json:"blocked_id" binding:"required"`
		Reason    string `json:"reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err := h.friendService.BlockPlayer(c.Request().Context(), playerID, req.BlockedID, req.Reason)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "player blocked"})
}

// UnblockPlayer handles POST /api/friends/unblock/:id
func (h *FriendHandler) UnblockPlayer(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	blockedID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid player id"})
	}

	err = h.friendService.UnblockPlayer(c.Request().Context(), playerID, blockedID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "player unblocked"})
}

// GetBlacklist handles GET /api/friends/blacklist
func (h *FriendHandler) GetBlacklist(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	blacklist, err := h.friendService.GetBlacklist(c.Request().Context(), playerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"blacklist": blacklist})
}
