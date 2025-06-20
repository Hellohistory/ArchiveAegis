// Package router file: internal/transport/http/router/router.go
package router

import (
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/transport/http/middleware"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// Dependencies 结构体用于将所有依赖项注入到路由器中
type Dependencies struct {
	Registry           map[string]port.DataSource
	AdminConfigService port.QueryAdminConfigService
	PluginManager      *plugin_manager.PluginManager
	RateLimiter        *aegmiddleware.BusinessRateLimiter
	AuthDB             *sql.DB
	SetupToken         string
	SetupTokenDeadline time.Time
}

// New 创建并配置一个全新的、基于 Gin 的 HTTP 路由器
func New(deps Dependencies) http.Handler {
	router := gin.Default()

	// --- 全局中间件注册 ---
	router.Use(aegobserve.PrometheusMiddleware())
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	router.Use(middleware.ErrorHandlingMiddleware())

	authService := service.NewAuthenticator(deps.AuthDB)

	v1 := router.Group("/api/v1")
	{
		// --- 系统/认证平面 ---
		authGroup := v1.Group("/auth")
		authGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			authGroup.POST("/login", loginHandler(deps.AuthDB))
		}

		systemGroup := v1.Group("/system")
		systemGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			systemGroup.Any("/setup", setupHandler(deps.AuthDB, deps.SetupToken, deps.SetupTokenDeadline))
		}
		v1.GET("/system/status", statusHandler(deps.AuthDB))

		// --- 元数据/发现平面 ---
		metaGroup := v1.Group("/meta")
		metaGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			metaGroup.GET("/biz", bizHandlerV1(deps.Registry))
			metaGroup.GET("/schema/:bizName", schemaHandlerV1(deps.Registry))
			metaGroup.GET("/presentations", presentationsHandlerV1(deps.AdminConfigService))
		}

		// --- 数据平面 ---
		dataGroup := v1.Group("/data")
		dataGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			dataGroup.POST("/query", queryHandlerV1(deps.Registry))
			dataGroup.POST("/mutate", mutateHandlerV1(deps.Registry))
		}

		// --- 控制平面 (Admin) ---
		adminGroup := v1.Group("/admin")
		adminGroup.Use(authMiddleware(authService), requireAdmin(), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			adminGroup.GET("/metrics", gin.WrapH(aegobserve.Handler()))

			pluginAdminGroup := adminGroup.Group("/plugins")
			{
				pluginAdminGroup.GET("/available", listAvailablePluginsHandler(deps.PluginManager))
				pluginAdminGroup.POST("/install", installPluginHandler(deps.PluginManager))
				pluginAdminGroup.POST("/instances", createInstanceHandler(deps.PluginManager))
				pluginAdminGroup.GET("/instances", listInstancesHandler(deps.PluginManager))
				pluginAdminGroup.DELETE("/instances/:instance_id", deleteInstanceHandler(deps.PluginManager))
				pluginAdminGroup.POST("/instances/:instance_id/start", startInstanceHandler(deps.PluginManager))
				pluginAdminGroup.POST("/instances/:instance_id/stop", stopInstanceHandler(deps.PluginManager))
			}

			bizConfigGroup := adminGroup.Group("/biz-config")
			{
				bizConfigGroup.GET("/", adminGetConfiguredBizNamesHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/:bizName", getBizConfigHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/:bizName/settings", updateBizOverallSettingsHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/:bizName/tables", adminUpdateBizSearchableTablesHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/:bizName/rate-limit", adminGetBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/:bizName/rate-limit", adminUpdateBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/:bizName/views", adminGetBizViewsHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/:bizName/views", adminUpdateBizViewsHandler(deps.AdminConfigService))

				tableGroup := bizConfigGroup.Group("/:bizName/tables/:tableName")
				{
					tableGroup.PUT("/fields", adminUpdateTableFieldSettingsHandler(deps.AdminConfigService))
					tableGroup.PUT("/permissions", adminUpdateTablePermissionsHandler(deps.AdminConfigService))
				}
			}

			securityGroup := adminGroup.Group("/security")
			{
				securityGroup.GET("/rate-limiting/global", adminGetIPLimitSettingsHandler(deps.AdminConfigService))
				securityGroup.PUT("/rate-limiting/global", adminUpdateIPLimitSettingsHandler(deps.AdminConfigService))
			}
		}
	}

	return router
}

// =============================================================================
// Gin 中间件 (Middleware)
// =============================================================================

// WrapNetHTTP 是一个更简洁、惯用的方式来包装 net/http 中间件给 Gin 使用
func WrapNetHTTP(middleware func(http.Handler) http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Next()
		})
		handlerToExec := middleware(nextHandler)
		handlerToExec.ServeHTTP(c.Writer, c.Request)
	}
}

func authMiddleware(auth *service.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Request = r
			c.Next()
		}))
		handler.ServeHTTP(c.Writer, c.Request)
	}
}

func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := service.ClaimFrom(c.Request)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "需要认证"})
			return
		}
		if claims.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

// =============================================================================
//  API 处理器 (Handlers)
// =============================================================================

// --- V1 数据平面处理器 (已更新以适配新协议) ---

// queryHandlerV1 现在处理通用的查询请求
func queryHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	// 请求体现在直接对应我们核心接口中的 port.QueryRequest
	type RequestBody struct {
		BizName string                 `json:"biz_name" binding:"required"`
		Query   map[string]interface{} `json:"query" binding:"required"`
	}

	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}

		dataSource, exists := registry[reqBody.BizName]
		if !exists {
			_ = c.Error(port.ErrBizNotFound)
			return
		}

		// 直接构建通用的 port.QueryRequest
		queryReq := port.QueryRequest{
			BizName: reqBody.BizName,
			Query:   reqBody.Query,
		}

		result, err := dataSource.Query(c.Request.Context(), queryReq)
		if err != nil {
			slog.Error("queryHandlerV1 执行失败", "biz", reqBody.BizName, "error", err)
			_ = c.Error(err)
			return
		}
		// 直接返回插件处理后的通用结果对象
		c.JSON(http.StatusOK, result)
	}
}

// mutateHandlerV1 现在处理通用的写操作请求
func mutateHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	// 请求体现在直接对应我们核心接口中的 port.MutateRequest
	type RequestBody struct {
		BizName   string                 `json:"biz_name" binding:"required"`
		Operation string                 `json:"operation" binding:"required"`
		Payload   map[string]interface{} `json:"payload" binding:"required"`
	}

	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}

		dataSource, exists := registry[reqBody.BizName]
		if !exists {
			_ = c.Error(port.ErrBizNotFound)
			return
		}

		slog.Info(
			"审计日志: 收到 Mutate 请求",
			"user_id", service.ClaimFrom(c.Request).ID,
			"biz_name", reqBody.BizName,
			"operation", reqBody.Operation,
		)

		// 直接构建通用的 port.MutateRequest
		mutateReq := port.MutateRequest{
			BizName:   reqBody.BizName,
			Operation: reqBody.Operation,
			Payload:   reqBody.Payload,
		}

		result, err := dataSource.Mutate(c.Request.Context(), mutateReq)
		if err != nil {
			slog.Error("mutateHandlerV1 执行失败", "biz", reqBody.BizName, "error", err)
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

// =============================================================================
//  V1 版本的新/重构处理器 (New/Refactored V1 Handlers)
// =============================================================================

// --- V1 元数据平面处理器 ---

// bizHandlerV1 返回所有已注册的业务组名称
func bizHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizNames := make([]string, 0, len(registry))
		for name := range registry {
			bizNames = append(bizNames, name)
		}
		sort.Strings(bizNames)
		c.JSON(http.StatusOK, gin.H{"data": bizNames})
	}
}

// schemaHandlerV1 返回指定业务组的 Schema 信息
func schemaHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		dataSource, exists := registry[bizName]
		if !exists {
			_ = c.Error(fmt.Errorf("业务组 '%s' 未找到或未注册", bizName)) // 使用错误中间件处理
			return
		}

		schema, err := dataSource.GetSchema(c.Request.Context(), port.SchemaRequest{BizName: bizName})
		if err != nil {
			_ = c.Error(err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"data": schema})
	}
}

// presentationsHandlerV1 返回指定业务组和表的默认表现层（视图）配置
func presentationsHandlerV1(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Query("biz")
		tableName := c.Query("table")
		if bizName == "" || tableName == "" {
			_ = c.Error(errors.New("缺少 'biz' 或 'table' 参数"))
			return
		}
		viewConfig, err := configService.GetDefaultViewConfig(c.Request.Context(), bizName, tableName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if viewConfig == nil {
			_ = c.Error(fmt.Errorf("未找到业务 '%s' 表 '%s' 的默认表现层配置", bizName, tableName))
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": viewConfig})
	}
}

// =============================================================================
//  系统与认证处理器
// =============================================================================

// statusHandler 返回系统状态，用于前端判断是否需要进入安装流程
func statusHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service.UserCount(db) > 0 {
			c.JSON(http.StatusOK, gin.H{"status": "ready_for_login"})
		} else {
			c.JSON(http.StatusOK, gin.H{"status": "needs_setup"})
		}
	}
}

// loginHandler 处理用户登录请求
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
			// 对于登录失败，我们直接返回401，不通过错误中间件
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

// setupHandler 处理首次安装时的管理员创建请求
func setupHandler(db *sql.DB, token string, deadline time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet {
			if service.UserCount(db) > 0 {
				c.JSON(http.StatusForbidden, gin.H{"error": "系统已安装，无法获取安装令牌"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": token})
			return
		}

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
			id, _, _ := service.CheckUser(db, req.User, req.Pass)
			jwtToken, err := service.GenToken(id, "admin")
			if err != nil {
				_ = c.Error(fmt.Errorf("为新管理员生成令牌失败: %w", err))
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": jwtToken, "user": gin.H{"id": id, "username": req.User, "role": "admin"}})
			return
		}
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "仅支持 GET 和 POST 方法"})
	}
}

// =============================================================================
//  管理员 API 处理器
// =============================================================================

func adminGetConfiguredBizNamesHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		names, err := configService.GetAllConfiguredBizNames(c.Request.Context())
		if err != nil {
			_ = c.Error(err)
			return
		}
		if names == nil {
			names = []string{}
		}
		c.JSON(http.StatusOK, names)
	}
}

func adminGetIPLimitSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings, err := configService.GetIPLimitSettings(c.Request.Context())
		if err != nil {
			_ = c.Error(err)
			return
		}
		if settings == nil {
			_ = c.Error(errors.New("未找到IP速率限制配置"))
			return
		}
		c.JSON(http.StatusOK, settings)
	}
}

func adminUpdateIPLimitSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload domain.IPLimitSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateIPLimitSettings(c.Request.Context(), payload); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

func getBizConfigHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		cfg, err := configService.GetBizQueryConfig(c.Request.Context(), bizName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if cfg == nil {
			_ = c.Error(port.ErrBizNotFound)
			return
		}
		c.JSON(http.StatusOK, cfg)
	}
}

func adminGetBizRateLimitHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		settings, err := configService.GetBizRateLimitSettings(c.Request.Context(), bizName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if settings == nil {
			_ = c.Error(errors.New("未找到该业务的速率限制配置"))
			return
		}
		c.JSON(http.StatusOK, settings)
	}
}

func adminUpdateBizRateLimitHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload domain.BizRateLimitSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateBizRateLimitSettings(c.Request.Context(), bizName, payload); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

func adminGetBizViewsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		views, err := configService.GetAllViewConfigsForBiz(c.Request.Context(), bizName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if views == nil {
			views = make(map[string][]*domain.ViewConfig)
		}
		c.JSON(http.StatusOK, views)
	}
}

func adminUpdateBizViewsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var viewsData map[string][]*domain.ViewConfig
		if err := c.ShouldBindJSON(&viewsData); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateAllViewsForBiz(c.Request.Context(), bizName, viewsData); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

func updateBizOverallSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload domain.BizOverallSettings
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateBizOverallSettings(c.Request.Context(), bizName, payload); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "业务组配置已更新"})
	}
}

func adminUpdateBizSearchableTablesHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload struct {
			SearchableTables []string `json:"searchable_tables"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateBizSearchableTables(c.Request.Context(), bizName, payload.SearchableTables); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "可搜索表列表已更新"})
	}
}

func adminUpdateTableFieldSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		tableName := c.Param("tableName")
		var payload []domain.FieldSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := configService.UpdateTableFieldSettings(c.Request.Context(), bizName, tableName, payload); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "字段配置已更新"})
	}
}

func adminUpdateTablePermissionsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	type permissionsPayload struct {
		AllowCreate bool `json:"allow_create"`
		AllowUpdate bool `json:"allow_update"`
		AllowDelete bool `json:"allow_delete"`
	}

	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		tableName := c.Param("tableName")

		var payload permissionsPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		perms := domain.TableConfig{
			AllowCreate: payload.AllowCreate,
			AllowUpdate: payload.AllowUpdate,
			AllowDelete: payload.AllowDelete,
		}
		if err := configService.UpdateTableWritePermissions(c.Request.Context(), bizName, tableName, perms); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "表的写权限已成功更新。"})
	}
}

// listAvailablePluginsHandler 返回所有可供安装的插件列表。
func listAvailablePluginsHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		availablePlugins := pluginManager.GetAvailablePlugins()
		if availablePlugins == nil {
			availablePlugins = make([]domain.PluginManifest, 0)
		}
		c.JSON(http.StatusOK, gin.H{"data": availablePlugins})
	}
}

// installPluginHandler 处理安装特定版本插件的请求。这是一个简化的接口。
func installPluginHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	type installPayload struct {
		PluginID string `json:"plugin_id" binding:"required"`
		Version  string `json:"version" binding:"required"`
	}
	return func(c *gin.Context) {
		var payload installPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		if err := pluginManager.Install(payload.PluginID, payload.Version); err != nil {
			_ = c.Error(fmt.Errorf("插件 '%s' v%s 安装失败: %w", payload.PluginID, payload.Version, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件 '%s' v%s 已成功提交安装任务。", payload.PluginID, payload.Version)})
	}
}

// listInstancesHandler 返回所有已配置的插件实例列表。
func listInstancesHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instances, err := pluginManager.ListInstances()
		if err != nil {
			_ = c.Error(err)
			return
		}
		if instances == nil {
			instances = make([]domain.PluginInstance, 0)
		}
		c.JSON(http.StatusOK, gin.H{"data": instances})
	}
}

// deleteInstanceHandler 删除一个插件实例的配置。
func deleteInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.DeleteInstance(instanceID); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功删除。", instanceID)})
	}
}

// startInstanceHandler 启动一个已配置的插件实例。
func startInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.Start(instanceID); err != nil {
			_ = c.Error(fmt.Errorf("启动插件实例 '%s' 失败: %w", instanceID, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功提交启动任务。", instanceID)})
	}
}

// stopInstanceHandler 停止一个正在运行的插件实例。
func stopInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.Stop(instanceID); err != nil {
			_ = c.Error(fmt.Errorf("停止插件实例 '%s' 失败: %w", instanceID, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功停止。", instanceID)})
	}
}

// createInstanceHandler 创建一个新的插件实例配置。
func createInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	type createPayload struct {
		DisplayName string `json:"display_name" binding:"required"`
		PluginID    string `json:"plugin_id" binding:"required"`
		Version     string `json:"version" binding:"required"`
		BizName     string `json:"biz_name" binding:"required"`
	}
	return func(c *gin.Context) {
		var payload createPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		instanceID, err := pluginManager.CreateInstance(payload.DisplayName, payload.PluginID, payload.Version, payload.BizName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"message":     "插件实例创建成功",
			"instance_id": instanceID,
		})
	}
}
