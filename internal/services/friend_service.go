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
	ErrFriendRequestExists = errors.New("friend request already exists")
	ErrAlreadyFriends      = errors.New("already friends")
	ErrFriendNotFound      = errors.New("friend not found")
	ErrRequestNotFound     = errors.New("friend request not found")
	ErrCannotAddSelf       = errors.New("cannot add yourself as friend")
	ErrPlayerBlocked       = errors.New("player is blocked")
)

// FriendService handles friend-related operations
type FriendService struct {
	db    *gorm.DB
	redis *redis.Client
}

// NewFriendService creates a new friend service
func NewFriendService(db *gorm.DB, redis *redis.Client) *FriendService {
	return &FriendService{
		db:    db,
		redis: redis,
	}
}

// SendFriendRequest sends a friend request
func (s *FriendService) SendFriendRequest(ctx context.Context, fromPlayer, toPlayer uint64, message string) error {
	if fromPlayer == toPlayer {
		return ErrCannotAddSelf
	}

	blocked, err := s.IsBlocked(ctx, toPlayer, fromPlayer)
	if err != nil {
		return err
	}
	if blocked {
		return ErrPlayerBlocked
	}

	var existingRequest models.FriendRequest
	err = s.db.Where("from_player = ? AND to_player = ? AND status = ?", fromPlayer, toPlayer, "pending").
		First(&existingRequest).Error
	if err == nil {
		return ErrFriendRequestExists
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	isFriend, err := s.IsFriend(ctx, fromPlayer, toPlayer)
	if err != nil {
		return err
	}
	if isFriend {
		return ErrAlreadyFriends
	}

	request := &models.FriendRequest{
		FromPlayer: fromPlayer,
		ToPlayer:   toPlayer,
		Message:    message,
		Status:     "pending",
	}

	return s.db.Create(request).Error
}

// AcceptFriendRequest accepts a friend request
func (s *FriendService) AcceptFriendRequest(ctx context.Context, requestID, playerID uint64) error {
	var request models.FriendRequest
	err := s.db.First(&request, requestID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		return err
	}

	if request.ToPlayer != playerID {
		return errors.New("unauthorized to accept this request")
	}

	if request.Status != "pending" {
		return errors.New("request already processed")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		request.Status = "accepted"
		if err := tx.Save(&request).Error; err != nil {
			return err
		}

		friend1 := &models.Friend{
			PlayerID: request.FromPlayer,
			FriendID: request.ToPlayer,
			Status:   models.FriendStatusAccepted,
		}
		if err := tx.Create(friend1).Error; err != nil {
			return err
		}

		friend2 := &models.Friend{
			PlayerID: request.ToPlayer,
			FriendID: request.FromPlayer,
			Status:   models.FriendStatusAccepted,
		}
		if err := tx.Create(friend2).Error; err != nil {
			return err
		}

		s.invalidateFriendCache(ctx, request.FromPlayer)
		s.invalidateFriendCache(ctx, request.ToPlayer)

		return nil
	})
}

// RejectFriendRequest rejects a friend request
func (s *FriendService) RejectFriendRequest(ctx context.Context, requestID, playerID uint64) error {
	var request models.FriendRequest
	err := s.db.First(&request, requestID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		return err
	}

	if request.ToPlayer != playerID {
		return errors.New("unauthorized to reject this request")
	}

	request.Status = "rejected"
	return s.db.Save(&request).Error
}

// RemoveFriend removes a friend relationship
func (s *FriendService) RemoveFriend(ctx context.Context, playerID, friendID uint64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("player_id = ? AND friend_id = ?", playerID, friendID).Delete(&models.Friend{}).Error; err != nil {
			return err
		}
		if err := tx.Where("player_id = ? AND friend_id = ?", friendID, playerID).Delete(&models.Friend{}).Error; err != nil {
			return err
		}

		s.invalidateFriendCache(ctx, playerID)
		s.invalidateFriendCache(ctx, friendID)

		return nil
	})
}

// GetFriendList retrieves the friend list for a player
func (s *FriendService) GetFriendList(ctx context.Context, playerID uint64) ([]models.Friend, error) {
	cacheKey := fmt.Sprintf("friend:list:%d", playerID)

	var friends []models.Friend
	err := s.db.Where("player_id = ? AND status = ?", playerID, models.FriendStatusAccepted).
		Find(&friends).Error
	if err != nil {
		return nil, err
	}

	s.redis.Set(ctx, cacheKey, friends, 5*time.Minute)

	return friends, nil
}

// GetPendingRequests retrieves pending friend requests for a player
func (s *FriendService) GetPendingRequests(ctx context.Context, playerID uint64) ([]models.FriendRequest, error) {
	var requests []models.FriendRequest
	err := s.db.Where("to_player = ? AND status = ?", playerID, "pending").
		Order("created_at DESC").
		Find(&requests).Error
	return requests, err
}

// BlockPlayer adds a player to the blacklist
func (s *FriendService) BlockPlayer(ctx context.Context, playerID, blockedID uint64, reason string) error {
	if playerID == blockedID {
		return ErrCannotAddSelf
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		blacklist := &models.Blacklist{
			PlayerID:  playerID,
			BlockedID: blockedID,
			Reason:    reason,
		}
		if err := tx.Create(blacklist).Error; err != nil {
			return err
		}

		tx.Where("(player_id = ? AND friend_id = ?) OR (player_id = ? AND friend_id = ?)",
			playerID, blockedID, blockedID, playerID).Delete(&models.Friend{})

		s.invalidateFriendCache(ctx, playerID)
		s.invalidateFriendCache(ctx, blockedID)

		return nil
	})
}

// UnblockPlayer removes a player from the blacklist
func (s *FriendService) UnblockPlayer(ctx context.Context, playerID, blockedID uint64) error {
	return s.db.Where("player_id = ? AND blocked_id = ?", playerID, blockedID).
		Delete(&models.Blacklist{}).Error
}

// GetBlacklist retrieves the blacklist for a player
func (s *FriendService) GetBlacklist(ctx context.Context, playerID uint64) ([]models.Blacklist, error) {
	var blacklist []models.Blacklist
	err := s.db.Where("player_id = ?", playerID).Find(&blacklist).Error
	return blacklist, err
}

// IsFriend checks if two players are friends
func (s *FriendService) IsFriend(ctx context.Context, playerID, friendID uint64) (bool, error) {
	var count int64
	err := s.db.Model(&models.Friend{}).
		Where("player_id = ? AND friend_id = ? AND status = ?", playerID, friendID, models.FriendStatusAccepted).
		Count(&count).Error
	return count > 0, err
}

// IsBlocked checks if a player is blocked
func (s *FriendService) IsBlocked(ctx context.Context, playerID, blockedID uint64) (bool, error) {
	var count int64
	err := s.db.Model(&models.Blacklist{}).
		Where("player_id = ? AND blocked_id = ?", playerID, blockedID).
		Count(&count).Error
	return count > 0, err
}

func (s *FriendService) invalidateFriendCache(ctx context.Context, playerID uint64) {
	cacheKey := fmt.Sprintf("friend:list:%d", playerID)
	s.redis.Del(ctx, cacheKey)
}
