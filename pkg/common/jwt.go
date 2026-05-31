package common

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTSecret JWT签名密钥
// 生产环境应从环境变量或配置文件读取
var JWTSecret = []byte("astra-game-secret-key-change-in-production")

// JWTClaims JWT声明结构体
type JWTClaims struct {
	PlayerID string `json:"player_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken 生成JWT token
// 参数:
//   - playerID: 玩家ID
//   - username: 用户名
//
// 返回:
//   - string: JWT token字符串
//   - error: 错误信息
func GenerateToken(playerID, username string) (string, error) {
	// 设置token有效期为24小时
	expirationTime := time.Now().Add(24 * time.Hour)

	// 创建声明
	claims := &JWTClaims{
		PlayerID: playerID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "astra-game",
			Subject:   playerID,
		},
	}

	// 使用HS256算法创建token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// 签名并返回token字符串
	tokenString, err := token.SignedString(JWTSecret)
	if err != nil {
		return "", fmt.Errorf("生成token失败: %w", err)
	}

	return tokenString, nil
}

// ValidateToken 验证JWT token
// 参数:
//   - tokenString: JWT token字符串
//
// 返回:
//   - *JWTClaims: 解析出的声明
//   - error: 错误信息
func ValidateToken(tokenString string) (*JWTClaims, error) {
	// 解析token
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// 验证签名算法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return JWTSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("解析token失败: %w", err)
	}

	// 验证token是否有效
	if !token.Valid {
		return nil, fmt.Errorf("token无效")
	}

	// 提取声明
	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("无法提取token声明")
	}

	return claims, nil
}

// ParseToken 解析JWT token（不带验证，用于调试）
// 参数:
//   - tokenString: JWT token字符串
//
// 返回:
//   - *JWTClaims: 解析出的声明
//   - error: 错误信息
func ParseToken(tokenString string) (*JWTClaims, error) {
	// 解析token但不验证签名（仅用于调试）
	parser := jwt.NewParser()
	claims := &JWTClaims{}
	_, _, err := parser.ParseUnverified(tokenString, claims)
	if err != nil {
		return nil, fmt.Errorf("解析token失败: %w", err)
	}

	return claims, nil
}

// GetPlayerIDFromToken 从token中提取player_id（便捷方法）
func GetPlayerIDFromToken(tokenString string) (string, error) {
	claims, err := ValidateToken(tokenString)
	if err != nil {
		return "", err
	}
	return claims.PlayerID, nil
}

// GetUsernameFromToken 从token中提取username（便捷方法）
func GetUsernameFromToken(tokenString string) (string, error) {
	claims, err := ValidateToken(tokenString)
	if err != nil {
		return "", err
	}
	return claims.Username, nil
}

// IsTokenExpired 检查token是否过期
func IsTokenExpired(tokenString string) (bool, error) {
	claims, err := ValidateToken(tokenString)
	if err != nil {
		return true, err
	}

	// 检查是否过期
	if claims.ExpiresAt != nil && claims.ExpiresAt.Time.Before(time.Now()) {
		return true, nil
	}

	return false, nil
}
