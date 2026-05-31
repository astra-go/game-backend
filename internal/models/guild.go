package models

import (
	"time"
)

// GuildRole represents the role of a guild member
type GuildRole string

const (
	GuildRoleMaster  GuildRole = "master"
	GuildRoleOfficer GuildRole = "officer"
	GuildRoleMember  GuildRole = "member"
)

// Guild represents a player guild
type Guild struct {
	ID          uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string    `json:"name" gorm:"type:varchar(50);uniqueIndex;not null"`
	Description string    `json:"description" gorm:"type:text"`
	MasterID    uint64    `json:"master_id" gorm:"index;not null"`
	Level       int       `json:"level" gorm:"default:1"`
	MaxMembers  int       `json:"max_members" gorm:"default:50"`
	Icon        string    `json:"icon" gorm:"type:varchar(255)"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for Guild model
func (Guild) TableName() string {
	return "guilds"
}

// GuildMember represents a member of a guild
type GuildMember struct {
	ID           uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	GuildID      uint64    `json:"guild_id" gorm:"index:idx_guild_player;not null"`
	PlayerID     uint64    `json:"player_id" gorm:"index:idx_guild_player;not null"`
	Role         GuildRole `json:"role" gorm:"type:varchar(20);not null;default:'member'"`
	Contribution int       `json:"contribution" gorm:"default:0"`
	JoinedAt     time.Time `json:"joined_at" gorm:"autoCreateTime"`
}

// TableName specifies the table name for GuildMember model
func (GuildMember) TableName() string {
	return "guild_members"
}

// GuildApplication represents a guild join application
type GuildApplication struct {
	ID        uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	GuildID   uint64    `json:"guild_id" gorm:"index:idx_guild_player;not null"`
	PlayerID  uint64    `json:"player_id" gorm:"index:idx_guild_player;not null"`
	Message   string    `json:"message" gorm:"type:varchar(255)"`
	Status    string    `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for GuildApplication model
func (GuildApplication) TableName() string {
	return "guild_applications"
}
