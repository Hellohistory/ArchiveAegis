// Package service internal/service/rate_limit_config.go
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"

	"ArchiveAegis/internal/core/domain"
)

// GetIPLimitSettings 获取全局IP速率限制配置。
func (s *AdminConfigServiceImpl) GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error) {
	settings := &domain.IPLimitSetting{}

	query := "SELECT key, value FROM global_settings WHERE key IN (?, ?)"
	rows, err := s.db.QueryContext(ctx, query, "ip_rate_limit_per_minute", "ip_burst_size")
	if err != nil {
		return nil, fmt.Errorf("查询全局IP限制配置失败: %w", err)
	}

	// 安全释放资源并捕获关闭错误
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.Printf("警告: 关闭 rows 失败 (IPLimitSettings 查询): %v", errClose)
		}
	}()

	var (
		hasRate  bool
		hasBurst bool
	)

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("扫描 IP 限制配置失败: %w", err)
		}

		switch key {
		case "ip_rate_limit_per_minute":
			if v, errConv := strconv.ParseFloat(value, 64); errConv == nil {
				settings.RateLimitPerMinute = v
				hasRate = true
			} else {
				log.Printf("警告: ip_rate_limit_per_minute 配置值非法: '%s'", value)
			}
		case "ip_burst_size":
			if v, errConv := strconv.Atoi(value); errConv == nil {
				settings.BurstSize = v
				hasBurst = true
			} else {
				log.Printf("警告: ip_burst_size 配置值非法: '%s'", value)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 IP 限制配置失败: %w", err)
	}

	// 未配置任何有效项，视为未设置
	if !hasRate && !hasBurst {
		log.Printf("信息: 系统中未找到有效的 IP 限速设置")
		return nil, nil
	}

	return settings, nil
}

// UpdateIPLimitSettings 更新全局IP速率限制配置。
// 使用 UPSERT 确保配置的存在性或更新。
func (s *AdminConfigServiceImpl) UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) (err error) {
	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (UpdateIPLimitSettings): %w", err)
	}

	// 管理事务的提交/回滚行为
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateIPLimitSettings 触发 panic，事务已回滚: %v", p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateIPLimitSettings 执行失败，事务已回滚: %v", err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (UpdateIPLimitSettings): %w", commitErr)
			}
		}
	}()

	// 预编译 UPSERT 语句
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO global_settings (key, value)
         VALUES (?, ?)
         ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return fmt.Errorf("准备 UPSERT 语句失败: %w", err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.Printf("警告: 关闭 stmt 失败 (UpdateIPLimitSettings): %v", errClose)
		}
	}()

	// 写入 ip_rate_limit_per_minute
	rateStr := fmt.Sprintf("%.4f", settings.RateLimitPerMinute)
	if _, err = stmt.ExecContext(ctx, "ip_rate_limit_per_minute", rateStr); err != nil {
		return fmt.Errorf("写入 ip_rate_limit_per_minute 失败，值为 '%s': %w", rateStr, err)
	}

	// 写入 ip_burst_size
	burstStr := strconv.Itoa(settings.BurstSize)
	if _, err = stmt.ExecContext(ctx, "ip_burst_size", burstStr); err != nil {
		return fmt.Errorf("写入 ip_burst_size 失败，值为 '%s': %w", burstStr, err)
	}

	log.Printf("信息: 全局 IP 限速配置已更新 (Rate: %s, Burst: %s)", rateStr, burstStr)
	return nil // 事务提交由 defer 完成
}

// GetUserLimitSettings 获取特定用户的速率限制配置。
func (s *AdminConfigServiceImpl) GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
	var rateLimit sql.NullFloat64
	var burstSize sql.NullInt64
	query := "SELECT rate_limit_per_second, burst_size FROM _user WHERE id = ?"
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&rateLimit, &burstSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 用户未设置个性化限制
		}
		return nil, fmt.Errorf("数据库查询用户ID %d 速率限制失败: %w", userID, err)
	}
	if !rateLimit.Valid || !burstSize.Valid {
		return nil, nil // 数据库中存在记录但值无效
	}
	return &domain.UserLimitSetting{
		RateLimitPerSecond: rateLimit.Float64,
		BurstSize:          int(burstSize.Int64),
	}, nil
}

// UpdateUserLimitSettings 更新特定用户的速率限制配置。
func (s *AdminConfigServiceImpl) UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error {
	query := "UPDATE _user SET rate_limit_per_second = ?, burst_size = ? WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, settings.RateLimitPerSecond, settings.BurstSize, userID)
	if err != nil {
		return fmt.Errorf("数据库更新用户ID %d 速率限制失败: %w", userID, err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("用户ID %d 不存在，无法更新其速率限制", userID)
	}
	log.Printf("信息: 用户ID %d 的速率限制已更新 (Rate: %.2f, Burst: %d)", userID, settings.RateLimitPerSecond, settings.BurstSize)
	return nil
}

// GetBizRateLimitSettings 获取特定业务组的速率限制配置。
func (s *AdminConfigServiceImpl) GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
	query := "SELECT rate_limit_per_second, burst_size FROM biz_ratelimit_settings WHERE biz_name = ?"
	setting := &domain.BizRateLimitSetting{}
	err := s.db.QueryRowContext(ctx, query, bizName).Scan(&setting.RateLimitPerSecond, &setting.BurstSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 业务组未设置个性化限制
		}
		return nil, fmt.Errorf("数据库查询业务组 '%s' 速率限制失败: %w", bizName, err)
	}
	return setting, nil
}

// UpdateBizRateLimitSettings 更新特定业务组的速率限制配置。
// 使用 UPSERT 确保配置的存在性或更新。
func (s *AdminConfigServiceImpl) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error {
	query := `
        INSERT INTO biz_ratelimit_settings (biz_name, rate_limit_per_second, burst_size) 
        VALUES (?, ?, ?) 
        ON CONFLICT(biz_name) DO UPDATE SET 
            rate_limit_per_second = excluded.rate_limit_per_second, 
            burst_size = excluded.burst_size`
	_, err := s.db.ExecContext(ctx, query, bizName, settings.RateLimitPerSecond, settings.BurstSize)
	if err != nil {
		return fmt.Errorf("数据库更新业务组 '%s' 速率限制失败: %w", bizName, err)
	}
	log.Printf("信息: 业务组 '%s' 的速率限制已更新 (Rate: %.2f, Burst: %d)", bizName, settings.RateLimitPerSecond, settings.BurstSize)
	return nil
}
