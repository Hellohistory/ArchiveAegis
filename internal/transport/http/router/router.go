// Package router file: internal/transport/http/router/router.go
package router

import (
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/service/workflow"
	"ArchiveAegis/internal/transport/http/middleware"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// Dependencies 结构体定义了所有路由处理器所依赖的服务和配置
type Dependencies struct {
	Registry           map[string]port.Executor
	AdminConfigService port.QueryAdminConfigService
	PluginManager      *plugin_manager.PluginManager
	RateLimiter        *aegmiddleware.BusinessRateLimiter
	AuthDB             *sql.DB
	SetupToken         string
	SetupTokenDeadline time.Time
	WorkflowService    *workflow.Service
}

// New 创建并配置一个基于 Gin 的 HTTP 路由器。
// 这是应用 HTTP 服务的总入口点。
func New(deps Dependencies) http.Handler {
	// 使用 Gin 的默认配置创建一个路由器实例
	router := gin.Default()

	// --- 全局中间件注册 ---
	// 注册 Prometheus 指标中间件
	router.Use(aegobserve.PrometheusMiddleware())
	// 注册 Gzip 压缩中间件
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	// 注册 CORS (跨域资源共享) 中间件
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	// 注册全局错误处理中间件
	router.Use(middleware.ErrorHandlingMiddleware())

	// 初始化认证服务
	authService := service.NewAuthenticator(deps.AuthDB)

	// --- API 路由组定义 (v1) ---
	apiV1 := router.Group("/api/v1")
	{
		// --- 系统/认证平面 (System/Authentication Plane) ---
		// 这部分路由不需要认证，用于系统初始化和用户登录
		registerSystemAndAuthRoutes(apiV1, deps)

		// --- 元数据/发现平面 (Metadata/Discovery Plane) ---
		// 这部分路由需要认证，用于查询业务元信息
		metaGroup := apiV1.Group("/meta")
		metaGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			registerMetaRoutes(metaGroup, deps)
		}

		// --- 数据平面 (Data Plane) ---
		// 这部分路由需要认证，用于核心的数据查询和修改操作
		dataGroup := apiV1.Group("/data")
		dataGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			registerDataRoutes(dataGroup, deps)
		}

		// --- 工作流操作平面 (Workflow Operation Plane) ---
		// 这部分路由需要认证，用于触发工作流
		workflowGroup := apiV1.Group("/workflows")
		workflowGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			registerWorkflowExecutionRoutes(workflowGroup, deps)
		}

		// --- 控制平面 (Admin Control Plane) ---
		// 这部分路由需要管理员权限
		adminGroup := apiV1.Group("/admin")
		adminGroup.Use(authMiddleware(authService), requireAdmin(), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			// 指标端点
			adminGroup.GET("/metrics", gin.WrapH(aegobserve.Handler()))

			// 插件管理路由
			pluginAdminGroup := adminGroup.Group("/plugins")
			registerPluginAdminRoutes(pluginAdminGroup, deps)

			// 业务配置管理路由
			bizConfigGroup := adminGroup.Group("/biz-config")
			registerBizConfigAdminRoutes(bizConfigGroup, deps)

			// 全局安全配置路由
			securityGroup := adminGroup.Group("/security")
			registerSecurityAdminRoutes(securityGroup, deps)

			// 工作流管理路由
			adminWorkflowGroup := adminGroup.Group("/workflows")
			registerWorkflowAdminRoutes(adminWorkflowGroup, deps)
		}
	}

	return router
}
