package common

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPlayer_Struct(t *testing.T) {
	now := time.Now()
	p := Player{
		ID:          "player_1",
		Username:    "testuser",
		Level:       10,
		Exp:         500,
		Gold:        1000,
		Diamond:     100,
		MMR:         1500,
		ELO:         1400,
		WinCount:    50,
		LoseCount:   30,
		Online:      true,
		LastLoginAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	assert.Equal(t, "player_1", p.ID)
	assert.Equal(t, "testuser", p.Username)
	assert.Equal(t, int32(10), p.Level)
	assert.Equal(t, int64(500), p.Exp)
	assert.Equal(t, int32(1500), p.MMR)
	assert.Equal(t, int32(1400), p.ELO)
	assert.True(t, p.Online)
}

func TestPlayer_JSONSerialization(t *testing.T) {
	p := Player{
		ID:       "player_1",
		Username: "testuser",
		MMR:      1500,
	}

	data, err := json.Marshal(p)
	assert.NoError(t, err)

	var decoded Player
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, p.ID, decoded.ID)
	assert.Equal(t, p.Username, decoded.Username)
	assert.Equal(t, p.MMR, decoded.MMR)
}

func TestPlayer_PasswordHashHidden(t *testing.T) {
	p := Player{
		ID:           "player_1",
		Username:     "testuser",
		PasswordHash: "secret_hash",
	}

	data, err := json.Marshal(p)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), "secret_hash")
}

func TestRoom_Struct(t *testing.T) {
	r := Room{
		ID:         "room_1",
		Name:       "Test Room",
		OwnerID:    "player_1",
		Status:     RoomStatusWaiting,
		MaxPlayers: 10,
		Mode:       GameMode5v5,
	}

	assert.Equal(t, "room_1", r.ID)
	assert.Equal(t, RoomStatusWaiting, r.Status)
	assert.Equal(t, GameMode5v5, r.Mode)
	assert.Equal(t, int32(10), r.MaxPlayers)
}

func TestRoomStatus_Constants(t *testing.T) {
	assert.Equal(t, RoomStatus("waiting"), RoomStatusWaiting)
	assert.Equal(t, RoomStatus("playing"), RoomStatusPlaying)
	assert.Equal(t, RoomStatus("ended"), RoomStatusEnded)
}

func TestGameMode_Constants(t *testing.T) {
	assert.Equal(t, GameMode("1v1"), GameMode1v1)
	assert.Equal(t, GameMode("5v5"), GameMode5v5)
	assert.Equal(t, GameMode("casual"), GameModeCasual)
	assert.Equal(t, GameMode("custom"), GameModeCustom)
	assert.Equal(t, GameMode("frame_sync"), GameModeFrameSync)
	assert.Equal(t, GameMode("state_sync"), GameModeStateSync)
}

func TestMatchResult_Struct(t *testing.T) {
	mr := MatchResult{
		RoomID:   "room_1",
		Players:  []string{"p1", "p2"},
		TeamA:    []string{"p1"},
		TeamB:    []string{"p2"},
		WaitTime: 5000,
		AvgMMR:   1500,
	}

	assert.Equal(t, "room_1", mr.RoomID)
	assert.Len(t, mr.Players, 2)
	assert.Equal(t, int64(5000), mr.WaitTime)
	assert.Equal(t, int32(1500), mr.AvgMMR)
}

func TestMatchTicket_Struct(t *testing.T) {
	mt := MatchTicket{
		PlayerID:  "p1",
		Mode:      GameMode1v1,
		MMR:       1500,
		ELO:       1400,
		Latency:   50,
		Timestamp: time.Now().Unix(),
	}

	assert.Equal(t, "p1", mt.PlayerID)
	assert.Equal(t, GameMode1v1, mt.Mode)
	assert.Equal(t, int32(50), mt.Latency)
}

func TestWSMessage_Struct(t *testing.T) {
	msg := WSMessage{
		Type:   WSMsgJoin,
		RoomID: "room_1",
		Data:   map[string]string{"key": "value"},
	}

	assert.Equal(t, WSMsgJoin, msg.Type)
	assert.Equal(t, "room_1", msg.RoomID)
}

func TestWSMessage_Constants(t *testing.T) {
	assert.Equal(t, "join", WSMsgJoin)
	assert.Equal(t, "leave", WSMsgLeave)
	assert.Equal(t, "input", WSMsgInput)
	assert.Equal(t, "frame", WSMsgFrame)
	assert.Equal(t, "state_delta", WSMsgStateDelta)
	assert.Equal(t, "heartbeat", WSMsgHeartbeat)
	assert.Equal(t, "reconnect", WSMsgReconnect)
	assert.Equal(t, "error", WSMsgError)
}

func TestWSMessage_JSONSerialization(t *testing.T) {
	msg := WSMessage{
		Type:   WSMsgInput,
		RoomID: "room_1",
		Frame:  42,
		Data:   "some_data",
	}

	data, err := json.Marshal(msg)
	assert.NoError(t, err)

	var decoded WSMessage
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, msg.Type, decoded.Type)
	assert.Equal(t, msg.RoomID, decoded.RoomID)
	assert.Equal(t, msg.Frame, decoded.Frame)
}

func TestInputCommand_Struct(t *testing.T) {
	cmd := InputCommand{
		Type:      InputTypeMove,
		Frame:     100,
		PlayerID:  "p1",
		Data:      []byte{1, 2, 3},
		Timestamp: time.Now().Unix(),
	}

	assert.Equal(t, InputTypeMove, cmd.Type)
	assert.Equal(t, int64(100), cmd.Frame)
}

func TestPosition_Velocity_Health(t *testing.T) {
	pos := Position{X: 1.0, Y: 2.0, Z: 3.0}
	assert.Equal(t, float32(1.0), pos.X)

	vel := Velocity{DX: 0.1, DY: 0.2, DZ: 0.3}
	assert.Equal(t, float32(0.1), vel.DX)

	hp := Health{Current: 80, Max: 100}
	assert.Equal(t, int32(80), hp.Current)
	assert.Equal(t, int32(100), hp.Max)
}

func TestRoomMember_Struct(t *testing.T) {
	rm := RoomMember{
		RoomID:   "room_1",
		PlayerID: "p1",
		TeamID:   1,
		Role:     "tank",
		HeroID:   5,
		MMR:      1500,
		Online:   true,
	}

	assert.Equal(t, "room_1", rm.RoomID)
	assert.Equal(t, int32(1), rm.TeamID)
	assert.Equal(t, "tank", rm.Role)
}

func TestMatchHistory_Struct(t *testing.T) {
	mh := MatchHistory{
		PlayerID:  "p1",
		RoomID:    "room_1",
		Mode:      GameMode1v1,
		MMRBefore: 1500,
		MMRAfter:  1520,
		IsWin:     true,
		IsDraw:    false,
		WaitTime:  3000,
		Duration:  600,
	}

	assert.Equal(t, "p1", mh.PlayerID)
	assert.True(t, mh.IsWin)
	assert.False(t, mh.IsDraw)
	assert.Equal(t, int32(1500), mh.MMRBefore)
	assert.Equal(t, int32(1520), mh.MMRAfter)
}

func TestGlicko2Params_Struct(t *testing.T) {
	params := Glicko2Params{
		Rating:     1500,
		Deviation:  350,
		Volatility: 0.06,
	}
	assert.Equal(t, 1500.0, params.Rating)
	assert.Equal(t, 350.0, params.Deviation)
	assert.Equal(t, 0.06, params.Volatility)
}
