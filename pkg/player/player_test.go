package player

import (
	"testing"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/stretchr/testify/assert"
)

// Since PlayerComponent depends on gorm.DB and redis.Client which require external services,
// we test the MMR calculation logic directly via the common package functions,
// and test the streak calculation and helper logic that we can access.

func TestCalculateStreaks(t *testing.T) {
	pc := &PlayerComponent{} // zero-value for testing unexported method access via exported logic

	tests := []struct {
		name         string
		games        []common.MatchHistory
		wantWin      int
		wantLose     int
	}{
		{
			name:     "empty history",
			games:    nil,
			wantWin:  0,
			wantLose: 0,
		},
		{
			name: "3 win streak",
			games: []common.MatchHistory{
				{IsWin: true},
				{IsWin: true},
				{IsWin: true},
			},
			wantWin:  3,
			wantLose: 0,
		},
		{
			name: "3 lose streak",
			games: []common.MatchHistory{
				{IsWin: false},
				{IsWin: false},
				{IsWin: false},
			},
			wantWin:  0,
			wantLose: 3,
		},
		{
			name: "mixed (last is win)",
			games: []common.MatchHistory{
				{IsWin: true},
				{IsWin: true},
				{IsWin: false},
				{IsWin: true},
			},
			wantWin:  1,
			wantLose: 0,
		},
		{
			name: "mixed (last is loss)",
			games: []common.MatchHistory{
				{IsWin: false},
				{IsWin: true},
				{IsWin: false},
			},
			wantWin:  0,
			wantLose: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winStreak, loseStreak := pc.calculateStreaks(tt.games)
			assert.Equal(t, tt.wantWin, winStreak)
			assert.Equal(t, tt.wantLose, loseStreak)
		})
	}
}

func TestMMRCalculationIntegration(t *testing.T) {
	// Test that UpdateMMR correctly uses the common.CalculateMMR function
	// by verifying the calculation logic independently
	tests := []struct {
		name        string
		playerMMR   int32
		opponents   []int32
		won         bool
		isDraw      bool
		gamesPlayed int
	}{
		{"win increases MMR", 1000, []int32{1000}, true, false, 50},
		{"loss decreases MMR", 1000, []int32{1000}, false, false, 50},
		{"draw equal MMR no change", 1000, []int32{1000}, false, true, 50},
		{"upset win big gain", 800, []int32{1500}, true, false, 50},
		{"expected loss small loss", 1500, []int32{800}, false, false, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newMMR, change := common.CalculateMMR(tt.playerMMR, tt.opponents, tt.won, tt.isDraw, tt.gamesPlayed, 0)

			if tt.won && !tt.isDraw {
				assert.Greater(t, newMMR, tt.playerMMR, "win should increase MMR")
				assert.Greater(t, change, int32(0))
			} else if !tt.won && !tt.isDraw {
				assert.Less(t, newMMR, tt.playerMMR, "loss should decrease MMR")
				assert.Less(t, change, int32(0))
			} else {
				// Draw at equal MMR
				assert.Equal(t, tt.playerMMR, newMMR)
				assert.Equal(t, int32(0), change)
			}
		})
	}
}

func TestStreakBonusIntegration(t *testing.T) {
	// Verify streak bonus is applied correctly via common package
	tests := []struct {
		name       string
		baseChange int32
		winStreak  int
		loseStreak int
	}{
		{"no streak no bonus", 10, 0, 0},
		{"3 win streak +10%", 10, 3, 0},
		{"5 win streak +20%", 10, 5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bonus := common.StreakBonus(tt.baseChange, tt.winStreak, tt.loseStreak)
			if tt.winStreak >= 5 {
				assert.Equal(t, int32(2), bonus) // 20% of 10
			} else if tt.winStreak >= 3 {
				assert.Equal(t, int32(1), bonus) // 10% of 10
			} else {
				assert.Equal(t, int32(0), bonus)
			}
		})
	}
}

func TestELOCalculationIntegration(t *testing.T) {
	// Verify ELO calculation used by UpdateMMR
	newWinner, newLoser, change := common.CalculateELO(1000, 1000)
	assert.Equal(t, int32(1010), newWinner)
	assert.Equal(t, int32(990), newLoser)
	assert.Equal(t, int32(10), change)
}

func TestNewPlayerComponent(t *testing.T) {
	// Test constructor with nil dependencies (component should still be created)
	pc := NewPlayerComponent(nil, nil, nil)
	assert.NotNil(t, pc)
}

func TestGeneratePlayerID(t *testing.T) {
	id1 := generatePlayerID()
	id2 := generatePlayerID()
	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	// IDs should be unique (extremely unlikely to collide)
	assert.NotEqual(t, id1, id2)
	// Should start with "player_"
	assert.Equal(t, "player_", id1[:7])
}
