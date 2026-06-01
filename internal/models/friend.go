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
}

func (Friend) TableName() string { return "friends" }

// FriendRequest represents a friend request
type FriendRequest struct {
	ID         uint64       `json:"id" gorm:"primaryKey;autoIncrement"`
	SenderID   uint64       `json:"sender_id" gorm:"index:idx_sender_receiver;not null"`
	ReceiverID uint64       `json:"receiver_id" gorm:"index:idx_sender_receiver;not null"`
	Status     FriendStatus `json:"status" gorm:"type:varchar(20);not null;default:'pending'"`
	CreatedAt  time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
}

func (FriendRequest) TableName() string { return "friend_requests" }

// Blacklist represents a blocked user
type Blacklist struct {
	ID        uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	PlayerID  uint64    `json:"player_id" gorm:"index;not null"`
	BlockedID uint64    `json:"blocked_id" gorm:"index;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (Blacklist) TableName() string { return "blacklists" }
