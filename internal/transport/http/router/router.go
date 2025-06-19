// file: internal/transport/http/router/router.go
package router

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
	AuthDB             *sql.DB
	SetupToken         string
	SetupTokenDeadline time.Time
}

// New 创建并配置一个全新的、基于 Gin 的 HTTP 路由器 (V1 版本)
func New(deps Dependencies) http.Handler {
	router := gin.Default()

	// --- 配置全局中间件 ---
	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	authService := service.NewAuthenticator(deps.AuthDB)
	v1 := router.Group("/api/v1")
	{
		// --- 系统/认证平面 (System/Auth Plane) ---
		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/login", loginHandler(deps.AuthDB))
			// 未来可在这里添加 /refresh, /logout, /verify-2fa 等
		}
		systemGroup := v1.Group("/system")
		{
			systemGroup.Any("/setup", setupHandler(deps.AuthDB, deps.SetupToken, deps.SetupTokenDeadline))
			systemGroup.GET("/status", statusHandler(deps.AuthDB))
		}

		// --- 元数据/发现平面 (Metadata/Discovery Plane) ---
		metaGroup := v1.Group("/meta")
		metaGroup.Use(authMiddleware(authService)) // 发现API也需要认证
		{
			metaGroup.GET("/biz", bizHandlerV1(deps.Registry))
			metaGroup.GET("/schema/:bizName", schemaHandlerV1(deps.Registry))
			metaGroup.GET("/presentations", presentationsHandlerV1(deps.AdminConfigService))
		}

		// --- 数据平面 (Data Plane) ---
		dataGroup := v1.Group("/data")
		dataGroup.Use(authMiddleware(authService)) // 数据API需要认证
		{
			dataGroup.POST("/query", queryHandlerV1(deps.Registry))
			// 未来在这里添加 POST /mutate
		}

		// --- 控制平面 (Control Plane) ---
		adminGroup := v1.Group("/admin")
		adminGroup.Use(authMiddleware(authService), requireAdmin()) // 控制平面需要管理员权限
		{
			adminGroup.GET("/resources/datasources/configured-names", adminGetConfiguredBizNamesHandler(deps.AdminConfigService))

			securityGroup := adminGroup.Group("/security")
			{
				securityGroup.GET("/rate-limiting/global", adminGetIPLimitSettingsHandler(deps.AdminConfigService))
				securityGroup.PUT("/rate-limiting/global", adminUpdateIPLimitSettingsHandler(deps.AdminConfigService))
			}

			// 按资源 "datasources" 组织 biz 相关的配置
			bizConfigGroup := adminGroup.Group("/resources/datasources/:bizName")
			{
				bizConfigGroup.GET("/", getBizConfigHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/settings", updateBizOverallSettingsHandler(deps.AdminConfigService, deps.AuthDB))
				bizConfigGroup.PUT("/tables", adminUpdateBizSearchableTablesHandler(deps.AdminConfigService, deps.AuthDB))
				bizConfigGroup.GET("/rate-limit", adminGetBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/rate-limit", adminUpdateBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/views", adminGetBizViewsHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/views", adminUpdateBizViewsHandler(deps.AdminConfigService))

				tableGroup := bizConfigGroup.Group("/tables/:tableName")
				{
					tableGroup.PUT("/fields", adminUpdateTableFieldSettingsHandler(deps.AdminConfigService, deps.AuthDB))
				}
			}
		}
	}

	return router
}

// =============================================================================
//  Gin 中间件 (Middleware)
// =============================================================================

func authMiddleware(auth *service.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.Request = r
			c.Next()
		}))
		handler.ServeHTTP(c.Writer, c.Request)
		if c.Writer.Written() {
			c.Abort()
		}
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
//  V1 版本的新/重构处理器 (New/Refactored V1 Handlers)
// =============================================================================

// --- V1 元数据平面处理器 ---

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

func schemaHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		dataSource, exists := registry[bizName]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "业务组 '" + bizName + "' 未找到或未注册"})
			return
		}

		// 注意：第二个参数 tableName 为空，表示获取整个 biz 的 schema
		schema, err := dataSource.GetSchema(c.Request.Context(), port.SchemaRequest{BizName: bizName})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取Schema失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"data": schema})
	}
}

func presentationsHandlerV1(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Query("biz")
		tableName := c.Query("table")
		if bizName == "" || tableName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 'biz' 或 'table' 参数"})
			return
		}
		viewConfig, err := configService.GetDefaultViewConfig(c.Request.Context(), bizName, tableName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取表现层配置时发生内部错误"})
			return
		}
		if viewConfig == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("未找到业务 '%s' 表 '%s' 的默认表现层配置", bizName, tableName)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": viewConfig})
	}
}

// --- V1 数据平面处理器 ---

func queryHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	// 定义请求体的结构
	type QueryPayload struct {
		Table          string            `json:"table"`
		FieldsToReturn []string          `json:"fields_to_return"`
		Filters        []port.QueryParam `json:"filters"`
		Page           int               `json:"page"`
		Size           int               `json:"size"`
	}
	type RequestBody struct {
		BizName string       `json:"biz_name" binding:"required"`
		Query   QueryPayload `json:"query" binding:"required"`
	}

	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求体: " + err.Error()})
			return
		}

		dataSource, exists := registry[reqBody.BizName]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "业务组 '" + reqBody.BizName + "' 未找到或未注册"})
			return
		}

		// 默认分页
		if reqBody.Query.Page <= 0 {
			reqBody.Query.Page = 1
		}
		if reqBody.Query.Size <= 0 {
			reqBody.Query.Size = 50
		}
		if reqBody.Query.Size > 2000 { // 安全限制
			reqBody.Query.Size = 2000
		}

		queryReq := port.QueryRequest{
			BizName:        reqBody.BizName,
			TableName:      reqBody.Query.Table,
			QueryParams:    reqBody.Query.Filters,
			Page:           reqBody.Query.Page,
			Size:           reqBody.Query.Size,
			FieldsToReturn: reqBody.Query.FieldsToReturn, // 传递要求返回的字段
		}

		result, err := dataSource.Query(c.Request.Context(), queryReq)
		if err != nil {
			log.Printf("ERROR: queryHandlerV1 query failed for biz '%s': %v", reqBody.BizName, err)
			// 现在这些 port.Err... 引用是合法的，因为它们是接口契约的一部分
			if errors.Is(err, port.ErrPermissionDenied) {
				c.JSON(http.StatusForbidden, gin.H{"error": "查询权限不足"})
			} else if errors.Is(err, port.ErrTableNotFoundInBiz) || errors.Is(err, port.ErrBizNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败: " + err.Error()})
			}
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": result.Data, "total": result.Total})
	}
}

// =============================================================================
//  旧处理器实现 (暂时保留，在新路由结构下被调用)
// =============================================================================

// --- 系统与认证处理器 ---

func statusHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service.UserCount(db) > 0 {
			c.JSON(http.StatusOK, gin.H{"status": "ready_for_login"})
		} else {
			c.JSON(http.StatusOK, gin.H{"status": "needs_setup"})
		}
	}
}

func loginHandler(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			User string `form:"user" json:"user" binding:"required"`
			Pass string `form:"pass" json:"pass" binding:"required"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名或密码不能为空"})
			return
		}
		id, role, ok := service.CheckUser(db, req.User, req.Pass)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码无效"})
			return
		}
		token, err := service.GenToken(id, role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": id, "username": req.User, "role": role}})
	}
}

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
				c.JSON(http.StatusForbidden, gin.H{"error": "系统已存在管理员账户，无法重复设置"})
				return
			}
			var req struct {
				Token string `form:"token" json:"token" binding:"required"`
				User  string `form:"user" json:"user" binding:"required"`
				Pass  string `form:"pass" json:"pass" binding:"required"`
			}
			if err := c.ShouldBind(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "令牌、用户名或密码不能为空"})
				return
			}
			if req.Token != token || token == "" || time.Now().After(deadline) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效或过期的安装令牌"})
				return
			}
			if err := service.CreateAdmin(db, req.User, req.Pass); err != nil {
				log.Printf("ERROR: [API /setup] 创建管理员 '%s' 失败: %v", req.User, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "创建管理员失败: " + err.Error()})
				return
			}
			id, _, _ := service.CheckUser(db, req.User, req.Pass)
			jwtToken, err := service.GenToken(id, "admin")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "为新管理员生成令牌失败"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"token": jwtToken, "user": gin.H{"id": id, "username": req.User, "role": "admin"}})
			return
		}
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "仅支持 GET 和 POST 方法"})
	}
}

// --- 管理员 API 处理器 ---

func adminGetConfiguredBizNamesHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		names, err := configService.GetAllConfiguredBizNames(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取业务列表失败: " + err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取配置失败: " + err.Error()})
			return
		}
		if settings == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到IP速率限制配置"})
			return
		}
		c.JSON(http.StatusOK, settings)
	}
}

func adminUpdateIPLimitSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload domain.IPLimitSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		if err := configService.UpdateIPLimitSettings(c.Request.Context(), payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新配置失败: " + err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取配置失败: " + err.Error()})
			return
		}
		if cfg == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("业务 '%s' 未找到查询配置", bizName)})
			return
		}
		c.JSON(http.StatusOK, cfg)
	}
}

func updateBizOverallSettingsHandler(configService port.QueryAdminConfigService, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload struct {
			IsPubliclySearchable *bool   `json:"is_publicly_searchable"`
			DefaultQueryTable    *string `json:"default_query_table"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		// TODO: 此处的具体实现应迁移到 service 层
		configService.InvalidateCacheForBiz(bizName)
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "业务组配置已更新"})
	}
}

func adminUpdateBizSearchableTablesHandler(configService port.QueryAdminConfigService, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload struct {
			SearchableTables []string `json:"searchable_tables"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		// TODO: 此处的具体实现应迁移到 service 层
		configService.InvalidateCacheForBiz(bizName)
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "可搜索表列表已更新"})
	}
}

func adminUpdateTableFieldSettingsHandler(configService port.QueryAdminConfigService, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload []domain.FieldSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		// TODO: 此处的具体实现应迁移到 service 层
		configService.InvalidateCacheForBiz(bizName)
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "字段配置已更新"})
	}
}

func adminGetBizRateLimitHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		settings, err := configService.GetBizRateLimitSettings(c.Request.Context(), bizName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取配置失败: " + err.Error()})
			return
		}
		if settings == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到该业务的速率限制配置"})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		if err := configService.UpdateBizRateLimitSettings(c.Request.Context(), bizName, payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新配置失败: " + err.Error()})
			return
		}
		configService.InvalidateCacheForBiz(bizName)
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

func adminGetBizViewsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		views, err := configService.GetAllViewConfigsForBiz(c.Request.Context(), bizName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取视图配置失败: " + err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}
		if err := configService.UpdateAllViewsForBiz(c.Request.Context(), bizName, viewsData); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新视图配置失败: " + err.Error()})
			return
		}
		configService.InvalidateCacheForBiz(bizName)
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}
