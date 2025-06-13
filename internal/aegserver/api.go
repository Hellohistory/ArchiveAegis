// Package aegserver — （Setup / Login / 业务 / 管理）
package aegserver

import (
	"ArchiveAegis/internal/aegauth"
	"ArchiveAegis/internal/aegdata"
	"ArchiveAegis/internal/aeglogic"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/pkg/aegmiddleware"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/time/rate"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/go-chi/chi/v5"
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

// NoCORSrespond respond 统一 JSON 输出 (无CORS头部)
func NoCORSrespond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}

// NoCORSerrResp 带 status code 的错误输出 (无CORS头部)
func NoCORSerrResp(w http.ResponseWriter, code int, msg string) {
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

// 为全局速率限制器定义启动参数
var (
	globalRateLimit = flag.Float64("global-rate-limit", 200.0, "全局业务API每秒请求速率限制")
	globalBurst     = flag.Int("global-burst", 400, "全局业务API瞬时请求峰值")
)

// NewRouter 为API路由添加/api前缀，并注册新的状态检查接口。
func NewRouter(mgr *aegdata.Manager, sysDB *sql.DB, configService aeglogic.QueryAdminConfigService) http.Handler {
	if sysDB == nil {
		log.Fatal("严重错误 (aegapi.NewRouter): sysDB (用户数据库) 连接为空！ 应用无法启动。")
	}

	if !flag.Parsed() {
		flag.Parse()
	}

	authenticator := aegauth.NewAuthenticator(sysDB)

	// --- 速率限制器 ---
	loginIPLimiter := aegmiddleware.NewIPRateLimiter(rate.Limit(15.0/60.0), 5)
	loginFailureLock := aegmiddleware.NewLoginFailureLock(5, 15*time.Minute)

	loginSecurityChain := func(h http.Handler) http.Handler {
		return loginFailureLock.Middleware(loginIPLimiter.Middleware(h))
	}

	businessLimiter := aegmiddleware.NewBusinessRateLimiter(configService, *globalRateLimit, *globalBurst)
	apiMux := http.NewServeMux()

	// --- 安全核心轨道 (最严格) ---
	apiMux.Handle("/api/setup", loginSecurityChain(setupHandler(sysDB)))
	apiMux.Handle("/api/login", loginSecurityChain(loginHandler(sysDB)))

	// --- 核心业务轨道 (全功能限制) ---
	businessApiMux := http.NewServeMux()
	businessApiMux.HandleFunc("/api/search", searchHandler(mgr))
	businessApiMux.HandleFunc("/api/columns", columnsHandler(configService))
	businessApiMux.HandleFunc("/api/view/config", viewConfigHandler(configService))
	apiMux.Handle("/api/search", businessLimiter.FullBusinessChain(businessApiMux))
	apiMux.Handle("/api/columns", businessLimiter.FullBusinessChain(businessApiMux))
	apiMux.Handle("/api/view/config", businessLimiter.FullBusinessChain(businessApiMux))

	// --- 公开信息轨道 ---
	publicApiMux := http.NewServeMux()
	publicApiMux.HandleFunc("/api/auth/status", authStatusHandler(sysDB))
	publicApiMux.HandleFunc("/api/biz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		NoCORSrespond(w, mgr.Summary())
	})
	publicApiMux.HandleFunc("/api/tables", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		bizName := r.URL.Query().Get("biz")
		if bizName == "" {
			NoCORSerrResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 参数")
			return
		}
		physicalTables := mgr.Tables(bizName)

		// 如果 mgr.Tables 返回 nil，说明业务组不存在或无效，应该返回 404
		if physicalTables == nil {
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("业务组 '%s' 未找到或不包含任何表", bizName))
			return
		}

		NoCORSrespond(w, physicalTables)
	})
	apiMux.Handle("/api/auth/status", businessLimiter.LightweightChain(publicApiMux))
	apiMux.Handle("/api/biz", businessLimiter.LightweightChain(publicApiMux))
	apiMux.Handle("/api/tables", businessLimiter.LightweightChain(publicApiMux))

	// --- 管理员API轨道 ---
	adminRouter := chi.NewRouter()
	adminRouter.Get("/configured-biz-names", adminGetConfiguredBizNamesHandler(configService))

	// 定义 /api/admin/settings 路由
	adminRouter.Route("/settings", func(r chi.Router) {
		r.Get("/ip_limit", adminIPLimitSettingsHandler(configService))
		r.Put("/ip_limit", adminIPLimitSettingsHandler(configService))
	})

	// 定义 /api/admin/config/biz/{bizName} 相关路由
	adminRouter.Route("/config/biz/{bizName}", func(r chi.Router) {
		// GET /api/admin/config/biz/{bizName}
		r.Get("/", adminGetBizConfigHandler(configService))

		// PUT /api/admin/config/biz/{bizName}/settings
		r.Put("/settings", adminUpdateBizOverallSettingsHandler(configService, sysDB))

		// PUT /api/admin/config/biz/{bizName}/tables
		r.Put("/tables", adminUpdateBizSearchableTablesHandler(configService, sysDB))

		// GET & PUT /api/admin/config/biz/{bizName}/ratelimit
		r.Get("/ratelimit", adminGetBizRateLimitHandler(configService))
		r.Put("/ratelimit", adminUpdateBizRateLimitHandler(configService))

		// GET & PUT /api/admin/config/biz/{bizName}/views
		r.Route("/views", func(subr chi.Router) {
			subr.Get("/", adminGetBizViewsHandler(configService))
			subr.Put("/", adminUpdateBizViewsHandler(configService))
		})

		// 针对特定表的路由 /api/admin/config/biz/{bizName}/tables/{tableName}
		r.Route("/tables/{tableName}", func(subr chi.Router) {
			// GET /api/admin/config/biz/{bizName}/tables/{tableName}/physical-columns
			subr.Get("/physical-columns", adminGetTablePhysicalColumnsHandler(mgr))
			// PUT /api/admin/config/biz/{bizName}/tables/{tableName}/fields
			subr.Put("/fields", adminUpdateTableFieldSettingsHandler(configService, sysDB))
		})
	})

	// 将所有 /api/admin/ 下的请求都交由 adminRouter 处理，并用管理员权限中间件保护
	apiMux.Handle("/api/admin/", aegmiddleware.RequireAdmin(http.StripPrefix("/api/admin", adminRouter)))

	root := http.NewServeMux()
	root.Handle("/api/", authenticator.Middleware(apiMux))
	return gziphandler.GzipHandler(root)
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
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		if aegauth.UserCount(sysDB) > 0 {
			NoCORSrespond(w, map[string]string{"status": "ready_for_login"})
		} else {
			NoCORSrespond(w, map[string]string{"status": "needs_setup"})
		}
	}
}

/*
============================================================
   /view/config Handler
============================================================
*/

// viewConfigHandler 处理获取指定表默认视图配置的请求
func viewConfigHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}

		q := r.URL.Query()
		bizName := q.Get("biz")
		tableName := q.Get("table")

		if bizName == "" || tableName == "" {
			NoCORSerrResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 或 'table' (表名) 参数")
			return
		}

		viewConfig, err := configService.GetDefaultViewConfig(r.Context(), bizName, tableName)
		if err != nil {
			// service 层返回的错误，500内部错误
			log.Printf("错误: [API /view/config] 调用 configService.GetDefaultViewConfig 失败 (biz: '%s', table: '%s'): %v", bizName, tableName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "获取视图配置时发生内部错误")
			return
		}

		// 如果 viewConfig 为 nil，表示没有找到对应的默认视图配置。
		// 这不是一个服务器错误，而是一个 "资源未找到" 的情况，返回 404 。
		if viewConfig == nil {
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("未找到业务 '%s' 表 '%s' 的默认视图配置", bizName, tableName))
			return
		}

		// 成功找到配置，返回 JSON
		NoCORSrespond(w, viewConfig)
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
				NoCORSerrResp(w, http.StatusForbidden, "系统已安装，无法获取安装令牌。")
				return
			}
			NoCORSrespond(w, map[string]string{"token": setupToken})
			return
		}

		if r.Method == http.MethodPost {
			if aegauth.UserCount(sysDB) > 0 {
				NoCORSerrResp(w, http.StatusForbidden, "系统已存在管理员账户，无法重复设置。")
				return
			}
			if r.FormValue("token") != setupToken || setupToken == "" || time.Now().After(setupTokenDead) {
				NoCORSerrResp(w, http.StatusBadRequest, "无效或过期的安装令牌")
				return
			}
			user := strings.TrimSpace(r.FormValue("user"))
			pass := r.FormValue("pass")
			if user == "" || pass == "" {
				NoCORSerrResp(w, http.StatusBadRequest, "用户名或密码不能为空")
				return
			}

			if err := aegauth.CreateAdmin(sysDB, user, pass); err != nil {
				log.Printf("错误: [API /setup] 创建管理员 '%s' 失败: %v", user, err)
				NoCORSerrResp(w, http.StatusInternalServerError, "创建管理员失败: "+err.Error())
				return
			}
			setupToken = ""

			userID, _, ok := aegauth.CheckUser(sysDB, user, pass)
			if !ok {
				log.Printf("严重错误: [API /setup] 刚创建的管理员 '%s' 无法校验以生成Token。", user)
				NoCORSerrResp(w, http.StatusInternalServerError, "无法为新管理员生成令牌")
				return
			}

			jwtToken, err := aegauth.GenToken(userID, "admin")
			if err != nil {
				log.Printf("错误: [API /setup] 为管理员 '%s' (ID: %d) 生成JWT失败: %v", user, userID, err)
				NoCORSerrResp(w, http.StatusInternalServerError, "生成JWT失败: "+err.Error())
				return
			}
			log.Printf("信息: [API /setup] 管理员 '%s' (ID: %d) 创建成功。", user, userID)
			responsePayload := map[string]interface{}{
				"token": jwtToken,
				"user":  map[string]interface{}{"id": userID, "username": user, "role": "admin"},
			}
			NoCORSrespond(w, responsePayload)
			return
		}

		http.NotFound(w, r)
	}
}

func loginHandler(sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持POST方法")
			return
		}
		user := strings.TrimSpace(r.FormValue("user"))
		pass := r.FormValue("pass")

		id, _, ok := aegauth.CheckUser(sysDB, user, pass)
		if !ok {
			NoCORSerrResp(w, http.StatusUnauthorized, "用户名或密码无效")
			return
		}
		dbUsername, dbRole, _ := aegauth.GetUserById(sysDB, id)

		jwtToken, err := aegauth.GenToken(id, dbRole)
		if err != nil {
			log.Printf("错误: [API /login] 为用户 '%s' (ID: %d, Role: %s) 生成JWT失败: %v", dbUsername, id, dbRole, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "生成JWT失败: "+err.Error())
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
		NoCORSrespond(w, responsePayload)
	}
}

/*
============================================================
    /columns Handler
============================================================
*/

func columnsHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法")
			return
		}
		bizName := r.URL.Query().Get("biz")
		tableName := r.URL.Query().Get("table")

		if bizName == "" || tableName == "" {
			NoCORSerrResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 或 'table' (表名) 参数")
			return
		}

		bizConfig, err := configService.GetBizQueryConfig(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [API /columns] 获取业务 '%s' 配置失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "获取业务配置时发生内部错误")
			return
		}
		if bizConfig == nil {
			log.Printf("信息: [API /columns] 业务 '%s' 未找到查询配置。", bizName)
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("业务 '%s' 未配置查询规则", bizName))
			return
		}
		if !bizConfig.IsPubliclySearchable {
			claims := aegauth.ClaimFrom(r)
			if claims == nil || claims.Role != "admin" {
				log.Printf("信息: [API /columns] 业务 '%s' 配置为不可公开查询，且访问者非管理员。", bizName)
				NoCORSerrResp(w, http.StatusForbidden, fmt.Sprintf("业务 '%s' 不允许查询", bizName))
				return
			}
			log.Printf("信息: [API /columns] 管理员访问业务 '%s' (配置为不可公开查询) 的列信息。", bizName)
		}

		tableConfig, tableExists := bizConfig.Tables[tableName]
		if !tableExists {
			log.Printf("信息: [API /columns] 表 '%s' (业务 '%s') 在查询配置中未定义。", tableName, bizName)
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("表 '%s' 在业务 '%s' 中未配置查询规则", tableName, bizName))
			return
		}

		var allConfiguredFields []ReturnableFieldInfo
		fieldNamesFromConfig := make([]string, 0, len(tableConfig.Fields))
		for fn := range tableConfig.Fields {
			fieldNamesFromConfig = append(fieldNamesFromConfig, fn)
		}
		sort.Strings(fieldNamesFromConfig)

		for _, fieldName := range fieldNamesFromConfig {
			setting := tableConfig.Fields[fieldName]
			dataType := setting.DataType
			if dataType == "" {
				dataType = "string"
			}
			allConfiguredFields = append(allConfiguredFields, ReturnableFieldInfo{
				Name:         setting.FieldName,
				OriginalName: setting.FieldName,
				IsSearchable: setting.IsSearchable,
				IsReturnable: setting.IsReturnable,
				DataType:     dataType,
			})
		}

		if allConfiguredFields == nil {
			allConfiguredFields = []ReturnableFieldInfo{}
		}
		NoCORSrespond(w, allConfiguredFields)
	}
}

/*
============================================================
    Search Handler
============================================================
*/

func searchHandler(mgr *aegdata.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET方法进行搜索")
			return
		}
		aegobserve.TotalReq.Inc()

		q := r.URL.Query()
		bizName := q.Get("biz")
		tableName := q.Get("table")

		if bizName == "" {
			NoCORSerrResp(w, http.StatusBadRequest, "缺少 'biz' (业务组) 参数")
			aegobserve.FailReq.Inc()
			return
		}

		fields := pick(q, "fields")
		values := pick(q, "values")
		fuzzyStr := pick(q, "fuzzy")
		logics := pick(q, "logic")

		if len(fields) > 0 && (len(fields) != len(values) || len(fields) != len(fuzzyStr)) {
			NoCORSerrResp(w, http.StatusBadRequest, "当提供 'fields' 时, 'values' 和 'fuzzy' 参数的个数必须与其一致")
			aegobserve.FailReq.Inc()
			return
		}
		if len(fields) > 1 && len(logics) != len(fields)-1 {
			NoCORSerrResp(w, http.StatusBadRequest, "当查询条件大于1个时, 'logic' 参数的个数应为 'fields' 个数减 1")
			aegobserve.FailReq.Inc()
			return
		}

		var queryParams []aegdata.Param
		for i := range fields {
			isFuzzy, errConv := strconv.ParseBool(fuzzyStr[i])
			if errConv != nil {
				isFuzzy = false
				log.Printf("警告: [API /search] 无效的 'fuzzy[%d]' 参数值 '%s' (业务 '%s')，已默认为 false。", i, fuzzyStr[i], bizName)
			}
			param := aegdata.Param{
				Field: fields[i], Value: values[i], Fuzzy: isFuzzy,
			}
			if i < len(logics) {
				param.Logic = strings.ToUpper(logics[i])
				if param.Logic != "AND" && param.Logic != "OR" {
					NoCORSerrResp(w, http.StatusBadRequest, fmt.Sprintf("无效的逻辑操作符: '%s' (在第 %d 个条件后)", param.Logic, i+1))
					aegobserve.FailReq.Inc()
					return
				}
			} else if len(fields) > 1 && i < len(fields)-1 {
				NoCORSerrResp(w, http.StatusBadRequest, fmt.Sprintf("第 %d 个查询条件后缺少逻辑操作符 'logic'", i+1))
				aegobserve.FailReq.Inc()
				return
			}
			queryParams = append(queryParams, param)
		}

		pageStr := q.Get("page")
		page, _ := strconv.Atoi(pageStr)
		if page < 1 {
			page = 1
		}
		sizeStr := q.Get("size")
		size, _ := strconv.Atoi(sizeStr)
		if size < 1 {
			size = 50
		} else if size > 2000 {
			log.Printf("警告: [API /search] 请求的页大小 %d (业务 '%s') 超出最大限制 2000，已调整为 2000。", size, bizName)
			size = 2000
		}

		results, err := mgr.Query(r.Context(), bizName, tableName, queryParams, page, size)
		if err != nil {
			aegobserve.FailReq.Inc()
			if errors.Is(err, aegdata.ErrPermissionDenied) {
				log.Printf("信息: [API /search] 业务 '%s' 表 '%s' 查询权限不足: %v", bizName, tableName, err)
				NoCORSerrResp(w, http.StatusForbidden, "查询权限不足或业务/表不可查询")
			} else if errors.Is(err, aegdata.ErrBizNotFound) || errors.Is(err, aegdata.ErrTableNotFoundInBiz) {
				log.Printf("信息: [API /search] 业务 '%s' 或表 '%s' 未找到: %v", bizName, tableName, err)
				NoCORSerrResp(w, http.StatusNotFound, "业务组或表未找到")
			} else if strings.Contains(err.Error(), "没有可用的默认视图") {
				log.Printf("信息: [API /search] 查询失败 (biz: '%s', table: '%s'): %v", bizName, tableName, err)
				NoCORSerrResp(w, http.StatusBadRequest, err.Error())
			} else {
				log.Printf("错误: [API /search] 调用 mgr.Query 失败 (biz: '%s', table: '%s'): %v", bizName, tableName, err)
				NoCORSerrResp(w, http.StatusInternalServerError, "查询处理过程中发生错误")
			}
			return
		}
		if results == nil {
			results = []map[string]any{}
		}
		NoCORSrespond(w, results)
	}
}

/*
============================================================
  Admin API Handlers
============================================================
*/

// adminUpdateBizRateLimitHandler: PUT /api/admin/config/biz/{bizName}/ratelimit
func adminUpdateBizRateLimitHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bizName := chi.URLParam(r, "bizName")

		var payload aeglogic.BizRateLimitSetting
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}

		if payload.RateLimitPerSecond < 0 || payload.BurstSize < 0 {
			NoCORSerrResp(w, http.StatusBadRequest, "速率和峰值不能为负数")
			return
		}

		err := configService.UpdateBizRateLimitSettings(r.Context(), bizName, payload)
		if err != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/ratelimit] 更新速率限制失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "更新配置时发生内部错误")
			return
		}

		// 重要: 速率限制的配置更新后，需要让缓存失效，以便中间件能加载到新规则
		configService.InvalidateCacheForBiz(bizName)

		response := map[string]string{
			"status":  "success",
			"message": fmt.Sprintf("业务组 '%s' 的速率限制已成功更新", bizName),
		}
		NoCORSrespond(w, response)
	}
}

// adminGetTablePhysicalColumnsHandler: GET /api/admin/config/biz/{bizName}/tables/{tableName}/physical-columns
func adminGetTablePhysicalColumnsHandler(mgr *aegdata.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bizName := chi.URLParam(r, "bizName")
		tableName := chi.URLParam(r, "tableName")

		physicalCols := mgr.PhysicalColumns(bizName, tableName)
		if physicalCols == nil {
			log.Printf("警告: [Admin API /physical-columns] 业务 '%s' - 表 '%s': 未从Manager获取到物理列信息。", bizName, tableName)
			NoCORSrespond(w, []string{})
			return
		}
		log.Printf("信息: [Admin API /physical-columns] 返回业务 '%s' - 表 '%s' 的物理列: %d 个。", bizName, tableName, len(physicalCols))
		NoCORSrespond(w, physicalCols)
	}
}

// adminGetBizConfigHandler: GET /api/admin/config/biz/{bizName}
func adminGetBizConfigHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bizName := chi.URLParam(r, "bizName")
		cfg, err := configService.GetBizQueryConfig(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [Admin API GET /biz/%s] 获取配置失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, fmt.Sprintf("获取业务 '%s' 配置失败: %v", bizName, err))
			return
		}
		if cfg == nil {
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("业务 '%s' 未找到查询配置", bizName))
			return
		}
		NoCORSrespond(w, cfg)
	}
}

// adminUpdateBizOverallSettingsHandler: PUT /api/admin/config/biz/{bizName}/settings
func adminUpdateBizOverallSettingsHandler(configService aeglogic.QueryAdminConfigService, sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bizName := chi.URLParam(r, "bizName")

		var payload struct {
			IsPubliclySearchable *bool   `json:"is_publicly_searchable"`
			DefaultQueryTable    *string `json:"default_query_table"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/settings] 开始事务失败: %v", bizName, errTx)
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
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
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库查询操作失败")
			return
		}

		finalIsPubliclySearchable := currentIsPubliclySearchable
		if payload.IsPubliclySearchable != nil {
			finalIsPubliclySearchable = *payload.IsPubliclySearchable
		}

		finalDefaultQueryTable := currentDefaultTable
		if payload.DefaultQueryTable != nil {
			if *payload.DefaultQueryTable == "" {
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
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}
		defer stmt.Close()

		var defaultTableForDB interface{}
		if finalDefaultQueryTable.Valid {
			defaultTableForDB = finalDefaultQueryTable.String
		} else {
			defaultTableForDB = nil
		}

		if _, errExec := stmt.ExecContext(r.Context(), bizName, finalIsPubliclySearchable, defaultTableForDB); errExec != nil {
			err = fmt.Errorf("更新配置失败: %w", errExec)
			log.Printf("错误: [Admin API PUT /biz/%s/settings] %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "更新业务组总体配置失败")
			return
		}

		if err == nil {
			configService.InvalidateCacheForBiz(bizName)
			log.Printf("信息: [Admin API] 业务组 '%s' 的总体配置已更新。", bizName)
			NoCORSrespond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 总体配置已更新", bizName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/settings] 事务处理结束时存在错误，可能未成功更新: %v", bizName, err)
		}
	}
}

// adminUpdateBizSearchableTablesHandler: PUT /api/admin/config/biz/{bizName}/tables
func adminUpdateBizSearchableTablesHandler(configService aeglogic.QueryAdminConfigService, sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bizName := chi.URLParam(r, "bizName")

		var payload struct {
			SearchableTables []string `json:"searchable_tables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}

		if payload.SearchableTables == nil {
			payload.SearchableTables = []string{}
		}

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/tables] 开始事务失败: %v", bizName, errTx)
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
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

		if _, errDel := tx.ExecContext(r.Context(), "DELETE FROM biz_searchable_tables WHERE biz_name = ?", bizName); errDel != nil {
			err = fmt.Errorf("清除旧可搜索表配置失败: %w", errDel)
			log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}

		if len(payload.SearchableTables) > 0 {
			stmt, errPrep := tx.PrepareContext(r.Context(), "INSERT INTO biz_searchable_tables (biz_name, table_name) VALUES (?, ?)")
			if errPrep != nil {
				err = fmt.Errorf("准备插入可搜索表SQL失败: %w", errPrep)
				log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
				NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
				return
			}
			defer stmt.Close()

			for _, tableName := range payload.SearchableTables {
				if strings.TrimSpace(tableName) == "" {
					continue
				}
				if _, errExec := stmt.ExecContext(r.Context(), bizName, tableName); errExec != nil {
					err = fmt.Errorf("插入可搜索表 '%s' 失败: %w", tableName, errExec)
					log.Printf("错误: [Admin API PUT /biz/%s/tables] %v", bizName, err)
					NoCORSerrResp(w, http.StatusInternalServerError, fmt.Sprintf("更新可搜索表 '%s' 失败", tableName))
					return
				}
			}
		}
		if err == nil {
			configService.InvalidateCacheForBiz(bizName)
			log.Printf("信息: [Admin API] 业务组 '%s' 的可搜索表列表已更新。", bizName)
			NoCORSrespond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 可搜索表列表已更新", bizName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/tables] 事务处理结束时存在错误，可能未成功更新: %v", bizName, err)
		}
	}
}

// adminUpdateTableFieldSettingsHandler: PUT /api/admin/config/biz/{bizName}/tables/{tableName}/fields
func adminUpdateTableFieldSettingsHandler(configService aeglogic.QueryAdminConfigService, sysDB *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bizName := chi.URLParam(r, "bizName")
		tableName := chi.URLParam(r, "tableName")

		var fieldSettings []aeglogic.FieldSetting
		if err := json.NewDecoder(r.Body).Decode(&fieldSettings); err != nil {
			NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体 (期望 FieldSetting 数组): "+err.Error())
			return
		}

		if fieldSettings == nil {
			fieldSettings = []aeglogic.FieldSetting{}
		}

		var err error
		tx, errTx := sysDB.BeginTx(r.Context(), nil)
		if errTx != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] 开始事务失败: %v", bizName, tableName, errTx)
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
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

		_, errDel := tx.ExecContext(r.Context(), "DELETE FROM biz_table_field_settings WHERE biz_name = ? AND table_name = ?", bizName, tableName)
		if errDel != nil {
			err = fmt.Errorf("清除旧字段配置失败: %w", errDel)
			log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
			return
		}

		if len(fieldSettings) > 0 {
			stmt, errPrep := tx.PrepareContext(r.Context(), `
                INSERT INTO biz_table_field_settings
                (biz_name, table_name, field_name, is_searchable, is_returnable, data_type)
                VALUES (?, ?, ?, ?, ?, ?)`)
			if errPrep != nil {
				err = fmt.Errorf("准备插入字段配置SQL失败: %w", errPrep)
				log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
				NoCORSerrResp(w, http.StatusInternalServerError, "数据库操作失败")
				return
			}
			defer stmt.Close()

			for _, fs := range fieldSettings {
				if strings.TrimSpace(fs.FieldName) == "" {
					continue
				}

				_, errExec := stmt.ExecContext(r.Context(), bizName, tableName, fs.FieldName,
					fs.IsSearchable, fs.IsReturnable, fs.DataType)
				if errExec != nil {
					err = fmt.Errorf("插入字段 '%s' 配置失败: %w", fs.FieldName, errExec)
					log.Printf("错误: [Admin API PUT /biz/%s/tables/%s/fields] %v", bizName, tableName, err)
					NoCORSerrResp(w, http.StatusInternalServerError, fmt.Sprintf("更新字段 '%s' 配置失败", fs.FieldName))
					return
				}
			}
		}

		if err == nil {
			configService.InvalidateCacheForBiz(bizName)
			log.Printf("信息: [Admin API] 业务组 '%s' 表 '%s' 的字段配置已更新。", bizName, tableName)
			NoCORSrespond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 表 '%s' 字段配置已更新", bizName, tableName)})
		} else {
			log.Printf("警告: [Admin API PUT /biz/%s/tables/%s/fields] 事务处理结束时存在错误，可能未成功更新: %v", bizName, tableName, err)
		}
	}
}

// adminGetBizViewsHandler: GET /api/admin/config/biz/{bizName}/views
func adminGetBizViewsHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bizName := chi.URLParam(r, "bizName")
		views, err := configService.GetAllViewConfigsForBiz(r.Context(), bizName)
		if err != nil {
			log.Printf("错误: [Admin API GET /biz/%s/views] 获取视图配置失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, fmt.Sprintf("获取业务 '%s' 的视图配置失败: %v", bizName, err))
			return
		}
		if views == nil {
			views = make(map[string][]*aeglogic.ViewConfig)
		}
		NoCORSrespond(w, views)
	}
}

// adminUpdateBizViewsHandler: PUT /api/admin/config/biz/{bizName}/views
func adminUpdateBizViewsHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bizName := chi.URLParam(r, "bizName")

		var viewsData map[string][]*aeglogic.ViewConfig
		if err := json.NewDecoder(r.Body).Decode(&viewsData); err != nil {
			NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
			return
		}

		err := configService.UpdateAllViewsForBiz(r.Context(), bizName, viewsData)
		if err != nil {
			log.Printf("错误: [Admin API PUT /biz/%s/views] 更新视图配置失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, fmt.Sprintf("更新业务 '%s' 的视图配置失败: %v", bizName, err))
			return
		}

		configService.InvalidateCacheForBiz(bizName)
		log.Printf("信息: [Admin API] 业务组 '%s' 的视图配置已更新。", bizName)
		NoCORSrespond(w, map[string]string{"status": "success", "message": fmt.Sprintf("业务组 '%s' 视图配置已更新", bizName)})
	}
}

/*
============================================================
  Admin IP Limit Settings Handler
============================================================
*/

// adminIPLimitSettingsHandler 处理全局IP速率限制的GET和PUT请求
func adminIPLimitSettingsHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetIPLimit(w, r, configService)
		case http.MethodPut:
			handleUpdateIPLimit(w, r, configService)
		default:
			NoCORSerrResp(w, http.StatusMethodNotAllowed, "仅支持GET和PUT方法")
		}
	}
}

// handleGetIPLimit: GET /api/admin/settings/ip_limit
func handleGetIPLimit(w http.ResponseWriter, r *http.Request, configService aeglogic.QueryAdminConfigService) {
	settings, err := configService.GetIPLimitSettings(r.Context())
	if err != nil {
		log.Printf("错误: [Admin API GET /settings/ip_limit] 调用服务失败: %v", err)
		NoCORSerrResp(w, http.StatusInternalServerError, "获取配置时发生内部错误")
		return
	}

	if settings == nil {
		log.Printf("信息: [Admin API GET /settings/ip_limit] 未找到配置, 返回系统启动默认值。")
		NoCORSrespond(w, map[string]interface{}{
			"rate_limit_per_minute": *globalRateLimit * 60,
			"burst_size":            *globalBurst,
		})
		return
	}

	NoCORSrespond(w, settings)
}

// handleUpdateIPLimit: PUT /api/admin/settings/ip_limit
func handleUpdateIPLimit(w http.ResponseWriter, r *http.Request, configService aeglogic.QueryAdminConfigService) {
	defer r.Body.Close()
	var payload aeglogic.IPLimitSetting

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		NoCORSerrResp(w, http.StatusBadRequest, "无效的JSON请求体: "+err.Error())
		return
	}

	err := configService.UpdateIPLimitSettings(r.Context(), payload)
	if err != nil {
		log.Printf("错误: [Admin API PUT /settings/ip_limit] 调用服务更新配置失败: %v", err)
		NoCORSerrResp(w, http.StatusInternalServerError, "更新配置失败")
		return
	}

	log.Printf("信息: [Admin API] 全局IP速率限制已更新。")
	NoCORSrespond(w, map[string]string{"status": "success", "message": "全局IP速率限制已更新"})
}

// adminGetConfiguredBizNamesHandler: GET /api/admin/configured-biz-names
func adminGetConfiguredBizNamesHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configuredNames, err := configService.GetAllConfiguredBizNames(r.Context())
		if err != nil {
			log.Printf("错误: [Admin API GET /configured-biz-names] 获取已配置业务名称失败: %v", err)
			NoCORSerrResp(w, http.StatusInternalServerError, "获取业务列表失败")
			return
		}
		if configuredNames == nil {
			configuredNames = []string{} // 确保永不返回 null
		}
		NoCORSrespond(w, configuredNames)
	}
}

// adminGetBizRateLimitHandler: GET /api/admin/config/biz/{bizName}/ratelimit
func adminGetBizRateLimitHandler(configService aeglogic.QueryAdminConfigService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bizName := chi.URLParam(r, "bizName")

		settings, err := configService.GetBizRateLimitSettings(r.Context(), bizName)

		// 处理 service 层返回的非“未找到”错误
		if err != nil {
			log.Printf("错误: [Admin API GET /biz/%s/ratelimit] 获取速率限制失败: %v", bizName, err)
			NoCORSerrResp(w, http.StatusInternalServerError, "获取配置时发生内部错误")
			return
		}

		// service 层返回 (nil, nil) 表示未找到
		if settings == nil {
			NoCORSerrResp(w, http.StatusNotFound, fmt.Sprintf("未找到业务组 '%s' 的速率限制配置", bizName))
			return
		}

		NoCORSrespond(w, settings)
	}
}
