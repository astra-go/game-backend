package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/astra-go/astra"
	"github.com/astra-go/game-backend/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockGuildComponent struct {
	mock.Mock
}

func (m *MockGuildComponent) CreateGuild(leaderID uint64, name, description, icon string) (*models.Guild, error) {
	args := m.Called(leaderID, name, description, icon)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Guild), args.Error(1)
}

func (m *MockGuildComponent) DissolveGuild(guildID, operatorID uint64) error {
	args := m.Called(guildID, operatorID)
	return args.Error(0)
}

func (m *MockGuildComponent) InviteMember(guildID, inviterID, targetID uint64) error {
	args := m.Called(guildID, inviterID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) KickMember(guildID, operatorID, targetID uint64) error {
	args := m.Called(guildID, operatorID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) LeaveGuild(guildID, playerID uint64) error {
	args := m.Called(guildID, playerID)
	return args.Error(0)
}

func (m *MockGuildComponent) PromoteMember(guildID, operatorID, targetID uint64) error {
	args := m.Called(guildID, operatorID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) DemoteMember(guildID, operatorID, targetID uint64) error {
	args := m.Called(guildID, operatorID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) TransferLeadership(guildID, currentLeaderID, newLeaderID uint64) error {
	args := m.Called(guildID, currentLeaderID, newLeaderID)
	return args.Error(0)
}

func (m *MockGuildComponent) UpdateGuildInfo(guildID, operatorID uint64, name, description, icon string) error {
	args := m.Called(guildID, operatorID, name, description, icon)
	return args.Error(0)
}

func (m *MockGuildComponent) GetGuild(guildID uint64) (*models.Guild, error) {
	args := m.Called(guildID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Guild), args.Error(1)
}

func (m *MockGuildComponent) GetMembers(guildID uint64) ([]models.GuildMember, error) {
	args := m.Called(guildID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.GuildMember), args.Error(1)
}

func (m *MockGuildComponent) GetPlayerGuild(playerID uint64) (*models.Guild, error) {
	args := m.Called(playerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Guild), args.Error(1)
}

func (m *MockGuildComponent) ListGuilds(page, pageSize int) ([]models.Guild, int64, error) {
	args := m.Called(page, pageSize)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]models.Guild), args.Get(1).(int64), args.Error(2)
}

func (m *MockGuildComponent) ApplyToGuild(guildID, playerID uint64, message string) error {
	args := m.Called(guildID, playerID, message)
	return args.Error(0)
}

func (m *MockGuildComponent) ApproveApplication(applicationID, approverID uint64) error {
	args := m.Called(applicationID, approverID)
	return args.Error(0)
}

func (m *MockGuildComponent) RejectApplication(applicationID, approverID uint64) error {
	args := m.Called(applicationID, approverID)
	return args.Error(0)
}

func setupTestRouter() (*astra.App, *MockGuildComponent) {
	r := astra.New()
	mockGuild := new(MockGuildComponent)
	api := &GuildAPI{guildComponent: mockGuild}

	authMiddleware := func(c *astra.Ctx) error {
		c.Set("player_id", "123")
		return c.Next()
	}
	api.RegisterRoutes(r, authMiddleware)

	return r, mockGuild
}

func TestCreateGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	now := time.Now()
	expectedGuild := &models.Guild{
		ID:          1,
		Name:        "TestGuild",
		Description: "Test Description",
		MasterID:    123,
		CreatedAt:   now,
	}
	mockGuild.On("CreateGuild", uint64(123), "TestGuild", "Test Description", "").Return(expectedGuild, nil)

	reqBody := CreateGuildRequest{
		Name:        "TestGuild",
		Description: "Test Description",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/create", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	data, ok := response["data"].(map[string]any)
	if !ok {
		t.Fatalf("Response data is not a map: %+v", response)
	}
	assert.Equal(t, "公会创建成功", data["message"])

	mockGuild.AssertExpectations(t)
}

func TestCreateGuild_Unauthorized(t *testing.T) {
	r, mockGuild := setupTestRouter()

	// 无效的 player_id
	r.POST("/api/v1/guild/create", func(c *astra.Ctx) error {
		// 不设置 player_id
		return c.JSON(http.StatusOK, map[string]string{"message": "ok"})
	})

	reqBody := CreateGuildRequest{
		Name:        "TestGuild",
		Description: "Test Description",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/create", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	_ = mockGuild
}

func TestGetGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuild := &models.Guild{
		ID:          1,
		Name:        "TestGuild",
		Description: "Test Description",
		MasterID:    123,
	}
	mockGuild.On("GetGuild", uint64(1)).Return(expectedGuild, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/1", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "TestGuild", data["name"])

	mockGuild.AssertExpectations(t)
}

func TestGetGuild_NotFound(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("GetGuild", uint64(999)).Return(nil, errors.New("公会不存在"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/999", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	mockGuild.AssertExpectations(t)
}

func TestInviteMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("InviteMember", uint64(1), uint64(123), uint64(456)).Return(nil)

	reqBody := InviteMemberRequest{
		TargetID: 456,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/1/invite", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "成员邀请成功", data["message"])

	mockGuild.AssertExpectations(t)
}

func TestKickMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("KickMember", uint64(1), uint64(123), uint64(456)).Return(nil)

	reqBody := KickMemberRequest{
		TargetID: 456,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/guild/1/kick", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "成员已踢出", data["message"])

	mockGuild.AssertExpectations(t)
}

func TestLeaveGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("LeaveGuild", uint64(1), uint64(123)).Return(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/1/leave", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "已离开公会", data["message"])

	mockGuild.AssertExpectations(t)
}

func TestDissolveGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("DissolveGuild", uint64(1), uint64(123)).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/guild/1/dissolve", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "公会已解散", data["message"])

	mockGuild.AssertExpectations(t)
}

func TestGetMembers_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	members := []models.GuildMember{
		{PlayerID: 123, Role: models.GuildRoleMaster, JoinedAt: time.Now()},
		{PlayerID: 456, Role: models.GuildRoleOfficer, JoinedAt: time.Now()},
	}
	mockGuild.On("GetMembers", uint64(1)).Return(members, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/1/members", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, float64(2), data["count"])

	mockGuild.AssertExpectations(t)
}

func TestListGuilds_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	guilds := []models.Guild{
		{ID: 1, Name: "Guild1"},
		{ID: 2, Name: "Guild2"},
	}
	mockGuild.On("ListGuilds", 1, 10).Return(guilds, int64(2), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/list?page=1&page_size=10", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, float64(2), data["total"])

	mockGuild.AssertExpectations(t)
}

func TestGetPlayerGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuild := &models.Guild{
		ID:   1,
		Name: "MyGuild",
	}
	mockGuild.On("GetPlayerGuild", uint64(123)).Return(expectedGuild, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/my", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	data := response["data"].(map[string]any)
	assert.Equal(t, "MyGuild", data["name"])

	mockGuild.AssertExpectations(t)
}

func TestPromoteMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("PromoteMember", uint64(1), uint64(123), uint64(456)).Return(nil)

	reqBody := PromoteMemberRequest{
		TargetID: 456,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/1/promote", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockGuild.AssertExpectations(t)
}

func TestTransferLeadership_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("TransferLeadership", uint64(1), uint64(123), uint64(456)).Return(nil)

	reqBody := TransferLeadershipRequest{
		NewLeaderID: 456,
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/1/transfer", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockGuild.AssertExpectations(t)
}

func TestUpdateGuildInfo_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("UpdateGuildInfo", uint64(1), uint64(123), "NewName", "NewDesc", "").Return(nil)

	reqBody := UpdateGuildInfoRequest{
		Name:        "NewName",
		Description: "NewDesc",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/1/info", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	mockGuild.AssertExpectations(t)
}
