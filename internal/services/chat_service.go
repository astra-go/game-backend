package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"

	"github.com/astra-go/game-backend/internal/models"
)

var (
	ErrInvalidChatScope   = errors.New("invalid chat scope")
	ErrMessageTooLong     = errors.New("message too long")
	ErrPlayerMuted        = errors.New("player is muted")
	ErrNotInRoom          = errors.New("not in the specified room")
	ErrNotInGuild         = errors.New("not in the specified guild")
)

const (
	MaxMessageLength = 500
)

// ChatService handles chat-related operations
type ChatService struct {
	db    *gorm.DB
	redis *redis.Client
	nats  *nats.Conn
}

// NewChatService creates a new chat service
func NewChatService(db *gorm.DB, redis *redis.Client, nats *nats.Conn) *ChatService {
	return &ChatService{
		db:    db,
		redis: redis,
		nats:  nats,
	}
}

// DB returns the database connection
func (s *ChatService) DB() *gorm.DB {
	return s.db
}

// ChatMessageDTO represents a chat message for transmission
type ChatMessageDTO struct {
	ID         uint64                `json:"id"`
	FromPlayer uint64                `json:"from_player"`
	ToPlayer   uint64                `json:"to_player,omitempty"`
	RoomID     uint64                `json:"room_id,omitempty"`
	GuildID    uint64                `json:"guild_id,omitempty"`
	Scope      models.ChatScope      `json:"scope"`
	Type       models.ChatMessageType `json:"type"`
	Content    string                `json:"content"`
	Timestamp  time.Time             `json:"timestamp"`
}

// SendMessage sends a chat message
func (s *ChatService) SendMessage(ctx context.Context, msg *models.ChatMessage) error {
	if len(msg.Content) > MaxMessageLength {
		return ErrMessageTooLong
	}

	isMuted, err := s.IsPlayerMuted(ctx, msg.FromPlayer)
	if err != nil {
		return err
	}
	if isMuted {
		return ErrPlayerMuted
	}

	switch msg.Scope {
	case models.ChatScopeRoom:
		if msg.RoomID == 0 {
			return errors.New("room_id required for room chat")
		}
		inRoom, err := s.isPlayerInRoom(ctx, msg.FromPlayer, msg.RoomID)
		if err != nil {
			return err
		}
		if !inRoom {
			return ErrNotInRoom
		}
	case models.ChatScopeGuild:
		if msg.GuildID == 0 {
			return errors.New("guild_id required for guild chat")
		}
		inGuild, err := s.isPlayerInGuild(ctx, msg.FromPlayer, msg.GuildID)
		if err != nil {
			return err
		}
		if !inGuild {
			return ErrNotInGuild
		}
	case models.ChatScopePrivate:
		if msg.ToPlayer == 0 {
			return errors.New("to_player required for private chat")
		}
	case models.ChatScopeWorld:
	default:
		return ErrInvalidChatScope
	}

	if err := s.db.Create(msg).Error; err != nil {
		return err
	}

	return s.broadcastMessage(ctx, msg)
}

// broadcastMessage broadcasts a message via NATS
func (s *ChatService) broadcastMessage(ctx context.Context, msg *models.ChatMessage) error {
	dto := &ChatMessageDTO{
		ID:         msg.ID,
		FromPlayer: msg.FromPlayer,
		ToPlayer:   msg.ToPlayer,
		RoomID:     msg.RoomID,
		GuildID:    msg.GuildID,
		Scope:      msg.Scope,
		Type:       msg.Type,
		Content:    msg.Content,
		Timestamp:  msg.CreatedAt,
	}

	data, err := json.Marshal(dto)
	if err != nil {
		return err
	}

	subject := s.getMessageSubject(msg)
	return s.nats.Publish(subject, data)
}

// getMessageSubject returns the NATS subject for a message
func (s *ChatService) getMessageSubject(msg *models.ChatMessage) string {
	switch msg.Scope {
	case models.ChatScopeRoom:
		return fmt.Sprintf("chat.room.%d", msg.RoomID)
	case models.ChatScopeGuild:
		return fmt.Sprintf("chat.guild.%d", msg.GuildID)
	case models.ChatScopePrivate:
		return fmt.Sprintf("chat.private.%d", msg.ToPlayer)
	case models.ChatScopeWorld:
		return "chat.world"
	default:
		return "chat.unknown"
	}
}

// GetRoomMessages retrieves recent messages for a room
func (s *ChatService) GetRoomMessages(ctx context.Context, roomID uint64, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var messages []models.ChatMessage
	err := s.db.Where("room_id = ? AND scope = ?", roomID, models.ChatScopeRoom).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// GetGuildMessages retrieves recent messages for a guild
func (s *ChatService) GetGuildMessages(ctx context.Context, guildID uint64, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var messages []models.ChatMessage
	err := s.db.Where("guild_id = ? AND scope = ?", guildID, models.ChatScopeGuild).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// GetPrivateMessages retrieves private messages between two players
func (s *ChatService) GetPrivateMessages(ctx context.Context, player1, player2 uint64, limit int) ([]models.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var messages []models.ChatMessage
	err := s.db.Where(
		"scope = ? AND ((from_player = ? AND to_player = ?) OR (from_player = ? AND to_player = ?))",
		models.ChatScopePrivate, player1, player2, player2, player1,
	).Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// MarkMessagesAsRead marks messages as read
func (s *ChatService) MarkMessagesAsRead(ctx context.Context, playerID, targetID uint64, targetType string) error {
	var history models.ChatHistory
	err := s.db.Where("player_id = ? AND target_id = ? AND target_type = ?", playerID, targetID, targetType).
		First(&history).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		history = models.ChatHistory{
			PlayerID:    playerID,
			TargetID:    targetID,
			TargetType:  targetType,
			UnreadCount: 0,
			LastReadAt:  time.Now(),
		}
		return s.db.Create(&history).Error
	}

	if err != nil {
		return err
	}

	history.UnreadCount = 0
	history.LastReadAt = time.Now()
	return s.db.Save(&history).Error
}

// GetUnreadCount retrieves unread message count
func (s *ChatService) GetUnreadCount(ctx context.Context, playerID uint64) (int, error) {
	var total int64
	err := s.db.Model(&models.ChatHistory{}).
		Where("player_id = ?", playerID).
		Select("SUM(unread_count)").
		Scan(&total).Error
	return int(total), err
}

// MutePlayer mutes a player for a duration
func (s *ChatService) MutePlayer(ctx context.Context, playerID uint64, duration time.Duration) error {
	muteKey := fmt.Sprintf("chat:mute:%d", playerID)
	return s.redis.Set(ctx, muteKey, "1", duration).Err()
}

// UnmutePlayer unmutes a player
func (s *ChatService) UnmutePlayer(ctx context.Context, playerID uint64) error {
	muteKey := fmt.Sprintf("chat:mute:%d", playerID)
	return s.redis.Del(ctx, muteKey).Err()
}

// IsPlayerMuted checks if a player is muted
func (s *ChatService) IsPlayerMuted(ctx context.Context, playerID uint64) (bool, error) {
	muteKey := fmt.Sprintf("chat:mute:%d", playerID)
	val, err := s.redis.Get(ctx, muteKey).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val == "1", nil
}

func (s *ChatService) isPlayerInRoom(ctx context.Context, playerID, roomID uint64) (bool, error) {
	var count int64
	err := s.db.Model(&models.RoomMember{}).
		Where("room_id = ? AND player_id = ?", roomID, playerID).
		Count(&count).Error
	return count > 0, err
}

func (s *ChatService) isPlayerInGuild(ctx context.Context, playerID, guildID uint64) (bool, error) {
	var count int64
	err := s.db.Model(&models.GuildMember{}).
		Where("guild_id = ? AND player_id = ?", guildID, playerID).
		Count(&count).Error
	return count > 0, err
}
