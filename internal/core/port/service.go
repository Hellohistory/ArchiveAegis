// Package port 定义服务接口，用于解耦核心逻辑与具体实现
// 文件位置: internal/core/port/service.go
package port

import (
	"ArchiveAegis/internal/core/domain"
	"context"
)

// QueryAdminConfigService 定义系统对业务配置的查询与更新能力
type QueryAdminConfigService interface {
	// GetBizQueryConfig 返回指定业务组的查询配置
	GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error)

	// GetAllConfiguredBizNames 返回所有已配置的业务组名称
	GetAllConfiguredBizNames(ctx context.Context) ([]string, error)

	// UpdateBizOverallSettings 更新指定业务组的总体设置
	UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) error

	// UpdateBizSearchableTables 更新指定业务组中可查询的表列表
	UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) error

	// UpdateTableWritePermissions 更新指定业务组中某个表的写权限配置
	UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error

	// UpdateTableFieldSettings 更新指定业务组中某个表的字段设置
	UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error

	// GetDefaultViewConfig 获取指定表的默认视图配置
	GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error)

	// GetAllViewConfigsForBiz 获取某业务组的全部视图配置
	GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error)

	// UpdateAllViewsForBiz 批量更新某业务组的全部视图配置
	UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error

	// GetIPLimitSettings 获取全局 IP 限流设置
	GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error)

	// UpdateIPLimitSettings 更新全局 IP 限流设置
	UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) error

	// GetUserLimitSettings 获取某用户的限流设置
	GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error)

	// UpdateUserLimitSettings 更新某用户的限流设置
	UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error

	// GetBizRateLimitSettings 获取某业务组的限流设置
	GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error)

	// UpdateBizRateLimitSettings 更新某业务组的限流设置
	UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error

	// InvalidateCacheForBiz 清除某业务组的缓存
	InvalidateCacheForBiz(bizName string)

	// InvalidateAllCaches 清除所有业务相关的缓存
	InvalidateAllCaches()
}
