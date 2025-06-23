// file: internal/aegmiddleware/limiter_test.go

package aegmiddleware_test

import (
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockAdminConfigService struct {
	GetIPLimitSettingsFunc      func(ctx context.Context) (*domain.IPLimitSetting, error)
	GetUserLimitSettingsFunc    func(ctx context.Context, userID int64) (*domain.UserLimitSetting, error)
	GetBizRateLimitSettingsFunc func(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error)
}

func (m *mockAdminConfigService) GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error) {
	if m.GetIPLimitSettingsFunc != nil {
		return m.GetIPLimitSettingsFunc(ctx)
	}
	return nil, nil
}
func (m *mockAdminConfigService) GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
	if m.GetUserLimitSettingsFunc != nil {
		return m.GetUserLimitSettingsFunc(ctx, userID)
	}
	return nil, nil
}
func (m *mockAdminConfigService) GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
	if m.GetBizRateLimitSettingsFunc != nil {
		return m.GetBizRateLimitSettingsFunc(ctx, bizName)
	}
	return nil, nil
}

// 其余方法在当前测试无关紧要，直接返回零值。
func (m *mockAdminConfigService) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) error {
	return nil
}
func (m *mockAdminConfigService) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error {
	return nil
}
func (m *mockAdminConfigService) UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) InvalidateCacheForBiz(bizName string) {}
func (m *mockAdminConfigService) InvalidateAllCaches()                 {}

// 一个最小化的处理器，便于验证限流行为。
var testHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
})

// 将 Claim 嵌入请求上下文的辅助函数。
func addClaimToContext(r *http.Request, claim *service.Claim) *http.Request {
	ctx := context.WithValue(r.Context(), service.ClaimKey, claim)
	return r.WithContext(ctx)
}

// Global（全局）限流

func TestBusinessRateLimiter_Global(t *testing.T) {
	mockService := &mockAdminConfigService{}
	limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 2, 2)
	middleware := limiter.Global(testHandler)

	t.Run("允许初始请求", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rr := httptest.NewRecorder()

			middleware.ServeHTTP(rr, req)

			if status := rr.Code; status != http.StatusOK {
				t.Errorf("第 %d 次请求返回状态码错误: 得到 %v, 期望 %v", i+1, status, http.StatusOK)
			}
		}
	})

	t.Run("超过限额后阻止", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()

		middleware.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusTooManyRequests {
			t.Errorf("超额请求应被阻止: 得到 %v, 期望 %v", status, http.StatusTooManyRequests)
		}
	})

	t.Run("时间窗口过后重新放行", func(t *testing.T) {
		time.Sleep(1 * time.Second)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()

		middleware.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("窗口结束后请求应放行: 得到 %v, 期望 %v", status, http.StatusOK)
		}
	})
}

// PerIP（按 IP）限流

func TestBusinessRateLimiter_PerIP(t *testing.T) {
	limiter := aegmiddleware.NewBusinessRateLimiter(nil, 100, 100)
	limiter.SetIPDefaultRateForTest(1, 1) // 设置更严的默认值，方便触发
	middleware := limiter.PerIP(testHandler)

	t.Run("同一 IP 被限流", func(t *testing.T) {
		req1 := httptest.NewRequest(http.MethodGet, "/", nil)
		req1.RemoteAddr = "192.0.2.1:12345"
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)

		if rr1.Code != http.StatusOK {
			t.Fatalf("第一次请求应通过, 得到 %d", rr1.Code)
		}

		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.RemoteAddr = "192.0.2.1:12345"
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)

		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("第二次请求应被限流, 得到 %d", rr2.Code)
		}
	})

	t.Run("不同 IP 互不影响", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.2:54321"
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("另一 IP 的请求应通过, 得到 %d", status)
		}
	})
}

// PerUser（按用户）限流

func TestBusinessRateLimiter_PerUser(t *testing.T) {
	claimUser1 := &service.Claim{ID: 1, Role: "user"}
	claimUser2 := &service.Claim{ID: 2, Role: "user"}

	t.Run("使用默认限额的用户", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = addClaimToContext(req, claimUser1)
			rr := httptest.NewRecorder()
			middleware.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("第 %d 次请求应通过, 得到 %d", i+1, rr.Code)
			}
		}
	})

	t.Run("使用专属限额的用户", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		mockService.GetUserLimitSettingsFunc = func(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
			if userID == 2 {
				return &domain.UserLimitSetting{RateLimitPerSecond: 1.0, BurstSize: 1}, nil
			}
			return nil, nil
		}

		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		// 第一请求应通过
		req1 := httptest.NewRequest(http.MethodGet, "/", nil)
		req1 = addClaimToContext(req1, claimUser2)
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)

		if rr1.Code != http.StatusOK {
			t.Fatal("第一次请求应通过")
		}

		// 第二请求应被限流
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2 = addClaimToContext(req2, claimUser2)
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)

		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("第二次请求应被限流, 得到 %d", rr2.Code)
		}
	})

	t.Run("未认证用户不受影响", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("未认证请求应通过, 得到 %d", rr.Code)
		}
	})
}

// PerBiz（按业务线）限流

func TestBusinessRateLimiter_PerBiz(t *testing.T) {
	mockService := &mockAdminConfigService{}
	limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
	middleware := limiter.PerBiz(testHandler)

	// 为 "sales" 与 "inventory" 注入专属限流策略
	mockService.GetBizRateLimitSettingsFunc = func(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
		if bizName == "sales" || bizName == "inventory" {
			return &domain.BizRateLimitSetting{RateLimitPerSecond: 1.0, BurstSize: 1}, nil
		}
		return nil, nil
	}

	t.Run("从 JSON Body 中提取 biz", func(t *testing.T) {
		jsonBody, _ := json.Marshal(map[string]string{"biz_name": "sales"})

		// 第一次请求应通过
		req1 := httptest.NewRequest(http.MethodPost, "/data/query", bytes.NewBuffer(jsonBody))
		req1.Header.Set("Content-Type", "application/json")
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)

		if rr1.Code != http.StatusOK {
			t.Fatalf("第一次请求应通过, 得到 %d", rr1.Code)
		}

		// 第二次请求应被限流
		req2 := httptest.NewRequest(http.MethodPost, "/data/query", bytes.NewBuffer(jsonBody))
		req2.Header.Set("Content-Type", "application/json")
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)

		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("第二次请求应被限流, 得到 %d", rr2.Code)
		}
	})

	t.Run("从 URL Query 中提取 biz", func(t *testing.T) {
		req1 := httptest.NewRequest(http.MethodGet, "/some_path?biz=inventory", nil)
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)

		if rr1.Code != http.StatusOK {
			t.Fatal("第一次请求应通过")
		}

		req2 := httptest.NewRequest(http.MethodGet, "/some_path?biz=inventory", nil)
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)

		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("第二次请求应被限流, 得到 %d", rr2.Code)
		}
	})

	t.Run("无关业务线不受影响", func(t *testing.T) {
		jsonBody, _ := json.Marshal(map[string]string{"biz_name": "marketing"})
		req := httptest.NewRequest(http.MethodPost, "/data/query", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("不在限流列表中的 biz 应通过, 得到 %d", rr.Code)
		}
	})

	t.Run("未提供 biz 时直接放行", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/no_biz_path", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("未指定 biz 的请求应通过, 得到 %d", rr.Code)
		}
	})
}
