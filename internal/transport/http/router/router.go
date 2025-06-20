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
			dataGroup.POST("/mutate", mutateHandlerV1(deps.Registry))
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
				// ✅ 重构: 移除了不再需要的 deps.AuthDB 依赖
				bizConfigGroup.PUT("/settings", updateBizOverallSettingsHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/tables", adminUpdateBizSearchableTablesHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/rate-limit", adminGetBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/rate-limit", adminUpdateBizRateLimitHandler(deps.AdminConfigService))
				bizConfigGroup.GET("/views", adminGetBizViewsHandler(deps.AdminConfigService))
				bizConfigGroup.PUT("/views", adminUpdateBizViewsHandler(deps.AdminConfigService))

				tableGroup := bizConfigGroup.Group("/tables/:tableName")
				{
					tableGroup.PUT("/fields", adminUpdateTableFieldSettingsHandler(deps.AdminConfigService))
					tableGroup.PUT("/permissions", adminUpdateTablePermissionsHandler(deps.AdminConfigService))
				}
			}
		}
	}

	return router
}

// =============================================================================
//  Gin 中间件 (Middleware)
// =============================================================================

// authMiddleware 是一个将 service.Authenticator 集成到 gin 流程的中间件
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

// requireAdmin 是一个确保只有管理员角色才能访问的中间件
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
			c.JSON(http.StatusNotFound, gin.H{"error": "业务组 '" + bizName + "' 未找到或未注册"})
			return
		}

		schema, err := dataSource.GetSchema(c.Request.Context(), port.SchemaRequest{BizName: bizName})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "获取Schema失败: " + err.Error()})
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

// queryHandlerV1 处理统一的数据查询请求
func queryHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
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

		if reqBody.Query.Page <= 0 {
			reqBody.Query.Page = 1
		}
		if reqBody.Query.Size <= 0 {
			reqBody.Query.Size = 50
		}
		if reqBody.Query.Size > 2000 {
			reqBody.Query.Size = 2000
		}

		queryReq := port.QueryRequest{
			BizName:        reqBody.BizName,
			TableName:      reqBody.Query.Table,
			QueryParams:    reqBody.Query.Filters,
			Page:           reqBody.Query.Page,
			Size:           reqBody.Query.Size,
			FieldsToReturn: reqBody.Query.FieldsToReturn,
		}

		result, err := dataSource.Query(c.Request.Context(), queryReq)
		if err != nil {
			log.Printf("ERROR: queryHandlerV1 query failed for biz '%s': %v", reqBody.BizName, err)
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

// mutateHandlerV1 处理统一的数据写入（增删改）请求
func mutateHandlerV1(registry map[string]port.DataSource) gin.HandlerFunc {
	type CreatePayload struct {
		Table string                 `json:"table" binding:"required"`
		Data  map[string]interface{} `json:"data" binding:"required"`
	}
	type UpdatePayload struct {
		Table   string                 `json:"table" binding:"required"`
		Data    map[string]interface{} `json:"data" binding:"required"`
		Filters []port.QueryParam      `json:"filters" binding:"required"`
	}
	type DeletePayload struct {
		Table   string            `json:"table" binding:"required"`
		Filters []port.QueryParam `json:"filters" binding:"required"`
	}

	type RequestBody struct {
		BizName    string         `json:"biz_name" binding:"required"`
		Operation  string         `json:"operation" binding:"required,oneof=create update delete"`
		CreateData *CreatePayload `json:"create_data,omitempty"`
		UpdateData *UpdatePayload `json:"update_data,omitempty"`
		DeleteData *DeletePayload `json:"delete_data,omitempty"`
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

		var goReq port.MutateRequest
		goReq.BizName = reqBody.BizName

		switch reqBody.Operation {
		case "create":
			if reqBody.CreateData == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "create操作缺少'create_data'字段"})
				return
			}
			goReq.CreateOp = &port.CreateOperation{
				TableName: reqBody.CreateData.Table,
				Data:      reqBody.CreateData.Data,
			}
		case "update":
			if reqBody.UpdateData == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "update操作缺少'update_data'字段"})
				return
			}
			goReq.UpdateOp = &port.UpdateOperation{
				TableName: reqBody.UpdateData.Table,
				Data:      reqBody.UpdateData.Data,
				Filters:   reqBody.UpdateData.Filters,
			}
		case "delete":
			if reqBody.DeleteData == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "delete操作缺少'delete_data'字段"})
				return
			}
			goReq.DeleteOp = &port.DeleteOperation{
				TableName: reqBody.DeleteData.Table,
				Filters:   reqBody.DeleteData.Filters,
			}
		}

		claims := service.ClaimFrom(c.Request)
		log.Printf("审计日志: 用户ID '%d' 正在尝试对业务 '%s' 执行 '%s' 操作。", claims.ID, reqBody.BizName, reqBody.Operation)

		result, err := dataSource.Mutate(c.Request.Context(), goReq)
		if err != nil {
			log.Printf("ERROR: mutateHandlerV1 failed for biz '%s': %v", reqBody.BizName, err)
			if errors.Is(err, port.ErrPermissionDenied) {
				c.JSON(http.StatusForbidden, gin.H{"error": "写操作权限不足"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "执行写操作失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"data": result})
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

// =============================================================================
//  管理员 API 处理器
// =============================================================================

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
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// updateBizOverallSettingsHandler 更新业务组的总体设置
func updateBizOverallSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		// 使用 domain.BizOverallSettings 来绑定 payload，支持部分更新
		var payload domain.BizOverallSettings
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}

		if err := configService.UpdateBizOverallSettings(c.Request.Context(), bizName, payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新业务组设置失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "业务组配置已更新"})
	}
}

// adminUpdateBizSearchableTablesHandler 更新业务组可搜索的数据表列表
func adminUpdateBizSearchableTablesHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		var payload struct {
			SearchableTables []string `json:"searchable_tables"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}

		if err := configService.UpdateBizSearchableTables(c.Request.Context(), bizName, payload.SearchableTables); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新可搜索表列表失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "可搜索表列表已更新"})
	}
}

// adminUpdateTableFieldSettingsHandler 更新数据表的字段设置
func adminUpdateTableFieldSettingsHandler(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		tableName := c.Param("tableName")
		var payload []domain.FieldSetting
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}

		if err := configService.UpdateTableFieldSettings(c.Request.Context(), bizName, tableName, payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新字段配置失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "字段配置已更新"})
	}
}

// adminUpdateTablePermissionsHandler 更新数据表的写入权限
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的JSON请求体: " + err.Error()})
			return
		}

		perms := domain.TableConfig{
			AllowCreate: payload.AllowCreate,
			AllowUpdate: payload.AllowUpdate,
			AllowDelete: payload.AllowDelete,
		}

		if err := configService.UpdateTableWritePermissions(c.Request.Context(), bizName, tableName, perms); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新权限失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "表的写权限已成功更新。"})
	}
}
