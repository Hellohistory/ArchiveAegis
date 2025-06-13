// Package aegserver aegis_business_limiter.go
package aegserver

import (
	"ArchiveAegis/internal/aegauth"
	"ArchiveAegis/internal/aeglogic"

	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ============================================================================
//  业务性能限制器 (Business Performance Limiter)
// ============================================================================

// BusinessRateLimiter 是一个统一的结构，管理所有业务性能相关的速率限制。
// 它可以根据全局、IP、用户和业务组等多个维度进行限制。
type BusinessRateLimiter struct {
	configService aeglogic.QueryAdminConfigService // 依赖注入，用于从数据库获取配置

	// 全局限制器
	globalLimiter *rate.Limiter

	// Per-IP 限制器相关字段
	ipLimiters     map[string]*limiterEntry
	ipMu           sync.Mutex
	ipDefaultRate  rate.Limit // IP限制的默认速率 (每秒)
	ipDefaultBurst int        // IP限制的默认峰值

	// Per-User 限制器相关字段
	userLimiters     map[int64]*limiterEntry
	userMu           sync.Mutex
	userDefaultRate  rate.Limit // 已认证用户的默认速率
	userDefaultBurst int        // 已认证用户的默认峰值

	// Per-Biz 限制器相关字段
	bizLimiters map[string]*limiterEntry
	bizMu       sync.Mutex
}

// NewBusinessRateLimiter 创建一个新的、功能完备的业务速率限制器。
func NewBusinessRateLimiter(cs aeglogic.QueryAdminConfigService, globalRate float64, globalBurst int) *BusinessRateLimiter {
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

	// 尝试从数据库加载IP限制的默认值，覆盖硬编码的默认值
	brl.loadIPDefaultSettings()

	// 启动后台清理守护进程，为每个动态创建的limiter map清理内存
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
		brl.ipDefaultRate = rate.Limit(settings.RateLimitPerMinute / 60.0) // 将“每分钟”转换为“每秒”
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
		claims := aegauth.ClaimFrom(r)
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

// PerBiz 返回业务组限制中间件
func (brl *BusinessRateLimiter) PerBiz(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bizName := r.URL.Query().Get("biz")
		if bizName == "" { // 如果请求没有biz参数，此中间件直接放行
			next.ServeHTTP(w, r)
			return
		}

		brl.bizMu.Lock()
		entry, exists := brl.bizLimiters[bizName]
		if !exists {
			// 缓存未命中，从数据库加载该业务组的特定配置
			rateLimit, burstSize := brl.userDefaultRate, brl.userDefaultBurst // 若无特定配置，可复用认证用户默认值或设另一套默认值
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
