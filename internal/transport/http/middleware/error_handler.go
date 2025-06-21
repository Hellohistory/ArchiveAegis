// Package middleware file: internal/transport/http/middleware/error_handler.go
package middleware

import (
	"ArchiveAegis/internal/core/port"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// ErrorHandlingMiddleware 是一个Gin中间件，用于集中处理错误。
func ErrorHandlingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return // 没有错误，直接返回
		}

		lastError := c.Errors.Last()
		err := lastError.Err

		// 检查是否是参数绑定或验证错误
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {

			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数验证失败", "details": ve.Error()})
			return
		}

		// 根据定义的业务错误类型，返回不同的HTTP状态码
		switch {
		case errors.Is(err, port.ErrPermissionDenied):
			c.JSON(http.StatusForbidden, gin.H{"error": "权限不足"})

		case errors.Is(err, port.ErrBizNotFound), errors.Is(err, port.ErrTableNotFoundInBiz):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})

		default:
			// 对于所有其他未知错误，返回 500 服务器内部错误
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
		}
	}
}
