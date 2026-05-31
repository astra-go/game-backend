package models

import (
	"time"
)

// RoomRole represents the role of a room member
type RoomRole string

const (
	RoomRoleOwner  RoomRole = "owner"
	RoomRoleMember RoomRole = "member"
)

// Room represents a chat room
type Room struct {
	ID          string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	Name        string    `json:"name" gorm:"type:varchar(50);not null"`
	Description string    `json:"description" gorm:"type:text"`
	OwnerID     string    `json:"owner_id" gorm:"type:varchar(64);index;not null"`
	MaxMembers  int       `json:"max_members" gorm:"default:100"`
	IsPublic    bool      `json:"is_public" gorm:"default:true"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for Room model
func (Room) TableName() string {
	return "rooms"
}

// RoomMember represents a member of a room
type RoomMember struct {
	ID       string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RoomID   string    `json:"room_id" gorm:"type:varchar(64);index:idx_room_player;not null"`
	PlayerID string    `json:"player_id" gorm:"type:varchar(64);index:idx_room_player;not null"`
	Role     RoomRole  `json:"role" gorm:"type:varchar(20);not null;default:'member'"`
	JoinedAt time.Time `json:"joined_at" gorm:"autoCreateTime"`
}

// TableName specifies the table name for RoomMember model
func (RoomMember) TableName() string {
	return "room_members"
}
