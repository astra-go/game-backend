package guild

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/astra-go/game-backend/internal/models"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/astra-go/game-backend/pkg/natsclient"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var ctx = context.Background()

const (
	defaultMemberLimit      = 50
	defaultMaxGuildsPerUser = 1
	cacheTTL                = 5 * time.Minute
	localCacheSize          = 1000
	distributedLockTTL      = 10 * time.Second
)

// GuildRole 公会角色
type GuildRole = models.GuildRole

const (
	RoleMaster   GuildRole = models.GuildRoleMaster
	RoleOfficer GuildRole = models.GuildRoleOfficer
	RoleMember   GuildRole = models.GuildRoleMember
)

// GuildPermission 公会权限
type GuildPermission string

const (
	PermInviteMember   GuildPermission = "invite_member"
	PermKickMember     GuildPermission = "kick_member"
	PermPromoteMember  GuildPermission = "promote_member"
	PermDemoteMember   GuildPermission = "demote_member"
	PermEditGuildInfo  GuildPermission = "edit_guild_info"
	PermDissolveGuild  GuildPermission = "dissolve_guild"
	PermManageAlliance GuildPermission = "manage_alliance"
)

// rolePermissions 角色权限映射
var rolePermissions = map[GuildRole][]GuildPermission{
	RoleMaster: {
		PermInviteMember,
		PermKickMember,
		PermPromoteMember,
		PermDemoteMember,
		PermEditGuildInfo,
		PermDissolveGuild,
		PermManageAlliance,
	},
	RoleOfficer: {
		PermInviteMember,
		PermKickMember,
	},
	RoleMember: {},
}

// GuildService 公会服务接口
type GuildService interface {
	CreateGuild(leaderID uint64, name, description, icon string) (*models.Guild, error)
	DissolveGuild(guildID, operatorID uint64) error
	InviteMember(guildID, inviterID, targetID uint64) error
	KickMember(guildID, operatorID, targetID uint64) error
	LeaveGuild(guildID, playerID uint64) error
	PromoteMember(guildID, operatorID, targetID uint64) error
	DemoteMember(guildID, operatorID, targetID uint64) error
	TransferLeadership(guildID, currentLeaderID, newLeaderID uint64) error
	UpdateGuildInfo(guildID, operatorID uint64, name, description, icon string) error
	GetGuild(guildID uint64) (*models.Guild, error)
	GetMembers(guildID uint64) ([]models.GuildMember, error)
	GetPlayerGuild(playerID uint64) (*models.Guild, error)
	ListGuilds(page, pageSize int) ([]models.Guild, int64, error)
	ApplyToGuild(guildID, playerID uint64, message string) error
	ApproveApplication(applicationID, approverID uint64) error
	RejectApplication(applicationID, approverID uint64) error
}

// GuildComponent 公会服务组件
type GuildComponent struct {
	db         *gorm.DB
	redis      *redis.Client
	nc         natsclient.Client
	logger     *zap.Logger
	localCache *lru.Cache[uint64, *models.Guild]
	mu         sync.RWMutex
	service     *GuildService
}

// NewGuildComponent 创建公会组件
func NewGuildComponent(db *gorm.DB, redis *redis.Client, nc natsclient.Client, logger *zap.Logger) *GuildComponent {
	localCache, _ := lru.New[uint64, *models.Guild](localCacheSize)

	return &GuildComponent{
		db:         db,
		redis:      redis,
		nc:         nc,
		logger:     logger,
		localCache: localCache,
	}
}

// Init 初始化
func (g *GuildComponent) Init() error {
	g.logger.Info("GuildComponent 初始化")

	// 自动迁移数据库
	err := g.db.AutoMigrate(&models.Guild{}, &models.GuildMember{}, &models.GuildApplication{})
	if err != nil {
		return err
	}

	g.logger.Info("GuildComponent 初始化完成")
	return nil
}

// CreateGuild 创建公会
func (g *GuildComponent) CreateGuild(leaderID uint64, name, description, icon string) (*models.Guild, error) {
	// 检查玩家是否已加入其他公会
	var existingMember models.GuildMember
	err := g.db.Where("player_id = ?", leaderID).First(&existingMember).Error
	if err == nil {
		return nil, errors.New("您已加入其他公会")
	}

	// 检查公会名称是否重复
	var existingGuild models.Guild
	err = g.db.Where("name = ?", name).First(&existingGuild).Error
	if err == nil {
		return nil, errors.New("公会名称已被使用")
	}

	// 开启事务：创建公会 + 添加会长
	var guild *models.Guild
	err = g.db.Transaction(func(tx *gorm.DB) error {
		// 创建公会
		guild = &models.Guild{
			Name:        name,
			Description: description,
			MasterID:    leaderID,
			Level:       1,
			MaxMembers:  defaultMemberLimit,
			Icon:        icon,
		}

		if err := tx.Create(guild).Error; err != nil {
			return err
		}

		// 添加会长为成员
		member := &models.GuildMember{
			GuildID:      guild.ID,
			PlayerID:     leaderID,
			Role:         models.GuildRoleMaster,
			Contribution: 0,
		}

		if err := tx.Create(member).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("创建公会失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guild.ID)

	g.logger.Info("公会已创建",
		zap.Uint64("guild_id", guild.ID),
		zap.String("name", name),
		zap.Uint64("leader_id", leaderID),
	)

	// NATS通知
	g.publishGuildCreatedNotification(guild)

	return guild, nil
}

// DissolveGuild 解散公会
func (g *GuildComponent) DissolveGuild(guildID, operatorID uint64) error {
	// 获取公会信息
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	// 只有会长可以解散
	if guild.MasterID != operatorID {
		return errors.New("只有会长可以解散公会")
	}

	// 获取所有成员
	members, err := g.GetMembers(guildID)
	if err != nil {
		return err
	}

	// 开启事务：删除公会 + 删除所有成员
	err = g.db.Transaction(func(tx *gorm.DB) error {
		// 删除所有成员
		if err := tx.Where("guild_id = ?", guildID).Delete(&models.GuildMember{}).Error; err != nil {
			return err
		}

		// 删除所有申请
		if err := tx.Where("guild_id = ?", guildID).Delete(&models.GuildApplication{}).Error; err != nil {
			return err
		}

		// 删除公会
		if err := tx.Delete(&models.Guild{}, guild.ID).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("解散公会失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("公会已解散",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("operator_id", operatorID),
	)

	// NATS通知所有成员
	for _, member := range members {
		g.publishGuildDissolvedNotification(member.PlayerID, guildID)
	}

	return nil
}

// InviteMember 邀请成员
func (g *GuildComponent) InviteMember(guildID, inviterID, targetID uint64) error {
	// 检查权限
	if !g.hasPermission(guildID, inviterID, PermInviteMember) {
		return errors.New("没有权限邀请成员")
	}

	// 检查目标玩家是否已加入其他公会
	var existingMember models.GuildMember
	err := g.db.Where("player_id = ?", targetID).First(&existingMember).Error
	if err == nil {
		return errors.New("该玩家已加入其他公会")
	}

	// 检查公会人数限制
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	memberCount, err := g.GetMemberCount(guildID)
	if err != nil {
		return err
	}

	if memberCount >= guild.MaxMembers {
		return fmt.Errorf("公会人数已达上限（%d）", guild.MaxMembers)
	}

	// 创建申请
	application := &models.GuildApplication{
		GuildID:  guildID,
		PlayerID: targetID,
		Message:  fmt.Sprintf("Invited by %d", inviterID),
		Status:   "pending",
	}

	err = g.db.Create(application).Error
	if err != nil {
		return fmt.Errorf("邀请成员失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("成员已邀请",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("player_id", targetID),
		zap.Uint64("inviter_id", inviterID),
	)

	return nil
}

// KickMember 踢出成员
func (g *GuildComponent) KickMember(guildID, operatorID, targetID uint64) error {
	// 检查权限
	if !g.hasPermission(guildID, operatorID, PermKickMember) {
		return errors.New("没有权限踢出成员")
	}

	// 不能踢出会长
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	if guild.MasterID == targetID {
		return errors.New("不能踢出会长")
	}

	// 获取目标成员信息
	var targetMember models.GuildMember
	err = g.db.Where("guild_id = ? AND player_id = ?", guildID, targetID).First(&targetMember).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("该玩家不是公会成员")
		}
		return err
	}

	// 官员不能踢出官员
	var operatorMember models.GuildMember
	err = g.db.Where("guild_id = ? AND player_id = ?", guildID, operatorID).First(&operatorMember).Error
	if err != nil {
		return err
	}

	if operatorMember.Role == RoleOfficer && targetMember.Role == RoleOfficer {
		return errors.New("官员不能踢出其他官员")
	}

	// 删除成员
	err = g.db.Where("guild_id = ? AND player_id = ?", guildID, targetID).Delete(&models.GuildMember{}).Error
	if err != nil {
		return fmt.Errorf("踢出成员失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("成员已被踢出公会",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("player_id", targetID),
		zap.Uint64("operator_id", operatorID),
	)

	// NATS通知
	g.publishMemberKickedNotification(guildID, targetID)

	return nil
}

// LeaveGuild 离开公会
func (g *GuildComponent) LeaveGuild(guildID, playerID uint64) error {
	// 获取公会信息
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	// 会长不能直接离开，需要先转让会长或解散公会
	if guild.MasterID == playerID {
		return errors.New("会长不能直接离开公会，请先转让会长或解散公会")
	}

	// 删除成员
	err = g.db.Where("guild_id = ? AND player_id = ?", guildID, playerID).Delete(&models.GuildMember{}).Error
	if err != nil {
		return fmt.Errorf("离开公会失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("成员已离开公会",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("player_id", playerID),
	)

	// NATS通知
	g.publishMemberLeftNotification(guildID, playerID)

	return nil
}

// PromoteMember 提升成员
func (g *GuildComponent) PromoteMember(guildID, operatorID, targetID uint64) error {
	// 检查权限 - 只有会长可以提升
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	if guild.MasterID != operatorID {
		return errors.New("只有会长可以提升成员")
	}

	// 不能提升为会长
	if targetID == guild.MasterID {
		return errors.New("不能提升会长")
	}

	// 更新成员角色
	err = g.db.Model(&models.GuildMember{}).
		Where("guild_id = ? AND player_id = ?", guildID, targetID).
		Update("role", string(RoleOfficer)).Error

	if err != nil {
		return fmt.Errorf("提升成员失败: %w", err)
	}

	g.logger.Info("成员已提升",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("player_id", targetID),
		zap.String("new_role", string(RoleOfficer)),
	)

	// NATS通知
	g.publishMemberRoleChangedNotification(guildID, targetID, string(RoleOfficer))

	return nil
}

// DemoteMember 降级成员
func (g *GuildComponent) DemoteMember(guildID, operatorID, targetID uint64) error {
	// 检查权限 - 只有会长可以降级
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	if guild.MasterID != operatorID {
		return errors.New("只有会长可以降级成员")
	}

	// 不能降级会长
	if guild.MasterID == targetID {
		return errors.New("不能降级会长")
	}

	// 更新成员角色为普通成员
	err = g.db.Model(&models.GuildMember{}).
		Where("guild_id = ? AND player_id = ?", guildID, targetID).
		Update("role", string(RoleMember)).Error

	if err != nil {
		return fmt.Errorf("降级成员失败: %w", err)
	}

	g.logger.Info("成员已降级",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("player_id", targetID),
	)

	// NATS通知
	g.publishMemberRoleChangedNotification(guildID, targetID, string(RoleMember))

	return nil
}

// TransferLeadership 转让会长
func (g *GuildComponent) TransferLeadership(guildID, currentLeaderID, newLeaderID uint64) error {
	// 获取公会信息
	guild, err := g.GetGuild(guildID)
	if err != nil {
		return err
	}

	// 只有当前会长可以转让
	if guild.MasterID != currentLeaderID {
		return errors.New("只有会长可以转让会长")
	}

	// 检查新会长是否是公会成员
	var newLeaderMember models.GuildMember
	err = g.db.Where("guild_id = ? AND player_id = ?", guildID, newLeaderID).First(&newLeaderMember).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("目标玩家不是公会成员")
		}
		return err
	}

	// 开启事务：更新公会会长 + 更新成员角色
	err = g.db.Transaction(func(tx *gorm.DB) error {
		// 更新公会会长
		if err := tx.Model(&models.Guild{}).Where("id = ?", guildID).Update("master_id", newLeaderID).Error; err != nil {
			return err
		}

		// 将新会长角色设为master
		if err := tx.Model(&models.GuildMember{}).
			Where("guild_id = ? AND player_id = ?", guildID, newLeaderID).
			Update("role", string(RoleMaster)).Error; err != nil {
			return err
		}

		// 将原会长角色设为member
		if err := tx.Model(&models.GuildMember{}).
			Where("guild_id = ? AND player_id = ?", guildID, currentLeaderID).
			Update("role", string(RoleMember)).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("转让会长失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("会长已转让",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("old_leader_id", currentLeaderID),
		zap.Uint64("new_leader_id", newLeaderID),
	)

	// NATS通知
	g.publishLeadershipTransferredNotification(guildID, currentLeaderID, newLeaderID)

	return nil
}

// UpdateGuildInfo 更新公会信息
func (g *GuildComponent) UpdateGuildInfo(guildID, operatorID uint64, name, description, icon string) error {
	// 检查权限
	if !g.hasPermission(guildID, operatorID, PermEditGuildInfo) {
		return errors.New("没有权限编辑公会信息")
	}

	// 更新公会信息
	updates := map[string]any{}
	if name != "" {
		updates["name"] = name
	}
	if description != "" {
		updates["description"] = description
	}
	if icon != "" {
		updates["icon"] = icon
	}

	err := g.db.Model(&models.Guild{}).
		Where("id = ?", guildID).
		Updates(updates).Error

	if err != nil {
		return fmt.Errorf("更新公会信息失败: %w", err)
	}

	// 清除缓存
	g.invalidateCache(guildID)

	g.logger.Info("公会信息已更新",
		zap.Uint64("guild_id", guildID),
		zap.Uint64("operator_id", operatorID),
	)

	return nil
}

// GetGuild 获取公会信息
func (g *GuildComponent) GetGuild(guildID uint64) (*models.Guild, error) {
	// 本地缓存查询
	if cached, ok := g.localCache.Get(guildID); ok {
		g.logger.Debug("公会信息命中本地缓存", zap.Uint64("guild_id", guildID))
		return cached, nil
	}

	// MySQL查询
	var guild models.Guild
	err := g.db.First(&guild, guildID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("公会不存在")
		}
		return nil, fmt.Errorf("查询公会失败: %w", err)
	}

	// 写入本地缓存
	g.localCache.Add(guildID, &guild)

	return &guild, nil
}

// GetMembers 获取公会成员列表
func (g *GuildComponent) GetMembers(guildID uint64) ([]models.GuildMember, error) {
	var members []models.GuildMember
	err := g.db.Where("guild_id = ?", guildID).Order("role ASC, joined_at ASC").Find(&members).Error
	if err != nil {
		return nil, fmt.Errorf("查询公会成员失败: %w", err)
	}

	return members, nil
}

// GetPlayerGuild 获取玩家所在的公会
func (g *GuildComponent) GetPlayerGuild(playerID uint64) (*models.Guild, error) {
	var member models.GuildMember
	err := g.db.Where("player_id = ?", playerID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("未加入任何公会")
		}
		return nil, err
	}

	return g.GetGuild(member.GuildID)
}

// ListGuilds 获取公会列表
func (g *GuildComponent) ListGuilds(page, pageSize int) ([]models.Guild, int64, error) {
	var guilds []models.Guild
	var total int64

	// 查询总数
	err := g.db.Model(&models.Guild{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 分页查询
	offset := (page - 1) * pageSize
	err = g.db.Order("level DESC, id ASC").
		Limit(pageSize).
		Offset(offset).
		Find(&guilds).Error

	if err != nil {
		return nil, 0, fmt.Errorf("查询公会列表失败: %w", err)
	}

	return guilds, total, nil
}

// ApplyToGuild 申请加入公会
func (g *GuildComponent) ApplyToGuild(guildID, playerID uint64, message string) error {
	// 检查玩家是否已加入其他公会
	hasGuild, err := g.HasGuild(playerID)
	if err != nil {
		return err
	}
	if hasGuild {
		return errors.New("您已加入其他公会")
	}

	// 检查是否已申请
	var existingApp models.GuildApplication
	err = g.db.Where("guild_id = ? AND player_id = ? AND status = ?", guildID, playerID, "pending").
		First(&existingApp).Error
	if err == nil {
		return errors.New("您已申请过该公会")
	}

	// 创建申请
	application := &models.GuildApplication{
		GuildID:  guildID,
		PlayerID: playerID,
		Message:  message,
		Status:   "pending",
	}

	return g.db.Create(application).Error
}

// ApproveApplication 批准入会申请
func (g *GuildComponent) ApproveApplication(applicationID, approverID uint64) error {
	var application models.GuildApplication
	if err := g.db.First(&application, applicationID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("申请不存在")
		}
		return err
	}

	if application.Status != "pending" {
		return errors.New("申请已处理")
	}

	// 检查权限
	canApprove, err := g.CanManageMembers(application.GuildID, approverID)
	if err != nil {
		return err
	}
	if !canApprove {
		return errors.New("没有权限批准申请")
	}

	// 检查公会人数
	guild, err := g.GetGuild(application.GuildID)
	if err != nil {
		return err
	}

	memberCount, err := g.GetMemberCount(application.GuildID)
	if err != nil {
		return err
	}

	if memberCount >= guild.MaxMembers {
		return errors.New("公会人数已达上限")
	}

	// 开启事务
	return g.db.Transaction(func(tx *gorm.DB) error {
		// 更新申请状态
		application.Status = "approved"
		if err := tx.Save(&application).Error; err != nil {
			return err
		}

		// 添加成员
		member := &models.GuildMember{
			GuildID:      application.GuildID,
			PlayerID:     application.PlayerID,
			Role:         models.GuildRoleMember,
			Contribution: 0,
		}
		return tx.Create(member).Error
	})
}

// RejectApplication 拒绝入会申请
func (g *GuildComponent) RejectApplication(applicationID, approverID uint64) error {
	var application models.GuildApplication
	if err := g.db.First(&application, applicationID).Error; err != nil {
		return err
	}

	// 检查权限
	canApprove, err := g.CanManageMembers(application.GuildID, approverID)
	if err != nil {
		return err
	}
	if !canApprove {
		return errors.New("没有权限拒绝申请")
	}

	application.Status = "rejected"
	return g.db.Save(&application).Error
}

// HasGuild 检查玩家是否有公会
func (g *GuildComponent) HasGuild(playerID uint64) (bool, error) {
	var count int64
	err := g.db.Model(&models.GuildMember{}).Where("player_id = ?", playerID).Count(&count).Error
	return count > 0, err
}

// CanManageMembers 检查玩家是否可以管理成员
func (g *GuildComponent) CanManageMembers(guildID, playerID uint64) (bool, error) {
	var member models.GuildMember
	err := g.db.Where("guild_id = ? AND player_id = ?", guildID, playerID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	return member.Role == RoleMaster || member.Role == RoleOfficer, nil
}

// GetMemberCount 获取公会成员数量
func (g *GuildComponent) GetMemberCount(guildID uint64) (int, error) {
	var count int64
	err := g.db.Model(&models.GuildMember{}).Where("guild_id = ?", guildID).Count(&count).Error
	return int(count), err
}

// hasPermission 检查权限
func (g *GuildComponent) hasPermission(guildID, playerID uint64, permission GuildPermission) bool {
	var member models.GuildMember
	err := g.db.Where("guild_id = ? AND player_id = ?", guildID, playerID).First(&member).Error
	if err != nil {
		return false
	}

	role := GuildRole(member.Role)
	permissions, ok := rolePermissions[role]
	if !ok {
		return false
	}

	for _, p := range permissions {
		if p == permission {
			return true
		}
	}

	return false
}

// invalidateCache 清除缓存
func (g *GuildComponent) invalidateCache(guildID uint64) {
	g.localCache.Remove(guildID)
}

// ========== NATS通知函数 ==========

func (g *GuildComponent) publishGuildCreatedNotification(guild *models.Guild) {
	_ = "guild.created"
	_ = map[string]any{
		"guild_id":   guild.ID,
		"name":       guild.Name,
		"leader_id":  guild.MasterID,
		"timestamp":  time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishGuildDissolvedNotification(playerID uint64, guildID uint64) {
	_ = fmt.Sprintf("guild.dissolved.%d", playerID)
	_ = map[string]any{
		"guild_id":  guildID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishMemberJoinedNotification(guildID, playerID uint64) {
	_ = fmt.Sprintf("guild.member_joined.%d", guildID)
	_ = map[string]any{
		"player_id": playerID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishMemberKickedNotification(guildID, playerID uint64) {
	_ = fmt.Sprintf("guild.member_kicked.%d", playerID)
	_ = map[string]any{
		"guild_id":  guildID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishMemberLeftNotification(guildID, playerID uint64) {
	_ = fmt.Sprintf("guild.member_left.%d", guildID)
	_ = map[string]any{
		"player_id": playerID,
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishMemberRoleChangedNotification(guildID, playerID uint64, newRole string) {
	_ = fmt.Sprintf("guild.member_role_changed.%d", playerID)
	_ = map[string]any{
		"guild_id":  guildID,
		"new_role":  newRole,
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

func (g *GuildComponent) publishLeadershipTransferredNotification(guildID, oldLeaderID, newLeaderID uint64) {
	_ = fmt.Sprintf("guild.leadership_transferred.%d", guildID)
	_ = map[string]any{
		"old_leader_id": oldLeaderID,
		"new_leader_id": newLeaderID,
		"timestamp":     time.Now().Format(time.RFC3339),
	}
}
