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
		// 首先，执行请求链中的后续操作（即你的API处理器）
		c.Next()

		// c.Next() 执行完毕后，检查上下文中是否有错误
		// 处理器中通过 c.Error(err) 附加的错误都会被收集到 c.Errors
		if len(c.Errors) == 0 {
			return // 没有错误，直接返回
		}

		// 我们只处理最后一个错误，因为它通常是根本原因
		lastError := c.Errors.Last()
		err := lastError.Err

		// 检查是否是参数绑定或验证错误
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			// 构建一个更友好的验证错误消息
			// out := make([]map[string]interface{}, len(ve))
			// for i, fe := range ve {
			// 	out[i] = map[string]interface{}{"field": fe.Field(), "tag": fe.Tag(), "value": fe.Param()}
			// }
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数验证失败", "details": ve.Error()})
			return
		}

		// 根据我们定义的业务错误类型，返回不同的HTTP状态码
		switch {
		case errors.Is(err, port.ErrPermissionDenied):
			c.JSON(http.StatusForbidden, gin.H{"error": "权限不足"})

		case errors.Is(err, port.ErrBizNotFound), errors.Is(err, port.ErrTableNotFoundInBiz):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})

		// 可以在这里添加更多自定义错误的判断
		// case errors.Is(err, anotherCustomError):
		// 	c.JSON(http.StatusConflict, gin.H{"error": err.Error()})

		default:
			// 对于所有其他未知错误，返回 500 服务器内部错误
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
		}
	}
}
