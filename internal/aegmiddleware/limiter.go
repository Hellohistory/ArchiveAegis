// Package aegmiddleware 提供项目通用的中间件实现。
// 本文件实现业务性能速率限制器（BusinessRateLimiter）及相关策略组件，支持按全局、IP、用户
// 与业务维度的多级限流。
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

// limiterEntry 记录限速器实例及其最后一次访问时间，供 BusinessRateLimiter 复用。
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

//  业务性能限制器 (Business Performance Limiter)

// BusinessRateLimiter 统一管理业务相关的多级速率限制，包括全局、IP、用户及业务维度。
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

// NewBusinessRateLimiter 创建并初始化 BusinessRateLimiter。
func NewBusinessRateLimiter(cs port.QueryAdminConfigService, globalRate float64, globalBurst int) *BusinessRateLimiter {
	brl := &BusinessRateLimiter{
		configService: cs,

		globalLimiter: rate.NewLimiter(rate.Limit(globalRate), globalBurst),

		ipLimiters:     make(map[string]*limiterEntry),
		ipDefaultRate:  1.0,
		ipDefaultBurst: 20,

		userLimiters:     make(map[int64]*limiterEntry),
		userDefaultRate:  5.0,
		userDefaultBurst: 15,

		bizLimiters: make(map[string]*limiterEntry),
	}

	if cs != nil {
		brl.loadIPDefaultSettings()
	} else {
		log.Println("警告: [Business Limiter] 未提供 configService，将使用硬编码的默认速率限制。")
	}

	go brl.cleanupIPs()
	go brl.cleanupUsers()
	go brl.cleanupBizs()

	log.Printf(
		"信息: [Business Limiter] 初始化完成。全局限制: %.2f req/s, 峰值: %d。IP 默认限制: %.2f req/s, 峰值: %d",
		globalRate, globalBurst, brl.ipDefaultRate, brl.ipDefaultBurst,
	)

	return brl
}

// loadIPDefaultSettings 从配置服务加载 IP 维度的默认速率限制。
func (brl *BusinessRateLimiter) loadIPDefaultSettings() {
	settings, err := brl.configService.GetIPLimitSettings(context.Background())
	if err == nil && settings != nil {
		brl.ipDefaultRate = rate.Limit(settings.RateLimitPerMinute / 60.0)
		brl.ipDefaultBurst = settings.BurstSize
		log.Printf("信息: [Business Limiter] 已从数据库加载 IP 速率限制默认值 (Rate: %.2f/min, Burst: %d)", settings.RateLimitPerMinute, settings.BurstSize)
	} else if err != nil {
		log.Printf("警告: [Business Limiter] 从数据库加载 IP 速率限制默认值失败: %v。将使用硬编码的默认值。", err)
	}
}

// cleanupIPs 按固定间隔删除 15 分钟内未访问的 IP 限速器实例。
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

// cleanupUsers 按固定间隔删除 15 分钟内未访问的用户限速器实例。
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

// cleanupBizs 按固定间隔删除 15 分钟内未访问的业务限速器实例。
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
//  模块化中间件方法
// ==================================================================

// Global 返回全局限速中间件，限制整体 QPS。
func (brl *BusinessRateLimiter) Global(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !brl.globalLimiter.Allow() {
			errResp(w, http.StatusTooManyRequests, "系统繁忙，请稍后再试 (global limit)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PerIP 返回基于客户端 IP 的限速中间件。
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

// PerUser 返回基于用户 ID 的限速中间件；未认证用户直接放行。
func (brl *BusinessRateLimiter) PerUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := service.ClaimFrom(r)
		if claims == nil {
			next.ServeHTTP(w, r)
			return
		}

		userID := claims.ID
		brl.userMu.Lock()
		entry, exists := brl.userLimiters[userID]

		if !exists {
			rateLimit, burstSize := brl.userDefaultRate, brl.userDefaultBurst
			if userSettings, err := brl.configService.GetUserLimitSettings(r.Context(), userID); err == nil && userSettings != nil {
				rateLimit = rate.Limit(userSettings.RateLimitPerSecond)
				burstSize = userSettings.BurstSize
				log.Printf("调试: [Business Limiter] 为用户 ID %d 加载特定速率限制: %.2f req/s, burst %d", userID, rateLimit, burstSize)
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

// PerBiz 返回基于业务名称的限速中间件，对 V1 API 支持从 JSON 请求体提取 biz_name 字段。
func (brl *BusinessRateLimiter) PerBiz(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bizName string

		// 优先尝试从 JSON Body 中解析 biz_name。
		if r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				log.Printf("WARN: [PerBiz Limiter] 读取请求体失败: %v", err)
				next.ServeHTTP(w, r)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var extractor struct {
				BizName string `json:"biz_name"`
			}
			if err := json.Unmarshal(bodyBytes, &extractor); err == nil {
				bizName = extractor.BizName
			}
		}

		// 回退到 URL 参数方式。
		if bizName == "" {
			bizName = r.URL.Query().Get("biz")
		}
		if bizName == "" {
			next.ServeHTTP(w, r)
			return
		}

		brl.bizMu.Lock()
		entry, exists := brl.bizLimiters[bizName]
		if !exists {
			rateLimit, burstSize := brl.userDefaultRate, brl.userDefaultBurst
			if bizSettings, err := brl.configService.GetBizRateLimitSettings(r.Context(), bizName); err == nil && bizSettings != nil {
				rateLimit = rate.Limit(bizSettings.RateLimitPerSecond)
				burstSize = bizSettings.BurstSize
				log.Printf("调试: [Business Limiter] 为业务组 %s 加载特定速率限制: %.2f req/s, burst %d", bizName, rateLimit, burstSize)
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

// FullBusinessChain 组合全局、IP、用户与业务四级限制，适用于核心业务接口。
func (brl *BusinessRateLimiter) FullBusinessChain(next http.Handler) http.Handler {
	return brl.Global(brl.PerIP(brl.PerUser(brl.PerBiz(next))))
}

// LightweightChain 组合全局与 IP 两级限制，适用于公共或轻量级接口。
func (brl *BusinessRateLimiter) LightweightChain(next http.Handler) http.Handler {
	return brl.Global(brl.PerIP(next))
}

// ==================================================================
//  Tactic 1: 按 IP 地址的严格速率限制器 (Strict Per-IP Rate Limiter)
// ==================================================================

// IPRateLimiter 提供简单的严格 IP 限速策略，实现方式与 BusinessRateLimiter 独立。
type IPRateLimiter struct {
	limiters map[string]*limiterEntry
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
}

// getClientIP 提取客户端真实 IP，优先使用 X-Forwarded-For 与 X-Real-IP 头。
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

// getLimiter 返回指定 IP 对应的限速器，如不存在则按默认配置创建。
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

// cleanupDaemon 定期清理长时间未使用的 IP 限速器实例。
//
//nolint:unused
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

// Middleware 将 IPRateLimiter 封装为 HTTP 中间件。
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

// LoginFailureLock 实现基于登录失败次数的临时账户锁定策略。
type LoginFailureLock struct {
	failureCache    *cache.Cache
	maxFailures     int
	lockoutDuration time.Duration
}

// statusRecorder 封装 ResponseWriter 以捕获 HTTP 状态码。
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

// Middleware 返回登录失败锁定中间件，用于包裹登录处理器。
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
			log.Printf("警告: [Login Lock] 已锁定的账户 '%s' (来自 IP: %s) 再次尝试登录。", username, ip)
			errResp(w, http.StatusUnauthorized, "用户名或密码无效")
			return
		}

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		if recorder.status == http.StatusUnauthorized {
			failureKey := "failures:" + ip + ":" + username
			err := l.failureCache.Increment(failureKey, int64(1))
			if err != nil {
				l.failureCache.Set(failureKey, int64(1), cache.DefaultExpiration)
			}
			var currentFailures int
			if x, found := l.failureCache.Get(failureKey); found {
				currentFailures = int(x.(int64))
			}
			log.Printf("信息: [Login Failure] 账户 '%s' (来自 IP: %s) 登录失败，当前失败次数: %d", username, ip, currentFailures)

			if currentFailures >= l.maxFailures {
				l.failureCache.Set(lockKey, true, l.lockoutDuration)
				l.failureCache.Delete(failureKey)
				log.Printf("警告: [Login Lock] 账户 '%s' (来自 IP: %s) 已被临时锁定 %v。", username, ip, l.lockoutDuration)
			}
		}

		if recorder.status == http.StatusOK {
			failureKey := "failures:" + ip + ":" + username
			l.failureCache.Delete(failureKey)
		}
	})
}

// errResp 输出统一的 JSON 格式错误响应。
func errResp(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// SetIPDefaultRateForTest 为测试提供的辅助函数，用于动态修改 IP 默认速率配置。
// 注意：此函数仅应在测试代码中调用。
func (brl *BusinessRateLimiter) SetIPDefaultRateForTest(newRate float64, burst int) {
	brl.ipMu.Lock()
	defer brl.ipMu.Unlock()
	brl.ipDefaultRate = rate.Limit(newRate)
	brl.ipDefaultBurst = burst
}
