package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/models"
	"github.com/astra-go/game-backend/internal/services"
	"github.com/astra-go/game-backend/pkg/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// MockChatService mocks the ChatService
type MockChatService struct {
	mock.Mock
}

// Add mock methods for ChatService
func (m *MockChatService) SendMessage(ctx context.Context, msg *models.ChatMessage) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func (m *MockChatService) GetPrivateMessages(ctx context.Context, player1, player2 uint64, limit int) ([]models.ChatMessage, error) {
	args := m.Called(ctx, player1, player2, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.ChatMessage), args.Error(1)
}

func (m *MockChatService) GetGuildMessages(ctx context.Context, guildID uint64, limit int) ([]models.ChatMessage, error) {
	args := m.Called(ctx, guildID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.ChatMessage), args.Error(1)
}

func (m *MockChatService) GetRoomMessages(ctx context.Context, roomID uint64, limit int) ([]models.ChatMessage, error) {
	args := m.Called(ctx, roomID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.ChatMessage), args.Error(1)
}

func (m *MockChatService) MarkMessagesAsRead(ctx context.Context, playerID, targetID uint64, targetType string) error {
	args := m.Called(ctx, playerID, targetID, targetType)
	return args.Error(0)
}

func (m *MockChatService) GetUnreadCount(ctx context.Context, playerID uint64) (int, error) {
	args := m.Called(ctx, playerID)
	return args.Int(0), args.Error(1)
}

func (m *MockChatService) MutePlayer(ctx context.Context, playerID uint64, duration time.Duration) error {
	args := m.Called(ctx, playerID, duration)
	return args.Error(0)
}

func (m *MockChatService) UnmutePlayer(ctx context.Context, playerID uint64) error {
	args := m.Called(ctx, playerID)
	return args.Error(0)
}

func (m *MockChatService) IsPlayerMuted(ctx context.Context, playerID uint64) (bool, error) {
	args := m.Called(ctx, playerID)
	return args.Bool(0), args.Error(1)
}

func (m *MockChatService) DB() *gorm.DB {
	args := m.Called()
	return args.Get(0).(*gorm.DB)
}

func TestChatAPI_SendPrivateMessage(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	// Add middleware to set player ID
	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	tests := []struct {
		name           string
		request        SendPrivateMessageRequest
		mockError      error
		expectedStatus int
		expectError    bool
	}{
		{
			name: "成功发送私聊消息",
			request: SendPrivateMessageRequest{
				ToPlayerID: 456,
				Type:       models.ChatTypeText,
				Content:    "Hello, world!",
			},
			mockError:      nil,
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name: "发送者被禁言",
			request: SendPrivateMessageRequest{
				ToPlayerID: 456,
				Type:       models.ChatTypeText,
				Content:    "Hello!",
			},
			mockError:      services.ErrPlayerMuted,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name: "消息内容过长",
			request: SendPrivateMessageRequest{
				ToPlayerID: 456,
				Type:       models.ChatTypeText,
				Content:    string(make([]byte, 501)), // 超过500字符
			},
			mockError:      services.ErrMessageTooLong,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.request)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/private", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			// Mock service call
			mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("*models.ChatMessage")).
				Return(tt.mockError).Once()

			app.ServeHTTP(rec, req)

			if tt.expectError {
				assert.Equal(t, tt.expectedStatus, rec.Code)
			} else {
				assert.Equal(t, http.StatusOK, rec.Code)
			}

			mockService.AssertExpectations(t)
		})
	}
}

func TestChatAPI_GetPrivateMessages(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功获取私聊消息", func(t *testing.T) {
		expectedMessages := []models.ChatMessage{
			{
				ID:         1,
				FromPlayer: 123,
				ToPlayer:   456,
				Scope:      models.ChatScopePrivate,
				Type:       models.ChatTypeText,
				Content:    "Hello!",
			},
			{
				ID:         2,
				FromPlayer: 456,
				ToPlayer:   123,
				Scope:      models.ChatScopePrivate,
				Type:       models.ChatTypeText,
				Content:    "Hi there!",
			},
		}

		mockService.On("GetPrivateMessages", mock.Anything, uint64(123), uint64(456), 50).
			Return(expectedMessages, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/private/456", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NotNil(t, response["data"])

		mockService.AssertExpectations(t)
	})

	t.Run("目标用户ID为空", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/private/", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		// This will return 404 because the route doesn't match
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestChatAPI_SendGuildMessage(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功发送公会消息", func(t *testing.T) {
		request := SendGuildMessageRequest{
			GuildID: 100,
			Type:    models.ChatTypeText,
			Content: "Guild message",
		}

		mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("*models.ChatMessage")).
			Return(nil).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/guild", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		mockService.AssertExpectations(t)
	})

	t.Run("不在公会中", func(t *testing.T) {
		request := SendGuildMessageRequest{
			GuildID: 100,
			Type:    models.ChatTypeText,
			Content: "Guild message",
		}

		mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("*models.ChatMessage")).
			Return(services.ErrNotInGuild).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/guild", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)

		mockService.AssertExpectations(t)
	})
}

func TestChatAPI_SendWorldMessage(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功发送世界消息", func(t *testing.T) {
		request := SendWorldMessageRequest{
			Type:    models.ChatTypeText,
			Content: "World message",
		}

		mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("*models.ChatMessage")).
			Return(nil).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/world", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		mockService.AssertExpectations(t)
	})

	t.Run("发送者被禁言", func(t *testing.T) {
		request := SendWorldMessageRequest{
			Type:    models.ChatTypeText,
			Content: "World message",
		}

		mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("*models.ChatMessage")).
			Return(services.ErrPlayerMuted).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/world", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)

		mockService.AssertExpectations(t)
	})
}

func TestChatAPI_MarkMessagesAsRead(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功标记消息已读", func(t *testing.T) {
		request := MarkMessagesAsReadRequest{
			TargetID:   456,
			TargetType: "private",
		}

		mockService.On("MarkMessagesAsRead", mock.Anything, uint64(123), uint64(456), "private").
			Return(nil).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/mark-read", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		mockService.AssertExpectations(t)
	})
}

func TestChatAPI_GetUnreadCount(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功获取未读消息数量", func(t *testing.T) {
		mockService.On("GetUnreadCount", mock.Anything, uint64(123)).
			Return(5, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/unread-count", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &response)
		data := response["data"].(map[string]interface{})
		assert.Equal(t, float64(5), data["unread_count"])

		mockService.AssertExpectations(t)
	})
}

func TestChatAPI_MutePlayer(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("成功禁言玩家", func(t *testing.T) {
		request := MutePlayerRequest{
			PlayerID: 456,
			Duration: 3600 * time.Second, // 1小时
		}

		mockService.On("MutePlayer", mock.Anything, uint64(456), mock.AnythingOfType("time.Duration")).
			Return(nil).Once()

		body, _ := json.Marshal(request)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/mute", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		mockService.AssertExpectations(t)
	})
}

func TestChatAPI_GetMuteStatus(t *testing.T) {
	mockService := new(MockChatService)
	logger, _ := zap.NewDevelopment()

	api := NewChatAPI(mockService, logger)

	app := astra.New()

	authMiddleware := func(c *astra.Ctx) error {
		c.Set(middleware.ContextKeyPlayerID, "123")
		return c.Next()
	}

	api.RegisterRoutes(app, authMiddleware)

	t.Run("玩家已被禁言", func(t *testing.T) {
		mockService.On("IsPlayerMuted", mock.Anything, uint64(456)).
			Return(true, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/mute/456/status", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &response)
		data := response["data"].(map[string]interface{})
		assert.Equal(t, true, data["is_muted"])

		mockService.AssertExpectations(t)
	})

	t.Run("玩家未被禁言", func(t *testing.T) {
		mockService.On("IsPlayerMuted", mock.Anything, uint64(789)).
			Return(false, nil).Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/mute/789/status", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var response map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &response)
		data := response["data"].(map[string]interface{})
		assert.Equal(t, false, data["is_muted"])

		mockService.AssertExpectations(t)
	})
}
