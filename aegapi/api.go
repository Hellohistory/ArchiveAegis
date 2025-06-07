// Package aegapi aegapi/api.go
// Package aegapi — （Setup / Login / 业务 / 管理）
package aegapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ArchiveAegis/aegauth"
	"ArchiveAegis/aegdb"
	"ArchiveAegis/aegmetric"

	"github.com/NYTimes/gziphandler"
)

/*
============================================================
  全局变量 / 帮助函数
============================================================
*/

var (
	// 首次安装随机令牌
	setupToken string
	// 令牌过期时间
	setupTokenDead time.Time
)

// SetSetupToken 由 main.go 在启动时调用，写入安装令牌
func SetSetupToken(tok string, dead time.Time) {
	setupToken, setupTokenDead = tok, dead
}

// pick 从 query map 中尝试读取 k，对应值为 []string，如果没找到，则尝试读取 "k[]"
func pick(v map[string][]string, k string) []string {
	if arr := v[k]; len(arr) > 0 {
		return arr
	}
	return v[k+"[]"]
}

// respond 统一 JSON 输出 (无CORS头部)
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}

// errResp 带 status code 的错误输出 (无CORS头部)
func errResp(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

/*
============================================================
  HTTP Handler 结构与路由定义
============================================================
*/

// ReturnableFieldInfo 定义了 /columns 接口返回的字段信息结构
type ReturnableFieldInfo struct {
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	IsSearchable bool   `json:"is_searchable"`
	IsReturnable bool   `json:"is_returnable"`
	DataType     string `json:"dataType"`
}

// NewRouter 为API路由添加/api前缀，并注册新的状态检查接口。
func NewRouter(mgr *aegdb.Manager, sysDB *sql.DB, configService aegdb.QueryAdminConfigService) http.Handler {
	if sysDB == nil {
		log.Fatal("严重错误 (aegapi.NewRouter): sysDB (用户数据库) 连接为空！ 应用无法启动。")
	}
	authenticator := aegauth.NewAuthenticator(sysDB)
	apiMux := http.NewServeMux()

	// 认证相关API
	apiMux.HandleFunc("/api/auth/status", authStatusHandler(sysDB))
	apiMux.HandleFunc("/api/setup", setupHandler(sysDB))
	apiMux.HandleFunc("/api/login", loginHandler(sysDB))

	// 公开业务查询相关API
	apiMux.HandleFunc("/api/biz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		respond(w, mgr.Summary())
	})
	apiMux.HandleFunc("/api/tables", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		bizName := r.URL.Query().Get("biz")
		if bizName == "" {
			errResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 参数")
			return
		}
		physicalTables := mgr.Tables(bizName)
		if physicalTables == nil {
			physicalTables = []string{}
		}
		respond(w, physicalTables)
	})
	apiMux.HandleFunc("/api/columns", columnsHandler(configService))
	apiMux.HandleFunc("/api/search", searchHandler(mgr))

	apiMux.HandleFunc("/api/view/config", viewConfigHandler(configService))

	// 管理员API
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/api/admin/config/biz/", adminConfigBizDispatcher(configService, sysDB, mgr))
	apiMux.Handle("/api/admin/", aegauth.RequireAdmin(adminMux))

	root := http.NewServeMux()
	root.Handle("/api/", apiMux)

	return gziphandler.GzipHandler(authenticator.Middleware(root))
}

/*
============================================================
                   系统状态检查 Handler
============================================================
*/

// authStatusHandler 检查系统中是否存在任何用户账户，并返回相应的状态。
// 这个接口用于帮助前端决定初始页面的跳转方向（设置页或登录页）。
func authStatusHandler(sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		if aegauth.UserCount(sysDB) > 0 {
			respond(w, map[string]string{"status": "ready_for_login"})
		} else {
			respond(w, map[string]string{"status": "needs_setup"})
		}
	}
}

/*
============================================================
   /view/config Handler
============================================================
*/

// viewConfigHandler 处理获取指定表默认视图配置的请求
func viewConfigHandler(configService aegdb.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}

		q := r.URL.Query()
		bizName := q.Get("biz")
		tableName := q.Get("table")

		if bizName == "" || tableName == "" {
			errResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 或 'table' (表名) 参数")
			return
		}

		viewConfig, err := configService.GetDefaultViewConfig(r.Context(), bizName, tableName)
		if err != nil {
			// service 层返回的错误，500内部错误
			log.Printf("错误: [API /view/config] 调用 configService.GetDefaultViewConfig 失败 (biz: '%s', table: '%s'): %v", bizName, tableName, err)
			errResp(w, http.StatusInternalServerError, "获取视图配置时发生内部错误")
			return
		}

		// 如果 viewConfig 为 nil，表示没有找到对应的默认视图配置。
		// 这不是一个服务器错误，而是一个 "资源未找到" 的情况，返回 404 。
		if viewConfig == nil {
			errResp(w, http.StatusNotFound, fmt.Sprintf("未找到业务 '%s' 表 '%s' 的默认视图配置", bizName, tableName))
			return
		}

		// 成功找到配置，返回 JSON
		respond(w, viewConfig)
	}
}

/*
============================================================

	Setup 和 Login Handlers

============================================================
*/
func setupHandler(sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Get("ping") == "1" {
			if aegauth.UserCount(sysDB) > 0 {
				errResp(w, http.StatusForbidden, "系统已安装，无法获取安装令牌。")
				return
			}
			respond(w, map[string]string{"token": setupToken})
			return
		}

		if r.Method == http.MethodPost {
			if aegauth.UserCount(sysDB) > 0 {
				errResp(w, http.StatusForbidden, "系统已存在管理员账户，无法重复设置。")
				return
			}
			if err := r.ParseForm(); err != nil {
				errResp(w, http.StatusBadRequest, "无法解析表单数据: "+err.Error())
				return
			}
			if r.FormValue("token") != setupToken || setupToken == "" || time.Now().After(setupTokenDead) {
				errResp(w, http.StatusBadRequest, "无效或过期的安装令牌")
				return
			}
			user := strings.TrimSpace(r.FormValue("user"))
			pass := r.FormValue("pass")
			if user == "" || pass == "" {
				errResp(w, http.StatusBadRequest, "用户名或密码不能为空")
				return
			}

			if err := aegauth.CreateAdmin(sysDB, user, pass); err != nil {
				log.Printf("错误: [API /setup] 创建管理员 '%s' 失败: %v", user, err)
				errResp(w, http.StatusInternalServerError, "创建管理员失败: "+err.Error())
				return
			}
			setupToken = ""

			userID, _, ok := aegauth.CheckUser(sysDB, user, pass)
			if !ok {
				log.Printf("严重错误: [API /setup] 刚创建的管理员 '%s' 无法校验以生成Token。", user)
				errResp(w, http.StatusInternalServerError, "无法为新管理员生成令牌")
				return
			}

			jwtToken, err := aegauth.GenToken(userID, "admin")
			if err != nil {
				log.Printf("错误: [API /setup] 为管理员 '%s' (ID: %d) 生成JWT失败: %v", user, userID, err)
				errResp(w, http.StatusInternalServerError, "生成JWT失败: "+err.Error())
				return
			}
			log.Printf("信息: [API /setup] 管理员 '%s' (ID: %d) 创建成功。", user, userID)
			responsePayload := map[string]interface{}{
				"token": jwtToken,
				"user":  map[string]interface{}{"id": userID, "username": user, "role": "admin"},
			}
			respond(w, responsePayload)
			return
		}

		http.NotFound(w, r)
	}
}

func loginHandler(sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			errResp(w, http.StatusMethodNotAllowed, "仅支持POST方法")
			return
		}
		if err := r.ParseForm(); err != nil {
			errResp(w, http.StatusBadRequest, "无法解析表单数据: "+err.Error())
			return
		}
		user := strings.TrimSpace(r.FormValue("user"))
		pass := r.FormValue("pass")

		id, _, ok := aegauth.CheckUser(sysDB, user, pass)
		if !ok {
			errResp(w, http.StatusUnauthorized, "用户名或密码无效")
			return
		}
		dbUsername, dbRole, _ := aegauth.GetUserById(sysDB, id)

		jwtToken, err := aegauth.GenToken(id, dbRole)
		if err != nil {
			log.Printf("错误: [API /login] 为用户 '%s' (ID: %d, Role: %s) 生成JWT失败: %v", dbUsername, id, dbRole, err)
			errResp(w, http.StatusInternalServerError, "生成JWT失败: "+err.Error())
			return
		}
		responsePayload := map[string]interface{}{
			"token": jwtToken,
			"user": map[string]interface{}{
				"id":       id,
				"username": dbUsername,
				"role":     dbRole,
			},
		}
		respond(w, responsePayload)
	}
}

/*
============================================================

	/columns Handler

============================================================
*/
func columnsHandler(configService aegdb.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		bizName := r.URL.Query().Get("biz")
		tableName := r.URL.Query().Get("table")

		if bizName == "" || tableName == "" {
			errResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 或 'table' (表名) 参数")
			return
		}

		bizConfig, err := configService.GetBizQueryConfig(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [API /columns] 获取业务 '%s' 配置失败: %v", bizName, err)
			errResp(w, http.StatusInternalServerError, "获取业务配置时发生内部错误")
			return
		}
		if bizConfig == nil {
			log.Printf("信息: [API /columns] 业务 '%s' 未找到查询配置。", bizName)
			errResp(w, http.StatusNotFound, fmt.Sprintf("业务 '%s' 未配置查询规则", bizName))
			return
		}
		if !bizConfig.IsPubliclySearchable {
			// 检查是否有认证用户（管理员）可以绕过公开查询限制
			claims := aegauth.ClaimFrom(r)
			if claims == nil || claims.Role != "admin" { // 假设只有管理员可以查看非公开业务组的列信息
				log.Printf("信息: [API /columns] 业务 '%s' 配置为不可公开查询，且访问者非管理员。", bizName)
				errResp(w, http.StatusForbidden, fmt.Sprintf("业务 '%s' 不允许查询", bizName))
				return
			}
			log.Printf("信息: [API /columns] 管理员访问业务 '%s' (配置为不可公开查询) 的列信息。", bizName)
		}

		tableConfig, tableExists := bizConfig.Tables[tableName]
		if !tableExists {
			log.Printf("信息: [API /columns] 表 '%s' (业务 '%s') 在查询配置中未定义。", tableName, bizName)
			errResp(w, http.StatusNotFound, fmt.Sprintf("表 '%s' 在业务 '%s' 中未配置查询规则", tableName, bizName))
			return
		}

		var allConfiguredFields []ReturnableFieldInfo // 存储所有配置过的字段信息

		// 为了保证返回顺序的稳定性，我们按字段名排序
		fieldNamesFromConfig := make([]string, 0, len(tableConfig.Fields))
		for fn := range tableConfig.Fields {
			fieldNamesFromConfig = append(fieldNamesFromConfig, fn)
		}
		sort.Strings(fieldNamesFromConfig)

		// 遍历排序后的字段名，并填充新的结构体
		for _, fieldName := range fieldNamesFromConfig {
			setting := tableConfig.Fields[fieldName] // setting 是 aegdb.FieldSetting

			// 获取数据类型，如果未配置，默认为 "string"
			dataType := setting.DataType
			if dataType == "" {
				dataType = "string"
			}

			// 填充我们新的、信息更丰富的结构体
			allConfiguredFields = append(allConfiguredFields, ReturnableFieldInfo{
				Name:         setting.FieldName, // 直接使用字段名，不再有别名逻辑
				OriginalName: setting.FieldName,
				IsSearchable: setting.IsSearchable,
				IsReturnable: setting.IsReturnable,
				DataType:     dataType,
			})
		}

		if allConfiguredFields == nil {
			allConfiguredFields = []ReturnableFieldInfo{} // 确保返回空数组而不是null
		}
		respond(w, allConfiguredFields)
	}
}

/*
============================================================

	Search Handler

============================================================
*/
func searchHandler(mgr *aegdb.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			errResp(w, http.StatusMethodNotAllowed, "仅支持GET方法进行搜索")
			return
		}
		aegmetric.TotalReq.Inc()

		q := r.URL.Query()
		bizName := q.Get("biz")
		tableName := q.Get("table") // tableName 在此接口中是可选的，mgr.Query 会处理

		if bizName == "" {
			errResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 参数")
			aegmetric.FailReq.Inc()
			return
		}

		// 权限检查：业务组是否允许查询 (与 /columns 类似)
		fields := pick(q, "fields")
		values := pick(q, "values")
		fuzzyStr := pick(q, "fuzzy")
		logics := pick(q, "logic")

		if len(fields) > 0 && (len(fields) != len(values) || len(fields) != len(fuzzyStr)) {
			errResp(w, http.StatusBadRequest, "当提供 'fields' 时, 'values' 和 'fuzzy' 参数的个数必须与其一致")
			aegmetric.FailReq.Inc()
			return
		}
		if len(fields) > 1 && len(logics) != len(fields)-1 {
			errResp(w, http.StatusBadRequest, "当查询条件大于1个时, 'logic' 参数的个数应为 'fields' 个数减 1")
			aegmetric.FailReq.Inc()
			return
		}

		var queryParams []aegdb.Param
		for i := range fields {
			isFuzzy, errConv := strconv.ParseBool(fuzzyStr[i])
			if errConv != nil {
				isFuzzy = false // 默认非模糊查询
				log.Printf("警告: [API /search] 无效的 'fuzzy[%d]' 参数值 '%s' (业务 '%s')，已默认为 false。", i, fuzzyStr[i], bizName)
			}
			param := aegdb.Param{
				Field: fields[i], Value: values[i], Fuzzy: isFuzzy,
			}
			if i < len(logics) {
				param.Logic = strings.ToUpper(logics[i])
				if param.Logic != "AND" && param.Logic != "OR" {
					errResp(w, http.StatusBadRequest, fmt.Sprintf("无效的逻辑操作符: '%s' (在第 %d 个条件后)", param.Logic, i+1))
					aegmetric.FailReq.Inc()
					return
				}
			} else if len(fields) > 1 && i < len(fields)-1 { // 确保最后一个条件前有logic
				errResp(w, http.StatusBadRequest, fmt.Sprintf("第 %d 个查询条件后缺少逻辑操作符 'logic'", i+1))
				aegmetric.FailReq.Inc()
				return
			}
			queryParams = append(queryParams, param)
		}

		pageStr := q.Get("page")
		page, _ := strconv.Atoi(pageStr) // 忽略错误，默认为0或1
		if page < 1 {
			page = 1 // 默认第一页
		}
		sizeStr := q.Get("size")
		size, _ := strconv.Atoi(sizeStr) // 忽略错误，默认为0或配置值
		if size < 1 {
			size = 50 // 默认每页50条
		} else if size > 2000 { // 最大页大小限制
			log.Printf("警告: [API /search] 请求的页大小 %d (业务 '%s') 超出最大限制 2000，已调整为 2000。", size, bizName)
			size = 2000
		}

		results, err := mgr.Query(r.Context(), bizName, tableName, queryParams, page, size)
		if err != nil {
			aegmetric.FailReq.Inc()
			// 检查错误类型，如果是权限错误，返回403，否则500
			if errors.Is(err, aegdb.ErrPermissionDenied) {
				log.Printf("信息: [API /search] 业务 '%s' 表 '%s' 查询权限不足: %v", bizName, tableName, err)
				errResp(w, http.StatusForbidden, "查询权限不足或业务/表不可查询")
			} else if errors.Is(err, aegdb.ErrBizNotFound) || errors.Is(err, aegdb.ErrTableNotFoundInBiz) {
				log.Printf("信息: [API /search] 业务 '%s' 或表 '%s' 未找到: %v", bizName, tableName, err)
				errResp(w, http.StatusNotFound, "业务组或表未找到")
			} else {
				log.Printf("错误: [API /search] 调用 mgr.Query 失败 (biz: '%s', table: '%s'): %v", bizName, tableName, err)
				errResp(w, http.StatusInternalServerError, "查询处理过程中发生错误")
			}
			return
		}
		if results == nil {
			results = []map[string]any{} // 确保返回空数组而不是null
		}
		respond(w, results)
	}
}

/*
============================================================
  Admin API Dispatcher 和 Handlers
============================================================
*/

func adminGetTablePhysicalColumnsHandler(mgr *aegdb.Manager, bizName string, tableName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 此接口为管理员接口，已通过 aegauth.RequireAdmin 保护
		physicalCols := mgr.PhysicalColumns(bizName, tableName)
		if physicalCols == nil {
			log.Printf("警告: [Admin API /physical-columns] 业务 '%s' - 表 '%s': 未从Manager获取到物理列信息。", bizName, tableName)
			respond(w, []string{}) // 返回空数组
			return
		}
		log.Printf("信息: [Admin API /physical-columns] 返回业务 '%s' - 表 '%s' 的物理列: %d 个。", bizName, tableName, len(physicalCols))
		respond(w, physicalCols)
	}
}

// adminConfigBizDispatcher 调度 /api/admin/config/biz/ 下的请求
func adminConfigBizDispatcher(configService aegdb.QueryAdminConfigService, sysDB *sql.DB, mgr *aegdb.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fullPath := r.URL.Path

		basePath := "/api/admin/config/biz/"
		trimmedPath := strings.TrimPrefix(fullPath, basePath)
		if strings.HasSuffix(trimmedPath, "/") && len(trimmedPath) > 1 {
			trimmedPath = trimmedPath[:len(trimmedPath)-1]
		}
		parts := strings.Split(trimmedPath, "/")

		log.Printf("调试: [Admin Dispatcher] Path: %s, Trimmed: %s, Parts: %v, Method: %s", fullPath, trimmedPath, parts, r.Method)

		if len(parts) > 0 && parts[0] != "" { // parts[0] 应该是 bizName
			bizName := parts[0]

			if r.Method == http.MethodGet && len(parts) == 1 { // GET /api/admin/config/biz/{bizName}
				adminGetBizConfigHandler(configService, bizName)(w, r)
				return
			}

			if len(parts) == 2 && parts[1] == "views" {
				if r.Method == http.MethodGet { // GET /api/admin/config/biz/{bizName}/views
					adminGetBizViewsHandler(configService, bizName)(w, r)
					return
				}
				if r.Method == http.MethodPut { // PUT /api/admin/config/biz/{bizName}/views
					adminUpdateBizViewsHandler(configService, bizName)(w, r)
					return
				}
			}

			if r.Method == http.MethodPut && len(parts) == 2 && parts[1] == "settings" { // PUT /api/admin/config/biz/{bizName}/settings
				adminUpdateBizOverallSettingsHandler(configService, sysDB, bizName)(w, r)
				return
			}
			if r.Method == http.MethodPut && len(parts) == 2 && parts[1] == "tables" { // PUT /api/admin/config/biz/{bizName}/tables
				adminUpdateBizSearchableTablesHandler(configService, sysDB, bizName)(w, r)
				return
			}

			// 针对特定表的操作: /api/admin/config/biz/{bizName}/tables/{tableName}/...
			if len(parts) >= 3 && parts[1] == "tables" {
				tableName := parts[2]
				if tableName == "" {
					http.NotFound(w, r)
					return
				}

				if r.Method == http.MethodPut && len(parts) == 4 && parts[3] == "fields" { // PUT /api/admin/config/biz/{bizName}/tables/{tableName}/fields
					adminUpdateTableFieldSettingsHandler(configService, sysDB, bizName, tableName)(w, r)
					return
				}
				if r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "physical-columns" { // GET /api/admin/config/biz/{bizName}/tables/{tableName}/physical-columns
					adminGetTablePhysicalColumnsHandler(mgr, bizName, tableName)(w, r)
					return
				}
			}
		}

		log.Printf("调试: [Admin Dispatcher] 未找到匹配的 Admin API 处理器 for path: '%s'", fullPath)
		http.NotFound(w, r)
	}
}

// --- 具体 Admin API Handlers ---

// adminGetBizConfigHandler: GET /admin/config/biz/{bizName}
func adminGetBizConfigHandler(configService aegdb.QueryAdminConfigService, bizName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := configService.GetBizQueryConfig(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [Admin API GET /biz/%s] 获取配置失败: %v", bizName, err)
			errResp(w, http.StatusInternalServerError, fmt.Sprintf("获取业务 '%s' 配置失败: %v", bizName, err))
			return
		}
		if cfg == nil { // GetBizQueryConfig 在未找到时应该返回 nil, nil (而不是sql.ErrNoRows)
			errResp(w, http.StatusNotFound, fmt.Sprintf("业务 '%s' 未找到查询配置", bizName))
			return
		}
		respond(w, cfg)
	}
}

// adminUpdateBizOverallSettingsHandler: PUT /admin/config/biz/{bizName}/settings
func adminUpdateBizOverallSettingsHandler(configService aegdb.QueryAdminConfigService, sysDB *sql.DB, bizName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			IsPubliclySearchable *bool   `json:"is_publicly_searchable"` // 指针用于区分 "未提供" 和 "提供false"
			DefaultQueryTable    *string `json:"default_query_table"`    // 指针用于区分 "未提供" 和 "提供空字符串"
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			errResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}
		defer r.Body.Close() //确保关闭请求体

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/settings] 开始事务失败: %v", bizName, errTx)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}
		defer func() {
			if p := recover(); p != nil {
				_ = tx.Rollback()
				panic(p)
			} else if err != nil {
				log.Printf("信息: [Admin API PUT /biz/%s/settings] 因错误回滚事务: %v", bizName, err)
				_ = tx.Rollback()
			} else {
				err = tx.Commit()
				if err != nil {
					log.Printf("错误: [Admin API PUT /biz/%s/settings] 提交事务失败: %v", bizName, err)
					// 此时 err 会被外部的错误处理捕获（如果适用）或仅日志记录
				} else {
					log.Printf("信息: [Admin API PUT /biz/%s/settings] 事务提交成功。", bizName)
				}
			}
		}()

		var currentIsPubliclySearchable bool = true
		var currentDefaultTable sql.NullString

		dbErr := tx.QueryRowContext(r.Context(), "SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings WHERE biz_name = ?", bizName).Scan(&currentIsPubliclySearchable, &currentDefaultTable)
		if dbErr != nil && !errors.Is(dbErr, sql.ErrNoRows) {
			err = fmt.Errorf("查询现有业务配置失败: %w", dbErr)
			log.Printf("错误: [Admin API PUT /biz/%s/settings] %v", bizName, err)
			errResp(w, http.StatusInternalServerError, "数据库查询操作失败")
			return
		}

		finalIsPubliclySearchable := currentIsPubliclySearchable
		if payload.IsPubliclySearchable != nil {
			finalIsPubliclySearchable = *payload.IsPubliclySearchable
		}

		finalDefaultQueryTable := currentDefaultTable
		if payload.DefaultQueryTable != nil {
			if *payload.DefaultQueryTable == "" { // 空字符串表示清除默认表
				finalDefaultQueryTable = sql.NullString{Valid: false}
			} else {
				finalDefaultQueryTable = sql.NullString{String: *payload.DefaultQueryTable, Valid: true}
			}
		}

		stmt, errStmt := tx.PrepareContext(r.Context(), `
            INSERT INTO biz_overall_settings (biz_name, is_publicly_searchable, default_query_table)
            VALUES (?, ?, ?)
            ON CONFLICT(biz_name) DO UPDATE SET
               is_publicly_searchable = excluded.is_publicly_searchable,
               default_query_table = excluded.default_query_table;`)
		if errStmt != nil {
			err = fmt.Errorf("准备SQL语句失败: %w", errStmt)
			log.Printf("错误: [Admin API PUT /biz/%s/settings] %v", bizName, err)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}
		defer stmt.Close()

		var defaultTableForDB interface{}
		if finalDefaultQueryTable.Valid {
			defaultTableForDB = finalDefaultQueryTable.String
		} else {
			defaultTableForDB = nil // SQL NULL
		}

		if _, errExec := stmt.ExecContext(r.Context(), bizName, finalIsPubliclySearchable, defaultTableForDB); errExec != nil {
			err = fmt.Errorf("更新配置失败: %w", errExec)
			log.Printf("错误: [Admin API PUT /biz/%s/settings] %v", bizName, err)
			errResp(w, http.StatusInternalServerError, "更新业务组总体配置失败")
			return
		}

		// 若所有操作（包括commit）都成功 (外部err为nil)
		if err == nil { // 确保是在事务成功后才使缓存失效
			configService.InvalidateCacheForBiz(bizName) // 使缓存失效
			log.Printf("信息: [Admin API] 业务组 '%s' 的总体配置已更新。", bizName)
			respond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 总体配置已更新", bizName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/settings] 事务处理结束时存在错误，可能未成功更新: %v", bizName, err)
		}
	}
}

// adminUpdateBizSearchableTablesHandler: PUT /admin/config/biz/{bizName}/tables
func adminUpdateBizSearchableTablesHandler(configService aegdb.QueryAdminConfigService, sysDB *sql.DB, bizName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			SearchableTables []string `json:"searchable_tables"` // 期望一个表名数组
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			errResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}
		defer r.Body.Close()

		if payload.SearchableTables == nil { // 如果json中该字段未提供或为null，视为空数组
			payload.SearchableTables = []string{}
		}

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/tables] 开始事务失败: %v", bizName, errTx)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}
		defer func() {
			if p := recover(); p != nil {
				_ = tx.Rollback()
				panic(p)
			} else if err != nil {
				_ = tx.Rollback()
			} else {
				err = tx.Commit()
				if err != nil {
					log.Printf("错误: [Admin API PUT /biz/%s/tables] 提交事务失败: %v", bizName, err)
				} else {
					log.Printf("信息: [Admin API PUT /biz/%s/tables] 事务提交成功。", bizName)
				}
			}
		}()

		// 1. 删除该业务组所有旧的可搜索表配置
		if _, errDel := tx.ExecContext(r.Context(), "DELETE FROM biz_searchable_tables WHERE biz_name = ?", bizName); errDel != nil {
			err = fmt.Errorf("清除旧可搜索表配置失败: %w", errDel)
			log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}

		// 2. 插入新的可搜索表配置 (如果列表非空)
		if len(payload.SearchableTables) > 0 {
			stmt, errPrep := tx.PrepareContext(r.Context(), "INSERT INTO biz_searchable_tables (biz_name, table_name) VALUES (?, ?)")
			if errPrep != nil {
				err = fmt.Errorf("准备插入可搜索表SQL失败: %w", errPrep)
				log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
				errResp(w, http.StatusInternalServerError, "数据库操作失败")
				return
			}
			defer stmt.Close()

			for _, tableName := range payload.SearchableTables {
				if strings.TrimSpace(tableName) == "" { // 跳过空表名
					continue
				}
				if _, errExec := stmt.ExecContext(r.Context(), bizName, tableName); errExec != nil {
					err = fmt.Errorf("插入可搜索表 '%s' 失败: %w", tableName, errExec)
					log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
					errResp(w, http.StatusInternalServerError, fmt.Sprintf("更新可搜索表 '%s' 失败", tableName))
					return
				}
			}
		}
		// 事务提交由 defer 处理
		if err == nil {
			configService.InvalidateCacheForBiz(bizName)
			log.Printf("信息: [Admin API] 业务组 '%s' 的可搜索表列表已更新。", bizName)
			respond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 可搜索表列表已更新", bizName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/tables] 事务处理结束时存在错误，可能未成功更新: %v", bizName, err)
		}
	}
}

// adminUpdateTableFieldSettingsHandler: PUT /admin/config/biz/{bizName}/tables/{tableName}/fields
func adminUpdateTableFieldSettingsHandler(configService aegdb.QueryAdminConfigService, sysDB *sql.DB, bizName string, tableName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var fieldSettings []aegdb.FieldSetting // aegdb.FieldSetting 是期望的DTO结构
		if err := json.NewDecoder(r.Body).Decode(&fieldSettings); err != nil {
			errResp(w, http.StatusBadRequest, "无效的JSON请求体 (期望 FieldSetting 数组): "+err.Error())
			return
		}
		defer r.Body.Close()

		// 如果 fieldSettings 为 nil (例如JSON是 "null")，处理为空数组，避免panic
		if fieldSettings == nil {
			fieldSettings = []aegdb.FieldSetting{}
		}

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] 开始事务失败: %v", bizName, tableName, errTx)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}
		defer func() {
			if p := recover(); p != nil {
				_ = tx.Rollback()
				panic(p)
			} else if err != nil {
				_ = tx.Rollback()
			} else {
				err = tx.Commit()
				if err != nil {
					log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] 提交事务失败: %v", bizName, tableName, err)
				} else {
					log.Printf("信息: [Admin API PUT /biz/%s/tables/%s/fields] 事务提交成功。", bizName, tableName)
				}
			}
		}()

		// 1. 删除该表所有旧的字段配置
		_, errDel := tx.ExecContext(r.Context(), "DELETE FROM biz_table_field_settings WHERE biz_name = ? AND table_name = ?", bizName, tableName)
		if errDel != nil {
			err = fmt.Errorf("清除旧字段配置失败: %w", errDel)
			log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
			errResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}

		// 2. 插入新的字段配置 (如果列表非空)
		if len(fieldSettings) > 0 {
			stmt, errPrep := tx.PrepareContext(r.Context(), `
                INSERT INTO biz_table_field_settings
                (biz_name, table_name, field_name, is_searchable, is_returnable, data_type)
                VALUES (?, ?, ?, ?, ?, ?)`)
			if errPrep != nil {
				err = fmt.Errorf("准备插入字段配置SQL失败: %w", errPrep)
				log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
				errResp(w, http.StatusInternalServerError, "数据库操作失败")
				return
			}
			defer stmt.Close()

			for _, fs := range fieldSettings {
				if strings.TrimSpace(fs.FieldName) == "" { // 跳过无效配置
					continue
				}

				_, errExec := stmt.ExecContext(r.Context(), bizName, tableName, fs.FieldName,
					fs.IsSearchable, fs.IsReturnable, fs.DataType)
				if errExec != nil {
					err = fmt.Errorf("插入字段 '%s' 配置失败: %w", fs.FieldName, errExec)
					log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
					errResp(w, http.StatusInternalServerError, fmt.Sprintf("更新字段 '%s' 配置失败", fs.FieldName))
					return
				}
			}
		}

		if err == nil {
			configService.InvalidateCacheForBiz(bizName) // 使整个业务组的缓存失效
			log.Printf("信息: [Admin API] 业务组 '%s' 表 '%s' 的字段配置已更新。", bizName, tableName)
			respond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 表 '%s' 字段配置已更新", bizName, tableName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/tables/%s/fields] 事务处理结束时存在错误，可能未成功更新: %v", bizName, tableName, err)
		}
	}
}

// adminGetBizViewsHandler: GET /api/admin/config/biz/{bizName}/views
func adminGetBizViewsHandler(configService aegdb.QueryAdminConfigService, bizName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 调用服务层方法获取所有视图配置
		views, err := configService.GetAllViewConfigsForBiz(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [Admin API GET /biz/%s/views] 获取视图配置失败: %v", bizName, err)
			errResp(w, http.StatusInternalServerError, fmt.Sprintf("获取业务 '%s' 的视图配置失败: %v", bizName, err))
			return
		}
		// 如果没有配置，views会是一个空的map，这是正常情况
		if views == nil {
			views = make(map[string][]*aegdb.ViewConfig) // 确保返回 {} 而不是 null
		}
		respond(w, views)
	}
}

// adminUpdateBizViewsHandler: PUT /api/admin/config/biz/{bizName}/views
func adminUpdateBizViewsHandler(configService aegdb.QueryAdminConfigService, bizName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 从请求体中解码视图配置数据
		var viewsData map[string][]*aegdb.ViewConfig
		if err := json.NewDecoder(r.Body).Decode(&viewsData); err != nil {
			errResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			// 关闭 Body，捕获异常（解码失败提前结束，主动 Close）
			if cerr := r.Body.Close(); cerr != nil {
				log.Printf("警告: 关闭请求体时发生错误: %v", cerr)
			}
			return
		}

		// 正常 defer，捕获 Close 错误
		defer func() {
			if cerr := r.Body.Close(); cerr != nil {
				log.Printf("警告: 关闭请求体时发生错误: %v", cerr)
			}
		}()

		// 调用服务层方法来更新数据库
		err := configService.UpdateAllViewsForBiz(r.Context(), bizName, viewsData)
		if err != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/views] 更新视图配置失败: %v", bizName, err)
			errResp(w, http.StatusInternalServerError, fmt.Sprintf("更新业务 '%s' 的视图配置失败: %v", bizName, err))
			return
		}

		// 更新成功后，让相关缓存失效
		configService.InvalidateCacheForBiz(bizName)
		log.Printf("信息: [Admin API] 业务组 '%s' 的视图配置已更新。", bizName)
		respond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 视图配置已更新", bizName)})
	}
}
