package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateELO(t *testing.T) {
	tests := []struct {
		name              string
		winnerRating      int32
		loserRating       int32
		expectWinnerUp    bool
		expectLoserDown   bool
	}{
		{"equal ratings", 1000, 1000, true, true},
		{"winner higher", 1500, 1000, true, true},
		{"winner lower (upset)", 1000, 1500, true, true},
		{"low ratings (K=40)", 500, 500, true, true},
		{"high ratings (K=10)", 2500, 2500, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newWinner, newLoser, change := CalculateELO(tt.winnerRating, tt.loserRating)
			if tt.expectWinnerUp {
				assert.Greater(t, newWinner, tt.winnerRating, "winner rating should increase")
			}
			if tt.expectLoserDown {
				assert.Less(t, newLoser, tt.loserRating, "loser rating should decrease")
			}
			assert.Greater(t, change, int32(0), "change should be positive for winner")
		})
	}
}

func TestCalculateELO_EqualRatings(t *testing.T) {
	// Equal ratings: both 1000, K=20
	newWinner, newLoser, change := CalculateELO(1000, 1000)
	// Expected win = 0.5, change = round(20 * (1 - 0.5)) = 10
	assert.Equal(t, int32(1010), newWinner)
	assert.Equal(t, int32(990), newLoser)
	assert.Equal(t, int32(10), change)
}

func TestCalculateELO_Upset(t *testing.T) {
	// Low rated beats high rated: bigger change
	_, _, changeLow := CalculateELO(800, 1500)
	_, _, changeHigh := CalculateELO(1500, 800)
	// Upset should give bigger change than expected outcome
	assert.Greater(t, changeLow, changeHigh)
}

func TestCalculateELO_FloorProtection(t *testing.T) {
	// Very low rated loser should not go below 0
	newWinner, newLoser, _ := CalculateELO(2000, 0)
	assert.GreaterOrEqual(t, newLoser, int32(0))
	assert.GreaterOrEqual(t, newWinner, int32(0))
}

func TestDynamicKFactor(t *testing.T) {
	tests := []struct {
		name   string
		rating int32
		want   float64
	}{
		{"new player (<1000)", 500, 40.0},
		{"mid player (1000-2000)", 1500, 20.0},
		{"high player (>2000)", 2500, 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, dynamicKFactor(tt.rating))
		})
	}
}

func TestDynamicKFactorWithGames(t *testing.T) {
	tests := []struct {
		name        string
		rating      int32
		gamesPlayed int
		want        float64
	}{
		{"few games", 1500, 10, 40.0},
		{"many games mid", 1500, 100, 20.0},
		{"many games high", 2500, 100, 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DynamicKFactorWithGames(tt.rating, tt.gamesPlayed))
		})
	}
}

func TestExpectedScore(t *testing.T) {
	// Equal ratings: expected = 0.5
	assert.InDelta(t, 0.5, expectedScore(1000, 1000), 0.001)
	// Higher rated should have higher expected score
	assert.Greater(t, expectedScore(1500, 1000), 0.5)
	assert.Less(t, expectedScore(1000, 1500), 0.5)
}

func TestCalculateMMR(t *testing.T) {
	tests := []struct {
		name         string
		playerMMR    int32
		opponents    []int32
		won          bool
		isDraw       bool
		gamesPlayed  int
		currentK     float64
		expectUp     bool
		expectDown   bool
	}{
		{"win", 1000, []int32{1000}, true, false, 100, 0, true, false},
		{"loss", 1000, []int32{1000}, false, false, 100, 0, false, true},
		{"draw equal", 1000, []int32{1000}, false, true, 100, 0, false, false},
		{"win vs higher", 1000, []int32{1500}, true, false, 100, 0, true, false},
		{"loss vs higher", 1000, []int32{1500}, false, false, 100, 0, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newMMR, change := CalculateMMR(tt.playerMMR, tt.opponents, tt.won, tt.isDraw, tt.gamesPlayed, tt.currentK)
			if tt.expectUp {
				assert.Greater(t, newMMR, tt.playerMMR)
				assert.Greater(t, change, int32(0))
			} else if tt.expectDown {
				assert.Less(t, newMMR, tt.playerMMR)
				assert.Less(t, change, int32(0))
			} else {
				// Draw at equal MMR: minimal change
				assert.Equal(t, tt.playerMMR, newMMR)
				assert.Equal(t, int32(0), change)
			}
		})
	}
}

func TestCalculateMMR_WithCustomK(t *testing.T) {
	// Explicit K factor
	newMMR, change := CalculateMMR(1000, []int32{1000}, true, false, 100, 32.0)
	assert.Greater(t, newMMR, int32(1000))
	assert.Greater(t, change, int32(0))
}

func TestCalculateMMRSimple(t *testing.T) {
	newMMR, change := CalculateMMRSimple(1000, []int32{1000}, true, false)
	assert.Greater(t, newMMR, int32(1000))
	assert.Greater(t, change, int32(0))
}

func TestDefaultGlicko2(t *testing.T) {
	params := DefaultGlicko2()
	assert.Equal(t, 1500.0, params.Rating)
	assert.Equal(t, 350.0, params.Deviation)
	assert.Equal(t, 0.06, params.Volatility)
}

func TestMatchQuality(t *testing.T) {
	tests := []struct {
		name    string
		mmrA    int32
		mmrB    int32
		devA    float64
		devB    float64
		wantMin float64
		wantMax float64
	}{
		{"equal mmr", 1000, 1000, 350, 350, 0.9, 1.0},
		{"close mmr", 1000, 1100, 350, 350, 0.7, 1.0},
		{"far mmr", 1000, 2000, 350, 350, 0.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := MatchQuality(tt.mmrA, tt.mmrB, tt.devA, tt.devB)
			assert.GreaterOrEqual(t, q, tt.wantMin)
			assert.LessOrEqual(t, q, tt.wantMax)
		})
	}
}

func TestStreakBonus(t *testing.T) {
	tests := []struct {
		name        string
		baseChange  int32
		winStreak   int
		loseStreak  int
		wantMin     int32
	}{
		{"no streak", 10, 0, 0, 0},
		{"3 win streak", 10, 3, 0, 1},   // 10% bonus = 1
		{"5 win streak", 10, 5, 0, 2},   // 20% bonus = 2
		{"3 lose streak", -10, 0, 3, 0}, // 5% of -10 = -0.5, truncated to 0
		{"5 lose streak", -10, 0, 5, 0},  // full protection
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bonus := StreakBonus(tt.baseChange, tt.winStreak, tt.loseStreak)
			if tt.wantMin > 0 {
				assert.GreaterOrEqual(t, bonus, tt.wantMin)
			}
			if tt.name == "no streak" {
				assert.Equal(t, int32(0), bonus)
			}
		})
	}
}
