// Package aegserver aegis_security_limiter.go
package aegserver

import (
	"bufio"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"golang.org/x/time/rate"
)

// ==================================================================
//  Tactic 1: 按 IP 地址的严格速率限制器 (Strict Per-IP Rate Limiter)
// ==================================================================

// limiterEntry 存储限制器和最后访问时间，用于清理
type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

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
