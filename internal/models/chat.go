package models

import (
	"time"
)

// ChatMessageType represents the type of chat message
type ChatMessageType string

const (
	ChatTypeText   ChatMessageType = "text"
	ChatTypeEmoji  ChatMessageType = "emoji"
	ChatTypeSystem ChatMessageType = "system"
)

// ChatScope represents the scope of a chat message
type ChatScope string

const (
	ChatScopeRoom   ChatScope = "room"
	ChatScopeGuild  ChatScope = "guild"
	ChatScopeWorld  ChatScope = "world"
	ChatScopePrivate ChatScope = "private"
)

// ChatMessage represents a chat message
type ChatMessage struct {
	ID         uint64          `json:"id" gorm:"primaryKey;autoIncrement"`
	FromPlayer uint64          `json:"from_player" gorm:"index;not null"`
	ToPlayer   uint64          `json:"to_player" gorm:"index"`
	RoomID     uint64          `json:"room_id" gorm:"index"`
	GuildID    uint64          `json:"guild_id" gorm:"index"`
	Scope      ChatScope       `json:"scope" gorm:"type:varchar(20);not null"`
	Type       ChatMessageType `json:"type" gorm:"type:varchar(20);not null;default:'text'"`
	Content    string          `json:"content" gorm:"type:text;not null"`
	CreatedAt  time.Time       `json:"created_at" gorm:"autoCreateTime;index"`
}

// TableName specifies the table name for ChatMessage model
func (ChatMessage) TableName() string {
	return "chat_messages"
}

// ChatHistory represents a chat history record for quick retrieval
type ChatHistory struct {
	ID           uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	PlayerID     uint64    `json:"player_id" gorm:"index;not null"`
	TargetID     uint64    `json:"target_id" gorm:"index;not null"`
	TargetType   string    `json:"target_type" gorm:"type:varchar(20);not null"`
	LastMessage  string    `json:"last_message" gorm:"type:text"`
	UnreadCount  int       `json:"unread_count" gorm:"default:0"`
	LastReadAt   time.Time `json:"last_read_at"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName specifies the table name for ChatHistory model
func (ChatHistory) TableName() string {
	return "chat_history"
}
