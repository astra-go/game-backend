package services

import (
	"context"
	"time"

	"github.com/astra-go/game-backend/internal/models"
	"gorm.io/gorm"
)

// ChatServiceInterface defines the interface for chat service operations
type ChatServiceInterface interface {
	SendMessage(ctx context.Context, msg *models.ChatMessage) error
	GetPrivateMessages(ctx context.Context, player1, player2 uint64, limit int) ([]models.ChatMessage, error)
	GetGuildMessages(ctx context.Context, guildID uint64, limit int) ([]models.ChatMessage, error)
	GetRoomMessages(ctx context.Context, roomID uint64, limit int) ([]models.ChatMessage, error)
	MarkMessagesAsRead(ctx context.Context, playerID, targetID uint64, targetType string) error
	GetUnreadCount(ctx context.Context, playerID uint64) (int, error)
	MutePlayer(ctx context.Context, playerID uint64, duration time.Duration) error
	UnmutePlayer(ctx context.Context, playerID uint64) error
	IsPlayerMuted(ctx context.Context, playerID uint64) (bool, error)
	DB() *gorm.DB
}
