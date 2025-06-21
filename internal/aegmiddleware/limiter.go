package aegmiddleware

import (
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"golang.org/x/time/rate"
)

// limiterEntry 存储限制器和最后访问时间，被 BusinessRateLimiter 复用
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ============================================================================
//  业务性能限制器 (Business Performance Limiter) - V2版本
// ============================================================================

// BusinessRateLimiter 是一个统一的结构，管理所有业务性能相关的速率限制。
type BusinessRateLimiter struct {
	configService port.QueryAdminConfigService

	globalLimiter *rate.Limiter

	ipLimiters     map[string]*limiterEntry
	ipMu           sync.Mutex
	ipDefaultRate  rate.Limit
	ipDefaultBurst int

	userLimiters     map[int64]*limiterEntry
	userMu           sync.Mutex
	userDefaultRate  rate.Limit
	userDefaultBurst int

	bizLimiters map[string]*limiterEntry
	bizMu       sync.Mutex
}

// NewBusinessRateLimiter 创建一个新的、功能完备的业务速率限制器。
func NewBusinessRateLimiter(cs port.QueryAdminConfigService, globalRate float64, globalBurst int) *BusinessRateLimiter {
	brl := &BusinessRateLimiter{
		configService: cs,

		globalLimiter: rate.NewLimiter(rate.Limit(globalRate), globalBurst),

		ipLimiters:     make(map[string]*limiterEntry),
		ipDefaultRate:  1.0, // 默认 60 req/min
		ipDefaultBurst: 20,

		userLimiters:     make(map[int64]*limiterEntry),
		userDefaultRate:  5.0, // 已认证用户默认 5 req/s
		userDefaultBurst: 15,

		bizLimiters: make(map[string]*limiterEntry),
	}

	brl.loadIPDefaultSettings()
	go brl.cleanupIPs()
	go brl.cleanupUsers()
	go brl.cleanupBizs()

	log.Printf(
		"信息: [Business Limiter] 初始化完成。全局限制: %.2f req/s, 峰值: %d。IP默认限制: %.2f req/s, 峰值: %d",
		globalRate, globalBurst, brl.ipDefaultRate, brl.ipDefaultBurst,
	)

	return brl
}

// loadIPDefaultSettings 从数据库加载IP限制的默认配置。
func (brl *BusinessRateLimiter) loadIPDefaultSettings() {
	settings, err := brl.configService.GetIPLimitSettings(context.Background())
	if err == nil && settings != nil {
		brl.ipDefaultRate = rate.Limit(settings.RateLimitPerMinute / 60.0)
		brl.ipDefaultBurst = settings.BurstSize
		log.Printf("信息: [Business Limiter] 已从数据库加载IP速率限制默认值 (Rate: %.2f/min, Burst: %d)", settings.RateLimitPerMinute, settings.BurstSize)
	} else if err != nil {
		log.Printf("警告: [Business Limiter] 从数据库加载IP速率限制默认值失败: %v。将使用硬编码的默认值。", err)
	}
}

// cleanupIPs 定期清理不活跃的IP条目
func (brl *BusinessRateLimiter) cleanupIPs() {
	for {
		time.Sleep(10 * time.Minute)
		brl.ipMu.Lock()
		for ip, entry := range brl.ipLimiters {
			if time.Since(entry.lastSeen) > 15*time.Minute {
				delete(brl.ipLimiters, ip)
			}
		}
		brl.ipMu.Unlock()
	}
}

// cleanupUsers 定期清理不活跃的用户条目
func (brl *BusinessRateLimiter) cleanupUsers() {
	for {
		time.Sleep(10 * time.Minute)
		brl.userMu.Lock()
		for id, entry := range brl.userLimiters {
			if time.Since(entry.lastSeen) > 15*time.Minute {
				delete(brl.userLimiters, id)
			}
		}
		brl.userMu.Unlock()
	}
}

// cleanupBizs 定期清理不活跃的业务组条目
func (brl *BusinessRateLimiter) cleanupBizs() {
	for {
		time.Sleep(10 * time.Minute)
		brl.bizMu.Lock()
		for name, entry := range brl.bizLimiters {
			if time.Since(entry.lastSeen) > 15*time.Minute {
				delete(brl.bizLimiters, name)
			}
		}
		brl.bizMu.Unlock()
	}
}

// ==================================================================
//  模块化的中间件方法
// ==================================================================

// Global 返回全局限制中间件
func (brl *BusinessRateLimiter) Global(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !brl.globalLimiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "系统繁忙，请稍后再试 (global limit)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PerIP 返回IP限制中间件
func (brl *BusinessRateLimiter) PerIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		brl.ipMu.Lock()
		entry, exists := brl.ipLimiters[ip]
		if !exists {
			limiter := rate.NewLimiter(brl.ipDefaultRate, brl.ipDefaultBurst)
			entry = &limiterEntry{limiter: limiter, lastSeen: time.Now()}
			brl.ipLimiters[ip] = entry
		}
		entry.lastSeen = time.Now()
		brl.ipMu.Unlock()

		if !entry.limiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "您的请求过于频繁，请稍后再试 (per-ip limit)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PerUser 返回用户限制中间件
func (brl *BusinessRateLimiter) PerUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := service.ClaimFrom(r)
		if claims == nil { // 对于未认证用户，此中间件直接放行
			next.ServeHTTP(w, r)
			return
		}

		userID := claims.ID
		brl.userMu.Lock()
		entry, exists := brl.userLimiters[userID]
		if !exists {
			rateLimit, burstSize := brl.userDefaultRate, brl.userDefaultBurst // 先使用默认值
			if userSettings, err := brl.configService.GetUserLimitSettings(r.Context(), userID); err == nil && userSettings != nil {
				rateLimit = rate.Limit(userSettings.RateLimitPerSecond)
				burstSize = userSettings.BurstSize
				log.Printf("调试: [Business Limiter] 为用户ID %d 加载了特定速率限制: %.2f req/s, burst %d", userID, rateLimit, burstSize)
			}
			limiter := rate.NewLimiter(rateLimit, burstSize)
			entry = &limiterEntry{limiter: limiter, lastSeen: time.Now()}
			brl.userLimiters[userID] = entry
		}
		entry.lastSeen = time.Now()
		brl.userMu.Unlock()

		if !entry.limiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "您的账户请求过于频繁，请稍后再试 (per-user limit)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PerBiz 中间件现在可以处理 V1 API 的 POST JSON 请求体
func (brl *BusinessRateLimiter) PerBiz(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bizName string

		// 优先尝试从JSON Body中解析biz_name，以适配V1 API
		if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("WARN: [PerBiz Limiter] 读取请求体失败: %v", err)
				next.ServeHTTP(w, r)
				return
			}
			// 关键：将读取过的内容重新放回 r.Body 中，以供后续的处理器使用
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// 只解析我们需要的字段，提高性能
			var extractor struct {
				BizName string `json:"biz_name"`
			}
			if err := json.Unmarshal(bodyBytes, &extractor); err == nil {
				bizName = extractor.BizName
			}
		}

		// 如果不是POST JSON请求，或解析失败，尝试回退到旧的URL参数方式
		if bizName == "" {
			bizName = r.URL.Query().Get("biz")
		}

		if bizName == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 后续的速率限制逻辑完全不变
		brl.bizMu.Lock()
		entry, exists := brl.bizLimiters[bizName]
		if !exists {
			rateLimit, burstSize := brl.userDefaultRate, brl.userDefaultBurst
			if bizSettings, err := brl.configService.GetBizRateLimitSettings(r.Context(), bizName); err == nil && bizSettings != nil {
				rateLimit = rate.Limit(bizSettings.RateLimitPerSecond)
				burstSize = bizSettings.BurstSize
				log.Printf("调试: [Business Limiter] 为业务组 %s 加载了特定速率限制: %.2f req/s, burst %d", bizName, rateLimit, burstSize)
			}
			limiter := rate.NewLimiter(rateLimit, burstSize)
			entry = &limiterEntry{limiter: limiter, lastSeen: time.Now()}
			brl.bizLimiters[bizName] = entry
		}
		entry.lastSeen = time.Now()
		brl.bizMu.Unlock()

		if !entry.limiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "此业务接口请求过于频繁，请稍后再试 (per-biz limit)")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// FullBusinessChain 组合了所有四个限制层，用于核心业务API。
func (brl *BusinessRateLimiter) FullBusinessChain(next http.Handler) http.Handler {
	// 顺序: Global -> IP -> User -> Biz -> Handler
	return brl.Global(brl.PerIP(brl.PerUser(brl.PerBiz(next))))
}

// LightweightChain 组合了基础的限制层，用于公共/轻量级API。
func (brl *BusinessRateLimiter) LightweightChain(next http.Handler) http.Handler {
	// 顺序: Global -> IP -> Handler
	return brl.Global(brl.PerIP(next))
}

// ==================================================================
//  Tactic 1: 按 IP 地址的严格速率限制器 (Strict Per-IP Rate Limiter)
// ==================================================================

// IPRateLimiter 结构体，用于管理IP速率限制
type IPRateLimiter struct {
	limiters map[string]*limiterEntry
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
}

// NewIPRateLimiter 创建一个新的IP速率限制器
func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
	limiter := &IPRateLimiter{
		limiters: make(map[string]*limiterEntry),
		rate:     r,
		burst:    b,
	}
	go limiter.cleanupDaemon()
	return limiter
}

// getClientIP 从请求中获取客户端IP地址，考虑代理情况
func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	ip = strings.TrimSpace(strings.Split(ip, ",")[0])
	if ip != "" {
		return ip
	}
	ip = r.Header.Get("X-Real-IP")
	if ip != "" {
		return ip
	}
	ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	return ip
}

// getLimiter 返回或创建指定IP的速率限制器
func (l *IPRateLimiter) getLimiter(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, exists := l.limiters[ip]
	if !exists {
		limiter := rate.NewLimiter(l.rate, l.burst)
		l.limiters[ip] = &limiterEntry{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

// cleanupDaemon 定期清理不活跃的IP条目
func (l *IPRateLimiter) cleanupDaemon() {
	for {
		time.Sleep(10 * time.Minute)
		l.mu.Lock()
		for ip, entry := range l.limiters {
			if time.Since(entry.lastSeen) > 15*time.Minute {
				delete(l.limiters, ip)
			}
		}
		l.mu.Unlock()
	}
}

// Middleware 返回一个HTTP中间件
func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		limiter := l.getLimiter(ip)
		if !limiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ============================================================================
//  Tactic 2 & 3: 失败计数与临时锁定 (Failure Counting & Temporary Lockout)
// ============================================================================

// LoginFailureLock 结构体，用于实现登录失败锁定逻辑
type LoginFailureLock struct {
	failureCache    *cache.Cache
	maxFailures     int
	lockoutDuration time.Duration
}

// NewLoginFailureLock 创建一个新的登录失败锁定器
func NewLoginFailureLock(maxFailures int, lockoutDuration time.Duration) *LoginFailureLock {
	return &LoginFailureLock{
		failureCache:    cache.New(5*time.Minute, 10*time.Minute),
		maxFailures:     maxFailures,
		lockoutDuration: lockoutDuration,
	}
}

// statusRecorder 是一个健壮的 http.ResponseWriter 包装器
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Flush() {
	if flusher, ok := rec.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (rec *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rec.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Middleware 返回一个特殊的中间件，用于包裹登录处理器
func (l *LoginFailureLock) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			errResp(w, http.StatusBadRequest, "无法解析表单数据: "+err.Error())
			return
		}
		username := strings.TrimSpace(r.FormValue("user"))
		ip := getClientIP(r)
		lockKey := "lock:" + ip + ":" + username

		if _, found := l.failureCache.Get(lockKey); found {
			log.Printf("警告: [Login Lock] 已锁定的账户 '%s' (来自IP: %s) 再次尝试登录。", username, ip)
			errResp(w, http.StatusUnauthorized, "用户名或密码无效")
			return
		}

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		if recorder.status == http.StatusUnauthorized {
			failureKey := "failures:" + ip + ":" + username

			// 尝试对计数器加一。Increment只返回一个error。
			err := l.failureCache.Increment(failureKey, int64(1))

			// 如果返回错误，说明key不存在（即第一次失败），所以设置初始值为1。
			if err != nil {
				l.failureCache.Set(failureKey, int64(1), cache.DefaultExpiration)
			}

			// 再从缓存中获取最新的计数值。
			var currentFailures int
			if x, found := l.failureCache.Get(failureKey); found {
				currentFailures = int(x.(int64)) // 从缓存取出的值需要类型断言
			}

			log.Printf("信息: [Login Failure] 账户 '%s' (来自IP: %s) 登录失败，当前失败次数: %d", username, ip, currentFailures)

			if currentFailures >= l.maxFailures {
				l.failureCache.Set(lockKey, true, l.lockoutDuration)
				l.failureCache.Delete(failureKey)
				log.Printf("警告: [Login Lock] 账户 '%s' (来自IP: %s) 已被临时锁定 %v。", username, ip, l.lockoutDuration)
			}
		}

		if recorder.status == http.StatusOK {
			failureKey := "failures:" + ip + ":" + username
			l.failureCache.Delete(failureKey)
		}
	})
}

// errResp 的一个本地副本
func errResp(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
