package common

import (
	"math"
)

// ============================================
// ELO 计算公式（标准 ELO，含动态 K-factor）
// ============================================

// CalculateELO 标准 ELO 公式
// 参数:
//   - winnerRating: 胜者评分
//   - loserRating:  败者评分
//
// 返回:
//   - newWinnerRating: 胜者新评分
//   - newLoserRating:  败者新评分
//   - change:          分数变化量（绝对值）
func CalculateELO(winnerRating, loserRating int32) (newWinnerRating, newLoserRating int32, change int32) {
	k := dynamicKFactor(winnerRating)

	// 期望胜率
	expectedWin := expectedScore(winnerRating, loserRating)

	// 实际结果（胜者胜 = 1，败者胜 = 0）
	winnerActual := 1.0
	loserActual := 0.0

	// 分数变化
	winnerChange := int32(math.Round(k * (winnerActual - expectedWin)))
	loserChange := int32(math.Round(k * (loserActual - (1 - expectedScore(loserRating, winnerRating)))))

	newWinnerRating = winnerRating + winnerChange
	newLoserRating = loserRating + loserChange
	change = winnerChange

	// 最低分保护
	if newWinnerRating < 0 {
		newWinnerRating = 0
	}
	if newLoserRating < 0 {
		newLoserRating = 0
	}

	return
}

// expectedScore 计算期望胜率（ELO 标准公式）
// Ea = 1 / (1 + 10^((Rb - Ra) / 400))
func expectedScore(ratingA, ratingB int32) float64 {
	return 1.0 / (1.0 + math.Pow(10, float64(ratingB-ratingA)/400.0))
}

// dynamicKFactor 动态 K-factor
// - 新手（<30场 或 <1000分）: K = 40
// - 中等（1000-2000分）: K = 20
// - 高手（>2000分）: K = 10
func dynamicKFactor(rating int32) float64 {
	switch {
	case rating < 1000:
		return 40.0
	default:
		return 20.0
	case rating > 2000:
		return 10.0
	}
}

// DynamicKFactorWithGames 带场数的动态 K-factor（更精确）
// - 场次 < 30: K = 40（快速收敛）
// - 场次 >= 30: 根据分数使用不同 K
func DynamicKFactorWithGames(rating int32, gamesPlayed int) float64 {
	if gamesPlayed < 30 {
		return 40.0
	}
	return dynamicKFactor(rating)
}

// ============================================
// Glicko-2 MMR 计算公式（TrueSkill 风格）
// ============================================

// Glicko2Params Glicko-2 参数
type Glicko2Params struct {
	Rating    float64 // μ (mu) - 评分
	Deviation float64 // φ (phi) - 评分偏差（不确定性）
	Volatility float64 // σ (sigma) - 评分波动率
}

// DefaultGlicko2 默认 Glicko-2 参数
func DefaultGlicko2() Glicko2Params {
	return Glicko2Params{
		Rating:    1500,   // 初始评分
		Deviation: 350,    // 初始偏差（高不确定性）
		Volatility: 0.06,  // 初始波动率
	}
}

// CalculateMMR TrueSkill/Glicko-2 风格 MMR 计算
// 参数:
//   - playerMMR:    当前玩家 MMR
//   - opponentsMMR:  对手 MMR 列表
//   - won:          是否胜利
//   - isDraw:       是否平局
//   - gamesPlayed:  已玩场数（用于调整 K）
//   - currentK:     当前 K 因子（可选，0表示自动计算）
//
// 返回:
//   - newMMR: 新 MMR
//   - change: 变化量
func CalculateMMR(playerMMR int32, opponentsMMR []int32, won, isDraw bool, gamesPlayed int, currentK float64) (newMMR int32, change int32) {
	// 计算对手平均 MMR
	var totalMMR int32
	for _, m := range opponentsMMR {
		totalMMR += m
	}
	avgOpponentMMR := float64(totalMMR) / float64(len(opponentsMMR))

	// 确定 K 因子
	k := currentK
	if k == 0 {
		k = DynamicKFactorWithGames(playerMMR, gamesPlayed)
	}

	// 期望胜率
	expected := expectedScore(playerMMR, int32(avgOpponentMMR))

	// 实际结果
	var actual float64
	if isDraw {
		actual = 0.5
	} else if won {
		actual = 1.0
	} else {
		actual = 0.0
	}

	// MMR 变化
	delta := int32(math.Round(k * (actual - expected)))

	newMMR = playerMMR + delta
	change = delta

	// 最低分保护
	if newMMR < 0 {
		newMMR = 0
	}

	return
}

// CalculateMMRSimple 简化版 MMR 计算（不带场数）
func CalculateMMRSimple(playerMMR int32, opponentsMMR []int32, won, isDraw bool) (int32, int32) {
	return CalculateMMR(playerMMR, opponentsMMR, won, isDraw, 100, 0)
}

// ============================================
// 匹配质量评估
// ============================================

// MatchQuality 计算匹配质量（0.0 ~ 1.0，越高越好）
// 基于双方 MMR 差距和不确定性
func MatchQuality(mmrA, mmrB int32, devA, devB float64) float64 {
	mmrDiff := math.Abs(float64(mmrA - mmrB))
	avgDev := (devA + devB) / 2.0

	// 质量 = 1 / (1 + (MMR差距 / (平均偏差 * 2))^2)
	quality := 1.0 / (1.0 + math.Pow(mmrDiff/(avgDev*2), 2))

	return quality
}

// ============================================
// 连胜/连败 加成
// ============================================

// StreakBonus 连胜/连败加成
// - 3连胜: +10% 额外 MMR
// - 5连胜: +20% 额外 MMR
// - 3连败: -5% 额外 MMR（保护机制）
// - 5连败: 不扣额外 MMR（保护机制）
func StreakBonus(baseChange int32, winStreak, loseStreak int) int32 {
	if winStreak >= 5 {
		return int32(float64(baseChange) * 0.2)
	} else if winStreak >= 3 {
		return int32(float64(baseChange) * 0.1)
	}

	if loseStreak >= 5 {
		return 0 // 连败保护
	} else if loseStreak >= 3 {
		return int32(float64(baseChange) * 0.05) // 减少扣
	}

	return 0
}
