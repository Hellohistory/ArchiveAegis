// file: internal/aegmiddleware/limiter_test.go

package aegmiddleware_test

import (
	"ArchiveAegis/internal/aegmiddleware" // 导入被测试的包
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

// ============================================================================
//  测试替身 (Test Doubles) / 模拟对象 (Mocks)
// ============================================================================

// mockAdminConfigService 是 port.QueryAdminConfigService 接口的一个测试替身。
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

// ============================================================================
//  测试辅助函数 (Test Helpers)
// ============================================================================

var testHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
})

func addClaimToContext(r *http.Request, claim *service.Claim) *http.Request {
	ctx := context.WithValue(r.Context(), service.ClaimKey, claim)
	return r.WithContext(ctx)
}

// ============================================================================
//  测试用例 (Test Cases)
// ============================================================================

func TestBusinessRateLimiter_Global(t *testing.T) {
	mockService := &mockAdminConfigService{}
	limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 2, 2)
	middleware := limiter.Global(testHandler)

	t.Run("should allow initial requests", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			rr := httptest.NewRecorder()
			middleware.ServeHTTP(rr, req)
			if status := rr.Code; status != http.StatusOK {
				t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
			}
		}
	})

	t.Run("should block subsequent requests", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if status := rr.Code; status != http.StatusTooManyRequests {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusTooManyRequests)
		}
	})

	t.Run("should allow requests again after delay", func(t *testing.T) {
		time.Sleep(1 * time.Second)
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code after delay: got %v want %v", status, http.StatusOK)
		}
	})
}

func TestBusinessRateLimiter_PerIP(t *testing.T) {
	limiter := aegmiddleware.NewBusinessRateLimiter(nil, 100, 100)
	limiter.SetIPDefaultRateForTest(1, 1)
	middleware := limiter.PerIP(testHandler)

	t.Run("should limit requests from the same IP", func(t *testing.T) {
		req1 := httptest.NewRequest("GET", "/", nil)
		req1.RemoteAddr = "192.0.2.1:12345"
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)
		if rr1.Code != http.StatusOK {
			t.Fatal("First request from IP 1 should be allowed")
		}

		req2 := httptest.NewRequest("GET", "/", nil)
		req2.RemoteAddr = "192.0.2.1:12345"
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)
		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("Second request from IP 1 should be blocked, got %d", rr2.Code)
		}
	})

	t.Run("should not affect requests from a different IP", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.0.2.2:54321"
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if status := rr.Code; status != http.StatusOK {
			t.Errorf("Request from IP 2 should be allowed, but got %v", status)
		}
	})
}

func TestBusinessRateLimiter_PerUser(t *testing.T) {
	claimUser1 := &service.Claim{ID: 1, Role: "user"}
	claimUser2 := &service.Claim{ID: 2, Role: "user"}

	t.Run("should use default limit for user without specific settings", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		for i := 0; i < 5; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req = addClaimToContext(req, claimUser1)
			rr := httptest.NewRecorder()
			middleware.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("Request %d for user 1 should be allowed, got %d", i+1, rr.Code)
			}
		}
	})

	t.Run("should use specific limit for user with settings", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		mockService.GetUserLimitSettingsFunc = func(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
			if userID == 2 {
				return &domain.UserLimitSetting{RateLimitPerSecond: 1.0, BurstSize: 1}, nil
			}
			return nil, nil
		}
		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		req1 := httptest.NewRequest("GET", "/", nil)
		req1 = addClaimToContext(req1, claimUser2)
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)
		if rr1.Code != http.StatusOK {
			t.Fatal("First request for user 2 should be allowed")
		}

		req2 := httptest.NewRequest("GET", "/", nil)
		req2 = addClaimToContext(req2, claimUser2)
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)
		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("Second request for user 2 should be blocked, got %d", rr2.Code)
		}
	})

	t.Run("should not limit unauthenticated users", func(t *testing.T) {
		mockService := &mockAdminConfigService{}
		limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
		middleware := limiter.PerUser(testHandler)

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Unauthenticated request should pass, got %d", rr.Code)
		}
	})
}

func TestBusinessRateLimiter_PerBiz(t *testing.T) {
	mockService := &mockAdminConfigService{}
	limiter := aegmiddleware.NewBusinessRateLimiter(mockService, 100, 100)
	middleware := limiter.PerBiz(testHandler)

	mockService.GetBizRateLimitSettingsFunc = func(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
		if bizName == "sales" || bizName == "inventory" {
			return &domain.BizRateLimitSetting{RateLimitPerSecond: 1.0, BurstSize: 1}, nil
		}
		return nil, nil
	}

	t.Run("should limit biz from JSON body", func(t *testing.T) {
		jsonBody, _ := json.Marshal(map[string]string{"biz_name": "sales"})
		req1 := httptest.NewRequest("POST", "/data/query", bytes.NewBuffer(jsonBody))
		req1.Header.Set("Content-Type", "application/json")
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)
		if rr1.Code != http.StatusOK {
			t.Fatalf("First request for biz 'sales' should be allowed, got %d", rr1.Code)
		}

		jsonBody, _ = json.Marshal(map[string]string{"biz_name": "sales"})
		req2 := httptest.NewRequest("POST", "/data/query", bytes.NewBuffer(jsonBody))
		req2.Header.Set("Content-Type", "application/json")
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)
		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("Second request for biz 'sales' should be blocked, got %d", rr2.Code)
		}
	})

	t.Run("should limit biz from URL query", func(t *testing.T) {
		req1 := httptest.NewRequest("GET", "/some_path?biz=inventory", nil)
		rr1 := httptest.NewRecorder()
		middleware.ServeHTTP(rr1, req1)
		if rr1.Code != http.StatusOK {
			t.Fatal("First GET request for biz 'inventory' should be allowed")
		}

		req2 := httptest.NewRequest("GET", "/some_path?biz=inventory", nil)
		rr2 := httptest.NewRecorder()
		middleware.ServeHTTP(rr2, req2)
		if rr2.Code != http.StatusTooManyRequests {
			t.Errorf("Second GET request for biz 'inventory' should be blocked, got %d", rr2.Code)
		}
	})

	t.Run("should not affect other biz", func(t *testing.T) {
		jsonBody, _ := json.Marshal(map[string]string{"biz_name": "marketing"})
		req := httptest.NewRequest("POST", "/data/query", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Request for biz 'marketing' should be allowed, got %d", rr.Code)
		}
	})

	t.Run("should pass if no biz name is provided", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/no_biz_path", nil)
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("Request without biz name should pass, got %d", rr.Code)
		}
	})
}
