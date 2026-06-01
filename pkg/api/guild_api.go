package api

import (
	"net/http"
	"strconv"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/pkg/guild"
	"github.com/astra-go/game-backend/pkg/middleware"
)

// GuildAPI 公会API
type GuildAPI struct {
	guildComponent guild.GuildService
}

// NewGuildAPI 创建公会API
func NewGuildAPI(guildComponent guild.GuildService) *GuildAPI {
	return &GuildAPI{
		guildComponent: guildComponent,
	}
}

// RegisterRoutes 注册路由
func (api *GuildAPI) RegisterRoutes(app *astra.App, authMiddleware astra.HandlerFunc) {
	// 公开接口
	app.GET("/api/v1/guild/list", api.ListGuilds)
	app.GET("/api/v1/guild/:id", api.GetGuild)
	app.GET("/api/v1/guild/:id/members", api.GetMembers)

	// 需要认证的接口
	app.POST("/api/v1/guild/create", authMiddleware, api.CreateGuild)
	app.DELETE("/api/v1/guild/:id/dissolve", authMiddleware, api.DissolveGuild)
	app.POST("/api/v1/guild/:id/invite", authMiddleware, api.InviteMember)
	app.DELETE("/api/v1/guild/:id/kick", authMiddleware, api.KickMember)
	app.POST("/api/v1/guild/:id/leave", authMiddleware, api.LeaveGuild)
	app.PUT("/api/v1/guild/:id/promote", authMiddleware, api.PromoteMember)
	app.PUT("/api/v1/guild/:id/demote", authMiddleware, api.DemoteMember)
	app.PUT("/api/v1/guild/:id/transfer", authMiddleware, api.TransferLeadership)
	app.PUT("/api/v1/guild/:id/info", authMiddleware, api.UpdateGuildInfo)
	app.GET("/api/v1/guild/my", authMiddleware, api.GetMyGuild)
}

// CreateGuildRequest 创建公会请求
type CreateGuildRequest struct {
	Name        string `json:"name" binding:"required,min=2,max=20"`
	Description string `json:"description" binding:"max=200"`
}

// CreateGuild 创建公会
func (api *GuildAPI) CreateGuild(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	var req CreateGuildRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	guild, err := api.guildComponent.CreateGuild(playerID, req.Name, req.Description, "")
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"message": "公会创建成功",
		"guild":   guild,
	})
}

// DissolveGuild 解散公会
func (api *GuildAPI) DissolveGuild(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	err = api.guildComponent.DissolveGuild(guildID, playerID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "公会已解散"})
}

// InviteMemberRequest 邀请成员请求
type InviteMemberRequest struct {
	TargetID uint64 `json:"target_id" binding:"required"`
}

// InviteMember 邀请成员
func (api *GuildAPI) InviteMember(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req InviteMemberRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.InviteMember(guildID, playerID, req.TargetID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "成员邀请成功"})
}

// KickMemberRequest 踢出成员请求
type KickMemberRequest struct {
	TargetID uint64 `json:"target_id" binding:"required"`
}

// KickMember 踢出成员
func (api *GuildAPI) KickMember(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req KickMemberRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.KickMember(guildID, playerID, req.TargetID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "成员已踢出"})
}

// LeaveGuild 离开公会
func (api *GuildAPI) LeaveGuild(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	err = api.guildComponent.LeaveGuild(guildID, playerID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "已离开公会"})
}

// PromoteMemberRequest 提升成员请求
type PromoteMemberRequest struct {
	TargetID uint64 `json:"target_id" binding:"required"`
	NewRole  string `json:"new_role" binding:"required,oneof=officer member"`
}

// PromoteMember 提升成员
func (api *GuildAPI) PromoteMember(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req PromoteMemberRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.PromoteMember(guildID, playerID, req.TargetID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "成员已提升"})
}

// DemoteMemberRequest 降级成员请求
type DemoteMemberRequest struct {
	TargetID uint64 `json:"target_id" binding:"required"`
}

// DemoteMember 降级成员
func (api *GuildAPI) DemoteMember(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req DemoteMemberRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.DemoteMember(guildID, playerID, req.TargetID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "成员已降级"})
}

// TransferLeadershipRequest 转让会长请求
type TransferLeadershipRequest struct {
	NewLeaderID uint64 `json:"new_leader_id" binding:"required"`
}

// TransferLeadership 转让会长
func (api *GuildAPI) TransferLeadership(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req TransferLeadershipRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.TransferLeadership(guildID, playerID, req.NewLeaderID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "会长已转让"})
}

// UpdateGuildInfoRequest 更新公会信息请求
type UpdateGuildInfoRequest struct {
	Name        string `json:"name" binding:"max=50"`
	Description string `json:"description" binding:"max=200"`
	Icon        string `json:"icon" binding:"max=255"`
}

// UpdateGuildInfo 更新公会信息
func (api *GuildAPI) UpdateGuildInfo(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	var req UpdateGuildInfoRequest
	if err := c.BindJSON(&req); err != nil {
		return ResponseError(c, http.StatusBadRequest, "参数错误: "+err.Error())
	}

	err = api.guildComponent.UpdateGuildInfo(guildID, playerID, req.Name, req.Description, req.Icon)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{"message": "公会信息已更新"})
}

// GetGuild 获取公会信息
func (api *GuildAPI) GetGuild(c *astra.Ctx) error {
	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	guild, err := api.guildComponent.GetGuild(guildID)
	if err != nil {
		return ResponseError(c, http.StatusNotFound, err.Error())
	}

	return ResponseOK(c, guild)
}

// GetMembers 获取公会成员列表
func (api *GuildAPI) GetMembers(c *astra.Ctx) error {
	guildIDStr := c.Param("id")
	if guildIDStr == "" {
		return ResponseError(c, http.StatusBadRequest, "公会ID不能为空")
	}

	guildID, err := strconv.ParseUint(guildIDStr, 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的公会ID")
	}

	members, err := api.guildComponent.GetMembers(guildID)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"members": members,
		"count":   len(members),
	})
}

// GetMyGuild 获取我的公会
func (api *GuildAPI) GetMyGuild(c *astra.Ctx) error {
	playerIDStr, ok := c.Get(middleware.ContextKeyPlayerID)
	if !ok || playerIDStr == "" {
		return ResponseError(c, http.StatusUnauthorized, "未授权")
	}

	playerID, err := strconv.ParseUint(playerIDStr.(string), 10, 64)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, "无效的用户ID")
	}

	guild, err := api.guildComponent.GetPlayerGuild(playerID)
	if err != nil {
		return ResponseError(c, http.StatusNotFound, err.Error())
	}

	return ResponseOK(c, guild)
}

// ListGuilds 获取公会列表
func (api *GuildAPI) ListGuilds(c *astra.Ctx) error {
	pageStr := c.Query("page")
	if pageStr == "" {
		pageStr = "1"
	}
	page, _ := strconv.Atoi(pageStr)

	pageSizeStr := c.Query("page_size")
	if pageSizeStr == "" {
		pageSizeStr = "20"
	}
	pageSize, _ := strconv.Atoi(pageSizeStr)

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	guilds, total, err := api.guildComponent.ListGuilds(page, pageSize)
	if err != nil {
		return ResponseError(c, http.StatusBadRequest, err.Error())
	}

	return ResponseOK(c, map[string]any{
		"guilds":    guilds,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}
