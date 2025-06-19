// Package port file: internal/core/port/service.go
package port

import (
	"ArchiveAegis/internal/core/domain"
	"context"
)

// QueryAdminConfigService 是一个接口，定义了系统获取配置的能力。
// 它现在依赖于 domain 包中的数据结构。
type QueryAdminConfigService interface {
	GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error)
	GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error)
	GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error)
	UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error
	GetAllConfiguredBizNames(ctx context.Context) ([]string, error)
	InvalidateCacheForBiz(bizName string)
	InvalidateAllCaches()

	// GetIPLimitSettings --- 速率限制相关的接口 ---
	GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error)
	UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) error
	GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error)
	UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error
	GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error)
	UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error
}
