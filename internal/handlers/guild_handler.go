package handlers

import (
	"net/http"
	"strconv"

	"github.com/astra-go/astra/contract"

	"github.com/astra-go/game-backend/internal/services"
)

// GuildHandler handles guild-related HTTP requests
type GuildHandler struct {
	guildService *services.GuildService
}

// NewGuildHandler creates a new guild handler
func NewGuildHandler(guildService *services.GuildService) *GuildHandler {
	return &GuildHandler{
		guildService: guildService,
	}
}

// CreateGuild handles POST /api/guilds
func (h *GuildHandler) CreateGuild(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	var req struct {
		Name        string `json:"name" binding:"required,min=2,max=50"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	guild, err := h.guildService.CreateGuild(c.Request().Context(), playerID, req.Name, req.Description, req.Icon)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, map[string]any{"guild": guild})
}

// ApplyToGuild handles POST /api/guilds/:id/apply
func (h *GuildHandler) ApplyToGuild(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	var req struct {
		Message string `json:"message"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err = h.guildService.ApplyToGuild(c.Request().Context(), guildID, playerID, req.Message)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "application submitted"})
}

// ApproveApplication handles POST /api/guilds/applications/:id/approve
func (h *GuildHandler) ApproveApplication(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	applicationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid application id"})
	}

	err = h.guildService.ApproveApplication(c.Request().Context(), applicationID, playerID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "application approved"})
}

// RejectApplication handles POST /api/guilds/applications/:id/reject
func (h *GuildHandler) RejectApplication(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	applicationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid application id"})
	}

	err = h.guildService.RejectApplication(c.Request().Context(), applicationID, playerID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "application rejected"})
}

// LeaveGuild handles POST /api/guilds/:id/leave
func (h *GuildHandler) LeaveGuild(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	err = h.guildService.LeaveGuild(c.Request().Context(), guildID, playerID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "left guild"})
}

// KickMember handles POST /api/guilds/:id/kick
func (h *GuildHandler) KickMember(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	var req struct {
		TargetID uint64 `json:"target_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err = h.guildService.KickMember(c.Request().Context(), guildID, playerID, req.TargetID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "member kicked"})
}

// PromoteMember handles POST /api/guilds/:id/promote
func (h *GuildHandler) PromoteMember(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	var req struct {
		TargetID uint64 `json:"target_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err = h.guildService.PromoteMember(c.Request().Context(), guildID, playerID, req.TargetID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "member promoted"})
}

// DemoteMember handles POST /api/guilds/:id/demote
func (h *GuildHandler) DemoteMember(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	var req struct {
		TargetID uint64 `json:"target_id" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	err = h.guildService.DemoteMember(c.Request().Context(), guildID, playerID, req.TargetID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"message": "member demoted"})
}

// GetGuildInfo handles GET /api/guilds/:id
func (h *GuildHandler) GetGuildInfo(c contract.Context) error {
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	guild, err := h.guildService.GetGuildInfo(c.Request().Context(), guildID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"guild": guild})
}

// GetGuildMembers handles GET /api/guilds/:id/members
func (h *GuildHandler) GetGuildMembers(c contract.Context) error {
	guildID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid guild id"})
	}

	members, err := h.guildService.GetGuildMembers(c.Request().Context(), guildID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{"members": members})
}

// GetPlayerGuild handles GET /api/guilds/my
func (h *GuildHandler) GetPlayerGuild(c contract.Context) error {
	playerID := c.MustGet("player_id").(uint64)

	guild, err := h.guildService.GetPlayerGuild(c.Request().Context(), playerID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}

	if guild == nil {
		return c.JSON(http.StatusNotFound, map[string]any{"error": "not in a guild"})
	}

	return c.JSON(http.StatusOK, map[string]any{"guild": guild})
}
