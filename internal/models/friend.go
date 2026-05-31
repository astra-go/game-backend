package models

import (
	"time"
)

// FriendStatus represents the status of a friend relationship
type FriendStatus string

const (
	FriendStatusPending  FriendStatus = "pending"
	FriendStatusAccepted FriendStatus = "accepted"
	FriendStatusBlocked  FriendStatus = "blocked"
)

// Friend represents a friend relationship between two players
type Friend struct {
	ID        uint64       `json:"id" gorm:"primaryKey;autoIncrement"`
	PlayerID  uint64       `json:"player_id" gorm:"index:idx_player_friend;not null"`
	FriendID  uint64       `json:"friend_id" gorm:"index:idx_player_friend;not null"`
	Status    FriendStatus `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	CreatedAt time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for Friend model
func (Friend) TableName() string {
	return "friends"
}

// FriendRequest represents a friend request
type FriendRequest struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	FromPlayer uint64    `json:"from_player" gorm:"index:idx_from_to;not null"`
	ToPlayer   uint64    `json:"to_player" gorm:"index:idx_from_to;not null"`
	Message    string    `json:"message" gorm:"type:varchar(255)"`
	Status     string    `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for FriendRequest model
func (FriendRequest) TableName() string {
	return "friend_requests"
}

// Blacklist represents a blocked player relationship
type Blacklist struct {
	ID         uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	PlayerID   uint64    `json:"player_id" gorm:"index:idx_player_blocked;not null"`
	BlockedID  uint64    `json:"blocked_id" gorm:"index:idx_player_blocked;not null"`
	Reason     string    `json:"reason" gorm:"type:varchar(255)"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

// TableName specifies the table name for Blacklist model
func (Blacklist) TableName() string {
	return "blacklist"
}
