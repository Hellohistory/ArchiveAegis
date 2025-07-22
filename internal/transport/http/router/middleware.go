// Package router file: internal/transport/http/router/middleware.go
package router

import (
	"ArchiveAegis/internal/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// Gin 中间件 (Middleware)
// =============================================================================

// WrapNetHTTP 是一个高阶函数，用于将标准的 `net/http` 中间件包装成 Gin 的 `HandlerFunc`。
// 这使得我们可以复用不特定于 Gin 框架的通用 HTTP 中间件。
func WrapNetHTTP(middleware func(http.Handler) http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 创建一个 `http.HandlerFunc` 作为下一个处理器，
		// 它在被调用时，会触发 Gin 的 c.Next() 来执行后续的 Gin 处理器。
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Next()
		})
		// 将 nextHandler 传递给我们想要执行的 `net/http` 中间件，
		// 这样它就可以在自己的逻辑执行完毕后调用 `next`。
		handlerToExec := middleware(nextHandler)
		// 使用 Gin 的 ResponseWriter 和 Request 来服务这个中间件链。
		handlerToExec.ServeHTTP(c.Writer, c.Request)
	}
}

// authMiddleware 创建一个 Gin 中间件，用于处理用户认证。
// 它内部调用了 `service.Authenticator` 的标准 `net/http` 中间件，
// 并通过 `ServeHTTP` 将其集成到 Gin 的处理流程中。
func authMiddleware(auth *service.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		// `auth.Middleware` 返回一个标准的 `http.Handler`。
		// 我们提供一个 `http.HandlerFunc` 作为其 "next" 参数。
		handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 当认证成功后，`auth.Middleware` 会调用这个 next 函数。
			// 认证服务可能会将解析出的用户信息（claims）放入 `r.Context()` 中。
			// 我们需要用这个可能被修改过的 request 对象替换掉 Gin 上下文中的原始 request 对象。
			c.Request = r
			// 调用 c.Next() 继续执行后续的 Gin 处理器。
			c.Next()
		}))
		// 执行这个认证处理器。
		handler.ServeHTTP(c.Writer, c.Request)
	}
}

// requireAdmin 是一个 Gin 中间件，用于确保当前用户是管理员。
// 它必须在 `authMiddleware` 之后使用，因为它依赖于 `authMiddleware` 注入到请求上下文中的用户身份信息。
func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从请求上下文中提取用户声明 (claims)
		claims := service.ClaimFrom(c.Request)
		if claims == nil {
			// 如果上下文中没有 claims，说明之前的认证步骤失败或被跳过。
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "需要认证"})
			return
		}
		// 检查用户的角色是否为 "admin"
		if claims.Role != "admin" {
			// 如果不是管理员，则中止请求，并返回 403 Forbidden。
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		// 权限检查通过，继续处理请求。
		c.Next()
	}
}
