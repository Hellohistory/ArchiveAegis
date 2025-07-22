// Package router file: internal/transport/http/router/router.go
package router

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/service/workflow"
	"ArchiveAegis/internal/transport/http/middleware"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// Dependencies 结构体现在注入新的 port.Executor 注册表
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

// New 创建并配置一个基于 Gin 的 HTTP 路由器
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

	apiV1 := router.Group("/api/v1")
	{
		// --- 系统/认证平面 ---
		authGroup := apiV1.Group("/auth")
		authGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			authGroup.POST("/login", loginHandler(deps.AuthDB))
		}

		systemGroup := apiV1.Group("/system")
		systemGroup.Use(WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			systemGroup.Any("/setup", setupHandler(deps.AuthDB, deps.SetupToken, deps.SetupTokenDeadline))
			systemGroup.GET("/status", statusHandler(deps.AuthDB))
		}

		// --- 元数据/发现平面 ---
		metaGroup := apiV1.Group("/meta")
		metaGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.LightweightChain))
		{
			metaGroup.GET("/biz", bizHandlerV1(deps.Registry))
			metaGroup.GET("/schema/:bizName", schemaHandlerV1(deps.Registry))
			metaGroup.GET("/presentations", presentationsHandlerV1(deps.AdminConfigService))
		}

		// --- 数据平面 ---
		dataGroup := apiV1.Group("/data")
		dataGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			// 保留旧的端点作为便利的别名
			dataGroup.POST("/query", queryHandlerV1(deps.Registry))
			dataGroup.POST("/mutate", mutateHandlerV1(deps.Registry))

			// 新增的统一执行端点
			dataGroup.POST("/execute", executeHandler(deps.Registry))
		}

		// --- 工作流操作平面 ---
		workflowGroup := apiV1.Group("/workflows")
		workflowGroup.Use(authMiddleware(authService), WrapNetHTTP(deps.RateLimiter.FullBusinessChain))
		{
			workflowGroup.POST("/:workflow_id/run", runWorkflowHandler(deps.WorkflowService))
		}

		// --- 控制平面 (Admin) ---
		adminGroup := apiV1.Group("/admin")
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

			// -- 工作流管理 ---
			adminWorkflowGroup := adminGroup.Group("/workflows")
			{
				adminWorkflowGroup.POST("/", createWorkflowHandler(deps.WorkflowService))
				adminWorkflowGroup.GET("/", listWorkflowsHandler(deps.WorkflowService))
				adminWorkflowGroup.GET("/:workflow_id", getWorkflowHandler(deps.WorkflowService))
				adminWorkflowGroup.PUT("/:workflow_id", updateWorkflowHandler(deps.WorkflowService))
				adminWorkflowGroup.DELETE("/:workflow_id", deleteWorkflowHandler(deps.WorkflowService))
			}
		}
	}

	return router
}

// =============================================================================
// Gin 中间件 (Middleware)
// =============================================================================

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
//  API 执行器辅助函数
// =============================================================================

// executeAndRespond 是一个高阶辅助函数，处理通用的执行和响应逻辑
func executeAndRespond(c *gin.Context, registry map[string]port.Executor, bizName string, reqPayload proto.Message, resPayload proto.Message) {
	executor, exists := registry[bizName]
	if !exists {
		_ = c.Error(port.ErrBizNotFound)
		return
	}

	packedPayload, err := anypb.New(reqPayload)
	if err != nil {
		_ = c.Error(fmt.Errorf("打包请求载荷失败: %w", err))
		return
	}

	envelope := &v1.RequestEnvelope{
		RequestId: uuid.New().String(),
		BizName:   bizName,
		Payload:   packedPayload,
	}

	resEnvelope, err := executor.Execute(c.Request.Context(), envelope)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if resEnvelope.Status.Code != int32(codes.OK) {
		_ = c.Error(status.Error(codes.Code(resEnvelope.Status.Code), resEnvelope.Status.Message))
		return
	}

	var responseData any
	if resEnvelope.Payload != nil {
		// 尝试解包到提供的 resPayload 结构体中
		if err := resEnvelope.Payload.UnmarshalTo(resPayload); err != nil {
			_ = c.Error(fmt.Errorf("解包响应载荷失败: %w", err))
			return
		}
		// 根据具体类型决定如何转换为JSON
		switch p := resPayload.(type) {
		case *v1.DataQueryResult:
			responseData = p.GetData().AsMap()
		case *v1.DataMutateResult:
			responseData = p.GetData().AsMap()
		default:
			// 对于像 SchemaResult 这样结构良好的消息，直接让Gin序列化
			responseData = p
		}
	} else {
		// 如果 payload 为空，但操作成功，返回一个成功的空对象
		responseData = gin.H{"success": true}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":   responseData,
		"source": executor.Type(),
	})
}

// =============================================================================
//  数据平面处理器
// =============================================================================

func queryHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
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
		queryStruct, err := structpb.NewStruct(reqBody.Query)
		if err != nil {
			_ = c.Error(fmt.Errorf("创建 query struct 失败: %w", err))
			return
		}
		reqPayload := &v1.DataQueryRequest{Query: queryStruct}
		resPayload := &v1.DataQueryResult{}
		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}

func mutateHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
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
		slog.Info("审计日志: 收到 Mutate 请求", "user_id", service.ClaimFrom(c.Request).ID, "biz_name", reqBody.BizName, "operation", reqBody.Operation)
		payloadStruct, err := structpb.NewStruct(reqBody.Payload)
		if err != nil {
			_ = c.Error(fmt.Errorf("创建 payload struct 失败: %w", err))
			return
		}
		reqPayload := &v1.DataMutateRequest{Operation: reqBody.Operation, Payload: payloadStruct}
		resPayload := &v1.DataMutateResult{}
		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}

// executeHandler 是统一执行器端点
func executeHandler(registry map[string]port.Executor) gin.HandlerFunc {
	type RequestBody struct {
		BizName string                 `json:"biz_name" binding:"required"`
		Command string                 `json:"command" binding:"required"`
		Payload map[string]interface{} `json:"payload"`
	}

	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}

		var reqPayload proto.Message
		var resPayload proto.Message

		switch reqBody.Command {
		case "DataQuery":
			reqPayload = &v1.DataQueryRequest{}
			resPayload = &v1.DataQueryResult{}
		case "DataMutate":
			reqPayload = &v1.DataMutateRequest{}
			resPayload = &v1.DataMutateResult{}
		case "GetSchema":
			reqPayload = &v1.GetSchemaRequest{}
			resPayload = &v1.SchemaResult{}
		case "TriggerBackup":
			reqPayload = &v1.TriggerBackupRequest{}
			resPayload = &v1.TriggerBackupResult{}
		default:
			_ = c.Error(status.Errorf(codes.Unimplemented, "不支持的命令: %s", reqBody.Command))
			return
		}

		if reqBody.Payload != nil {
			jsonBytes, err := json.Marshal(reqBody.Payload)
			if err != nil {
				_ = c.Error(fmt.Errorf("无法序列化载荷以进行映射: %w", err))
				return
			}

			if err := protojson.Unmarshal(jsonBytes, reqPayload); err != nil {
				_ = c.Error(fmt.Errorf("无法将载荷映射到命令 '%s' 的结构: %w", reqBody.Command, err))
				return
			}
		}

		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}

// =============================================================================
//  元数据平面处理器
// =============================================================================

func bizHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizNames := make([]string, 0, len(registry))
		for name := range registry {
			bizNames = append(bizNames, name)
		}
		sort.Strings(bizNames)
		c.JSON(http.StatusOK, gin.H{"data": bizNames})
	}
}

func schemaHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		reqPayload := &v1.GetSchemaRequest{TableName: c.Query("tableName")}
		resPayload := &v1.SchemaResult{}
		executeAndRespond(c, registry, bizName, reqPayload, resPayload)
	}
}

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

func listAvailablePluginsHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		availablePlugins := pluginManager.GetAvailablePlugins()
		if availablePlugins == nil {
			availablePlugins = make([]domain.PluginManifest, 0)
		}
		c.JSON(http.StatusOK, gin.H{"data": availablePlugins})
	}
}

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

func createInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		type createPayload struct {
			DisplayName string `json:"display_name" binding:"required"`
			PluginID    string `json:"plugin_id" binding:"required"`
			Version     string `json:"version" binding:"required"`
			BizName     string `json:"biz_name" binding:"required"`
		}
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

// =============================================================================
//  工作流 API 处理器
// =============================================================================

// runWorkflowHandler 触发一个工作流的执行
func runWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	type runPayload struct {
		InitialParams map[string]any `json:"initial_params"`
	}
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var payload runPayload
		if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
			_ = c.Error(err)
			return
		}
		if payload.InitialParams == nil {
			payload.InitialParams = make(map[string]any)
		}

		finalAction, err := workflowService.RunWorkflow(c.Request.Context(), workflowID, payload.InitialParams)
		if err != nil {
			_ = c.Error(err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":       "completed",
			"workflow_id":  workflowID,
			"final_action": finalAction,
		})
	}
}

// createWorkflowHandler 创建一个新的工作流定义
func createWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}

		createdWorkflow, err := workflowService.CreateWorkflow(c.Request.Context(), payload.Workflow, payload.Nodes, payload.Edges)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, createdWorkflow)
	}
}

// listWorkflowsHandler 列出所有工作流
func listWorkflowsHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflows, err := workflowService.ListWorkflows(c.Request.Context())
		if err != nil {
			_ = c.Error(err)
			return
		}
		if workflows == nil {
			workflows = []domain.Workflow{}
		}
		c.JSON(http.StatusOK, workflows)
	}
}

// getWorkflowHandler 获取单个工作流的完整定义
func getWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		getWorkflow, err := workflowService.GetWorkflow(c.Request.Context(), workflowID)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, getWorkflow)
	}
}

// updateWorkflowHandler 更新一个工作流的定义
func updateWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		payload.Workflow.ID = workflowID

		updatedWorkflow, err := workflowService.UpdateWorkflow(c.Request.Context(), payload.Workflow, payload.Nodes, payload.Edges)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedWorkflow)
	}
}

// deleteWorkflowHandler 删除一个工作流
func deleteWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		if err := workflowService.DeleteWorkflow(c.Request.Context(), workflowID); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
