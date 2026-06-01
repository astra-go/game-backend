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
	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/game-backend/pkg/guild"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockGuildComponent struct {
	mock.Mock
}

func (m *MockGuildComponent) CreateGuild(leaderID, name, description string) (*common.Guild, error) {
	args := m.Called(leaderID, name, description)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*common.Guild), args.Error(1)
}

func (m *MockGuildComponent) DissolveGuild(guildID, operatorID string) error {
	args := m.Called(guildID, operatorID)
	return args.Error(0)
}

func (m *MockGuildComponent) InviteMember(guildID, inviterID, targetID string) error {
	args := m.Called(guildID, inviterID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) KickMember(guildID, operatorID, targetID string) error {
	args := m.Called(guildID, operatorID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) LeaveGuild(guildID, playerID string) error {
	args := m.Called(guildID, playerID)
	return args.Error(0)
}

func (m *MockGuildComponent) PromoteMember(guildID, operatorID, targetID string, newRole guild.GuildRole) error {
	args := m.Called(guildID, operatorID, targetID, newRole)
	return args.Error(0)
}

func (m *MockGuildComponent) DemoteMember(guildID, operatorID, targetID string) error {
	args := m.Called(guildID, operatorID, targetID)
	return args.Error(0)
}

func (m *MockGuildComponent) TransferLeadership(guildID, currentLeaderID, newLeaderID string) error {
	args := m.Called(guildID, currentLeaderID, newLeaderID)
	return args.Error(0)
}

func (m *MockGuildComponent) UpdateGuildInfo(guildID, operatorID, description string) error {
	args := m.Called(guildID, operatorID, description)
	return args.Error(0)
}

func (m *MockGuildComponent) GetGuild(guildID string) (*common.Guild, error) {
	args := m.Called(guildID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*common.Guild), args.Error(1)
}

func (m *MockGuildComponent) GetMembers(guildID string) ([]common.GuildMember, error) {
	args := m.Called(guildID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]common.GuildMember), args.Error(1)
}

func (m *MockGuildComponent) GetPlayerGuild(playerID string) (*common.Guild, error) {
	args := m.Called(playerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*common.Guild), args.Error(1)
}

func (m *MockGuildComponent) ListGuilds(page, pageSize int) ([]common.Guild, int64, error) {
	args := m.Called(page, pageSize)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]common.Guild), args.Get(1).(int64), args.Error(2)
}

func setupTestRouter() (*astra.App, *MockGuildComponent) {
	r := astra.New()
	mockGuild := new(MockGuildComponent)
	api := &GuildAPI{guildComponent: mockGuild}

	authMiddleware := func(c *astra.Ctx) error {
		c.Set("player_id", "test_player_123")
		return c.Next()
	}

	api.RegisterRoutes(r, authMiddleware)

	return r, mockGuild
}

func TestCreateGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuild := &common.Guild{
		ID:          "guild_123",
		Name:        "测试公会",
		Description: "这是一个测试公会",
		LeaderID:    "test_player_123",
		Level:       1,
		MemberCount: 1,
		MemberLimit: 100,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	mockGuild.On("CreateGuild", "test_player_123", "测试公会", "这是一个测试公会").Return(expectedGuild, nil)

	reqBody := CreateGuildRequest{
		Name:        "测试公会",
		Description: "这是一个测试公会",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/create", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response apiResponse
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, 0, response.Code)
	data := response.Data.(map[string]any)
	assert.Equal(t, "公会创建成功", data["message"])
	assert.NotNil(t, data["guild"])

	mockGuild.AssertExpectations(t)
}

func TestCreateGuild_InvalidRequest(t *testing.T) {
	r, _ := setupTestRouter()

	reqBody := CreateGuildRequest{
		Name: "A",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/create", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response apiResponse
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response.Msg, "参数错误")
}

func TestCreateGuild_AlreadyInGuild(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("CreateGuild", "test_player_123", "测试公会", "描述").Return(nil, errors.New("您已加入其他公会"))

	reqBody := CreateGuildRequest{
		Name:        "测试公会",
		Description: "描述",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/create", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "您已加入其他公会", response["error"])

	mockGuild.AssertExpectations(t)
}

func TestDissolveGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("DissolveGuild", "guild_123", "test_player_123").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/guild/guild_123/dissolve", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "公会已解散", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestDissolveGuild_NoPermission(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("DissolveGuild", "guild_123", "test_player_123").Return(errors.New("只有会长可以解散公会"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/guild/guild_123/dissolve", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "只有会长可以解散公会", response["error"])

	mockGuild.AssertExpectations(t)
}

func TestInviteMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("InviteMember", "guild_123", "test_player_123", "target_player_456").Return(nil)

	reqBody := InviteMemberRequest{
		TargetID: "target_player_456",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/guild_123/invite", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "成员邀请成功", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestKickMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("KickMember", "guild_123", "test_player_123", "target_player_456").Return(nil)

	reqBody := KickMemberRequest{
		TargetID: "target_player_456",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/guild/guild_123/kick", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "成员已踢出", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestLeaveGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("LeaveGuild", "guild_123", "test_player_123").Return(nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/guild/guild_123/leave", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "已离开公会", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestPromoteMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("PromoteMember", "guild_123", "test_player_123", "target_player_456", guild.RoleOfficer).Return(nil)

	reqBody := PromoteMemberRequest{
		TargetID: "target_player_456",
		NewRole:  "officer",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/guild_123/promote", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "成员已提升", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestDemoteMember_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("DemoteMember", "guild_123", "test_player_123", "target_player_456").Return(nil)

	reqBody := DemoteMemberRequest{
		TargetID: "target_player_456",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/guild_123/demote", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "成员已降级", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestTransferLeadership_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("TransferLeadership", "guild_123", "test_player_123", "new_leader_456").Return(nil)

	reqBody := TransferLeadershipRequest{
		NewLeaderID: "new_leader_456",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/guild_123/transfer", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "会长已转让", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestUpdateGuildInfo_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("UpdateGuildInfo", "guild_123", "test_player_123", "新的公会描述").Return(nil)

	reqBody := UpdateGuildInfoRequest{
		Description: "新的公会描述",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/guild/guild_123/info", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "公会信息已更新", response["message"])

	mockGuild.AssertExpectations(t)
}

func TestGetGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuild := &common.Guild{
		ID:          "guild_123",
		Name:        "测试公会",
		Description: "这是一个测试公会",
		LeaderID:    "leader_123",
		Level:       5,
		MemberCount: 20,
		MemberLimit: 100,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	mockGuild.On("GetGuild", "guild_123").Return(expectedGuild, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/guild_123", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response common.Guild
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "guild_123", response.ID)
	assert.Equal(t, "测试公会", response.Name)

	mockGuild.AssertExpectations(t)
}

func TestGetGuild_NotFound(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("GetGuild", "nonexistent_guild").Return(nil, errors.New("公会不存在"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/nonexistent_guild", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "公会不存在", response["error"])

	mockGuild.AssertExpectations(t)
}

func TestGetMembers_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedMembers := []common.GuildMember{
		{GuildID: "guild_123", PlayerID: "player_1", Role: "leader", JoinedAt: time.Now(), Contribution: 100},
		{GuildID: "guild_123", PlayerID: "player_2", Role: "officer", JoinedAt: time.Now(), Contribution: 50},
		{GuildID: "guild_123", PlayerID: "player_3", Role: "member", JoinedAt: time.Now(), Contribution: 20},
	}

	mockGuild.On("GetMembers", "guild_123").Return(expectedMembers, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/guild_123/members", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(3), response["count"])
	assert.NotNil(t, response["members"])

	mockGuild.AssertExpectations(t)
}

func TestGetMyGuild_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuild := &common.Guild{
		ID:          "guild_123",
		Name:        "我的公会",
		Description: "这是我的公会",
		LeaderID:    "test_player_123",
		Level:       3,
		MemberCount: 10,
		MemberLimit: 100,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	mockGuild.On("GetPlayerGuild", "test_player_123").Return(expectedGuild, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/my", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response common.Guild
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "guild_123", response.ID)
	assert.Equal(t, "我的公会", response.Name)

	mockGuild.AssertExpectations(t)
}

func TestGetMyGuild_NotInGuild(t *testing.T) {
	r, mockGuild := setupTestRouter()

	mockGuild.On("GetPlayerGuild", "test_player_123").Return(nil, errors.New("未加入任何公会"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/my", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "未加入任何公会", response["error"])

	mockGuild.AssertExpectations(t)
}

func TestListGuilds_Success(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuilds := []common.Guild{
		{ID: "guild_1", Name: "公会1", Level: 5, MemberCount: 50},
		{ID: "guild_2", Name: "公会2", Level: 3, MemberCount: 30},
	}

	mockGuild.On("ListGuilds", 1, 20).Return(expectedGuilds, int64(2), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/list", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(2), response["total"])
	assert.Equal(t, float64(1), response["page"])
	assert.Equal(t, float64(20), response["page_size"])
	assert.NotNil(t, response["guilds"])

	mockGuild.AssertExpectations(t)
}

func TestListGuilds_WithPagination(t *testing.T) {
	r, mockGuild := setupTestRouter()

	expectedGuilds := []common.Guild{
		{ID: "guild_3", Name: "公会3", Level: 2, MemberCount: 15},
	}

	mockGuild.On("ListGuilds", 2, 10).Return(expectedGuilds, int64(21), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/guild/list?page=2&page_size=10", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(21), response["total"])
	assert.Equal(t, float64(2), response["page"])
	assert.Equal(t, float64(10), response["page_size"])

	mockGuild.AssertExpectations(t)
}
