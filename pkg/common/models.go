package common

import "time"

// Guild 公会数据模型
type Guild struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	Description string    `json:"description"`
	LeaderID    string    `gorm:"not null" json:"leader_id"`
	Level       int       `gorm:"default:1" json:"level"`
	MemberCount int       `gorm:"default:0" json:"member_count"`
	MemberLimit int       `gorm:"default:100" json:"member_limit"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GuildMember 公会成员数据模型
type GuildMember struct {
	GuildID      string    `gorm:"primaryKey" json:"guild_id"`
	PlayerID     string    `gorm:"primaryKey" json:"player_id"`
	Role         string    `gorm:"not null" json:"role"`
	JoinedAt     time.Time `json:"joined_at"`
	Contribution int       `gorm:"default:0" json:"contribution"`
}
