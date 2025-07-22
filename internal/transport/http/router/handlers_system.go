// Package router file: internal/transport/http/router/handlers_system.go
package router

import (
	"ArchiveAegis/internal/service"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// registerSystemAndAuthRoutes 注册系统和认证相关的路由。
// 这些端点通常是公开的，用于系统初始化和用户登录。
func registerSystemAndAuthRoutes(group *gin.RouterGroup, deps Dependencies) {
	// --- /api/v1/auth ---
	authGroup := group.Group("/auth")
	authGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
	{
		authGroup.POST("/login", loginHandler(deps.AuthDB))
	}

	// --- /api/v1/system ---
	systemGroup := group.Group("/system")
	systemGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
	{
		// .Any() 允许 GET (获取 token) 和 POST (执行安装)
		systemGroup.Any("/setup", setupHandler(deps.AuthDB, deps.SetupToken, deps.SetupTokenDeadline))
		systemGroup.GET("/status", statusHandler(deps.AuthDB))
	}
}

// =============================================================================
//  系统与认证处理器 (System & Authentication Handlers)
// =============================================================================

// statusHandler 检查系统当前的状态。
// 它判断系统中是否已存在用户，来决定系统是“需要安装”还是“准备好登录”。
func statusHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service.UserCount(db) > 0 {
			c.JSON(http.StatusOK, gin.H{"status": "ready_for_login"})
		} else {
			c.JSON(http.StatusOK, gin.H{"status": "needs_setup"})
		}
	}
}

// loginHandler 处理用户的登录请求。
// 它验证用户名和密码，如果成功，则生成并返回一个 JWT。
func loginHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			User string `form:"user" json:"user" binding:"required"`
			Pass string `form:"pass" json:"pass" binding:"required"`
		}
		if err := c.ShouldBind(&req); err != nil {
			_ = c.Error(err)
			return
		}

		id, role, ok := service.CheckUser(db, req.User, req.Pass)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码无效"})
			return
		}

		token, err := service.GenToken(id, role)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": id, "username": req.User, "role": role}})
	}
}

// setupHandler 处理系统的初始化安装。
// GET 请求用于在系统未安装时获取一次性的安装令牌。
// POST 请求使用该令牌来创建第一个管理员账户。
func setupHandler(db *sql.DB, token string, deadline time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		// --- 处理 GET 请求: 获取安装令牌 ---
		if c.Request.Method == http.MethodGet {
			if service.UserCount(db) > 0 {
				c.JSON(http.StatusForbidden, gin.H{"error": "系统已安装，无法获取安装令牌"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": token})
			return
		}

		// --- 处理 POST 请求: 执行安装 ---
		if c.Request.Method == http.MethodPost {
			if service.UserCount(db) > 0 {
				_ = c.Error(errors.New("系统已存在管理员账户，无法重复设置"))
				return
			}
			var req struct {
				Token string `form:"token" json:"token" binding:"required"`
				User  string `form:"user" json:"user" binding:"required"`
				Pass  string `form:"pass" json:"pass" binding:"required"`
			}
			if err := c.ShouldBind(&req); err != nil {
				_ = c.Error(err)
				return
			}

			if req.Token != token || token == "" || time.Now().After(deadline) {
				_ = c.Error(errors.New("无效或过期的安装令牌"))
				return
			}

			if err := service.CreateAdmin(db, req.User, req.Pass); err != nil {
				_ = c.Error(fmt.Errorf("创建管理员失败: %w", err))
				return
			}

			// 创建成功后，立即为新管理员生成一个 JWT 并返回，以便直接登录
			id, _, _ := service.CheckUser(db, req.User, req.Pass)
			jwtToken, err := service.GenToken(id, "admin")
			if err != nil {
				_ = c.Error(fmt.Errorf("为新管理员生成令牌失败: %w", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": jwtToken, "user": gin.H{"id": id, "username": req.User, "role": "admin"}})
			return
		}

		// --- 处理其他 HTTP 方法 ---
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "仅支持 GET 和 POST 方法"})
	}
}
