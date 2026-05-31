package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"

	"github.com/astra-go/game-backend/internal/models"
)

var (
	ErrGuildNameTaken      = errors.New("guild name already taken")
	ErrGuildNotFound       = errors.New("guild not found")
	ErrAlreadyInGuild      = errors.New("already in a guild")
	ErrNotGuildMember      = errors.New("not a guild member")
	ErrInsufficientPermission = errors.New("insufficient permission")
	ErrGuildFull           = errors.New("guild is full")
	ErrApplicationExists   = errors.New("application already exists")
)

// GuildService handles guild-related operations
type GuildService struct {
	db    *gorm.DB
	redis *redis.Client
}

// NewGuildService creates a new guild service
func NewGuildService(db *gorm.DB, redis *redis.Client) *GuildService {
	return &GuildService{
		db:    db,
		redis: redis,
	}
}

// CreateGuild creates a new guild
func (s *GuildService) CreateGuild(ctx context.Context, masterID uint64, name, description, icon string) (*models.Guild, error) {
	hasGuild, err := s.HasGuild(ctx, masterID)
	if err != nil {
		return nil, err
	}
	if hasGuild {
		return nil, ErrAlreadyInGuild
	}

	var existingGuild models.Guild
	err = s.db.Where("name = ?", name).First(&existingGuild).Error
	if err == nil {
		return nil, ErrGuildNameTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	guild := &models.Guild{
		Name:        name,
		Description: description,
		MasterID:    masterID,
		Level:       1,
		MaxMembers:  50,
		Icon:        icon,
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(guild).Error; err != nil {
			return err
		}

		member := &models.GuildMember{
			GuildID:      guild.ID,
			PlayerID:     masterID,
			Role:         models.GuildRoleMaster,
			Contribution: 0,
		}
		return tx.Create(member).Error
	})

	if err != nil {
		return nil, err
	}

	return guild, nil
}

// ApplyToGuild submits a guild join application
func (s *GuildService) ApplyToGuild(ctx context.Context, guildID, playerID uint64, message string) error {
	hasGuild, err := s.HasGuild(ctx, playerID)
	if err != nil {
		return err
	}
	if hasGuild {
		return ErrAlreadyInGuild
	}

	var guild models.Guild
	if err := s.db.First(&guild, guildID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGuildNotFound
		}
		return err
	}

	var existingApp models.GuildApplication
	err = s.db.Where("guild_id = ? AND player_id = ? AND status = ?", guildID, playerID, "pending").
		First(&existingApp).Error
	if err == nil {
		return ErrApplicationExists
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	application := &models.GuildApplication{
		GuildID:  guildID,
		PlayerID: playerID,
		Message:  message,
		Status:   "pending",
	}

	return s.db.Create(application).Error
}

// ApproveApplication approves a guild join application
func (s *GuildService) ApproveApplication(ctx context.Context, applicationID, approverID uint64) error {
	var application models.GuildApplication
	if err := s.db.First(&application, applicationID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("application not found")
		}
		return err
	}

	if application.Status != "pending" {
		return errors.New("application already processed")
	}

	canApprove, err := s.CanManageMembers(ctx, application.GuildID, approverID)
	if err != nil {
		return err
	}
	if !canApprove {
		return ErrInsufficientPermission
	}

	var guild models.Guild
	if err := s.db.First(&guild, application.GuildID).Error; err != nil {
		return err
	}

	memberCount, err := s.GetMemberCount(ctx, guild.ID)
	if err != nil {
		return err
	}
	if memberCount >= guild.MaxMembers {
		return ErrGuildFull
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		application.Status = "approved"
		if err := tx.Save(&application).Error; err != nil {
			return err
		}

		member := &models.GuildMember{
			GuildID:      application.GuildID,
			PlayerID:     application.PlayerID,
			Role:         models.GuildRoleMember,
			Contribution: 0,
		}
		return tx.Create(member).Error
	})
}

// RejectApplication rejects a guild join application
func (s *GuildService) RejectApplication(ctx context.Context, applicationID, approverID uint64) error {
	var application models.GuildApplication
	if err := s.db.First(&application, applicationID).Error; err != nil {
		return err
	}

	canApprove, err := s.CanManageMembers(ctx, application.GuildID, approverID)
	if err != nil {
		return err
	}
	if !canApprove {
		return ErrInsufficientPermission
	}

	application.Status = "rejected"
	return s.db.Save(&application).Error
}

// LeaveGuild allows a member to leave the guild
func (s *GuildService) LeaveGuild(ctx context.Context, guildID, playerID uint64) error {
	var member models.GuildMember
	err := s.db.Where("guild_id = ? AND player_id = ?", guildID, playerID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotGuildMember
		}
		return err
	}

	if member.Role == models.GuildRoleMaster {
		return errors.New("guild master cannot leave, transfer ownership first")
	}

	return s.db.Delete(&member).Error
}

// KickMember removes a member from the guild
func (s *GuildService) KickMember(ctx context.Context, guildID, kickerID, targetID uint64) error {
	canKick, err := s.CanManageMembers(ctx, guildID, kickerID)
	if err != nil {
		return err
	}
	if !canKick {
		return ErrInsufficientPermission
	}

	var targetMember models.GuildMember
	err = s.db.Where("guild_id = ? AND player_id = ?", guildID, targetID).First(&targetMember).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotGuildMember
		}
		return err
	}

	if targetMember.Role == models.GuildRoleMaster {
		return errors.New("cannot kick guild master")
	}

	return s.db.Delete(&targetMember).Error
}

// PromoteMember promotes a member to officer
func (s *GuildService) PromoteMember(ctx context.Context, guildID, promoterID, targetID uint64) error {
	var promoter models.GuildMember
	err := s.db.Where("guild_id = ? AND player_id = ?", guildID, promoterID).First(&promoter).Error
	if err != nil {
		return err
	}

	if promoter.Role != models.GuildRoleMaster {
		return ErrInsufficientPermission
	}

	var target models.GuildMember
	err = s.db.Where("guild_id = ? AND player_id = ?", guildID, targetID).First(&target).Error
	if err != nil {
		return err
	}

	if target.Role != models.GuildRoleMember {
		return errors.New("can only promote regular members")
	}

	target.Role = models.GuildRoleOfficer
	return s.db.Save(&target).Error
}

// DemoteMember demotes an officer to member
func (s *GuildService) DemoteMember(ctx context.Context, guildID, demoterID, targetID uint64) error {
	var demoter models.GuildMember
	err := s.db.Where("guild_id = ? AND player_id = ?", guildID, demoterID).First(&demoter).Error
	if err != nil {
		return err
	}

	if demoter.Role != models.GuildRoleMaster {
		return ErrInsufficientPermission
	}

	var target models.GuildMember
	err = s.db.Where("guild_id = ? AND player_id = ?", guildID, targetID).First(&target).Error
	if err != nil {
		return err
	}

	if target.Role != models.GuildRoleOfficer {
		return errors.New("can only demote officers")
	}

	target.Role = models.GuildRoleMember
	return s.db.Save(&target).Error
}

// GetGuildInfo retrieves guild information
func (s *GuildService) GetGuildInfo(ctx context.Context, guildID uint64) (*models.Guild, error) {
	cacheKey := fmt.Sprintf("guild:info:%d", guildID)

	var guild models.Guild
	err := s.db.First(&guild, guildID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGuildNotFound
		}
		return nil, err
	}

	s.redis.Set(ctx, cacheKey, guild, 10*time.Minute)

	return &guild, nil
}

// GetGuildMembers retrieves all members of a guild
func (s *GuildService) GetGuildMembers(ctx context.Context, guildID uint64) ([]models.GuildMember, error) {
	var members []models.GuildMember
	err := s.db.Where("guild_id = ?", guildID).Order("role ASC, joined_at ASC").Find(&members).Error
	return members, err
}

// GetPlayerGuild retrieves the guild a player belongs to
func (s *GuildService) GetPlayerGuild(ctx context.Context, playerID uint64) (*models.Guild, error) {
	var member models.GuildMember
	err := s.db.Where("player_id = ?", playerID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetGuildInfo(ctx, member.GuildID)
}

// HasGuild checks if a player is in a guild
func (s *GuildService) HasGuild(ctx context.Context, playerID uint64) (bool, error) {
	var count int64
	err := s.db.Model(&models.GuildMember{}).Where("player_id = ?", playerID).Count(&count).Error
	return count > 0, err
}

// CanManageMembers checks if a player can manage guild members
func (s *GuildService) CanManageMembers(ctx context.Context, guildID, playerID uint64) (bool, error) {
	var member models.GuildMember
	err := s.db.Where("guild_id = ? AND player_id = ?", guildID, playerID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}

	return member.Role == models.GuildRoleMaster || member.Role == models.GuildRoleOfficer, nil
}

// GetMemberCount returns the number of members in a guild
func (s *GuildService) GetMemberCount(ctx context.Context, guildID uint64) (int, error) {
	var count int64
	err := s.db.Model(&models.GuildMember{}).Where("guild_id = ?", guildID).Count(&count).Error
	return int(count), err
}
