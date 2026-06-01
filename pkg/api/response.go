package api

import (
	"net/http"

	"github.com/astra-go/astra"
)

// 统一响应结构
type apiResponse struct {
	Code int    `json:"code"`
	Data any    `json:"data,omitempty"`
	Msg  string `json:"msg"`
}

// ResponseOK 返回成功响应
func ResponseOK(c *astra.Ctx, data any) error {
	return c.JSON(http.StatusOK, apiResponse{
		Code: 0,
		Data: data,
		Msg:  "ok",
	})
}

// ResponseError 返回错误响应
func ResponseError(c *astra.Ctx, code int, msg string) error {
	// 根据业务错误码映射HTTP状态码
	httpCode := code
	if code >= 1000 {
		// 自定义业务错误码,统一返回400
		httpCode = http.StatusBadRequest
	}
	return c.JSON(httpCode, apiResponse{
		Code: code,
		Msg:  msg,
	})
}
