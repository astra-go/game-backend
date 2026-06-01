package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/astra-go/game-backend/pkg/common"
	"github.com/astra-go/astra"
	"github.com/golang-jwt/jwt/v5"
	"github.com/astra-go/astra/log"
)

// 上下文键，用于存储认证信息
const (
	ContextKeyPlayerID = "player_id"
	ContextKeyUsername = "username"
)

// 统一响应结构
type response struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// AuthMiddleware JWT认证中间件
// 从Header(Authorization: Bearer xxx)或Query参数(token=xxx)提取token
// 验证有效性后将playerID和username存入context
func AuthMiddleware(logger *log.Logger) astra.HandlerFunc {
	return func(c *astra.Ctx) error {
		var tokenStr string

		// 1. 优先从Header提取
		authHeader := c.Request().Header.Get("Authorization")
		if authHeader != "" {
			// 格式: Bearer xxx
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				tokenStr = strings.TrimSpace(parts[1])
			}
		}

		// 2. Header没有则从Query参数提取（方便WebSocket连接）
		if tokenStr == "" {
			tokenStr = c.Query("token")
		}

		// 3. 没有token
		if tokenStr == "" {
			logger.Warn("请求未携带token")
			return c.JSON(http.StatusUnauthorized, response{
				Code: 401,
				Msg:  "未授权",
			})
		}

		// 4. 验证token
		claims, err := common.ValidateToken(tokenStr)
		if err != nil {
			// 区分过期和无效
			if errors.Is(err, jwt.ErrTokenExpired) {
				logger.Warn("token已过期", "error", err)
				return c.JSON(http.StatusUnauthorized, response{
					Code: 401,
					Msg:  "token已过期",
				})
			}
			logger.Warn("token无效", "error", err)
			return c.JSON(http.StatusUnauthorized, response{
				Code: 401,
				Msg:  "未授权",
			})
		}

		// 5. 将认证信息存入context
		c.Set(ContextKeyPlayerID, claims.PlayerID)
		c.Set(ContextKeyUsername, claims.Username)

		return nil
	}
}
