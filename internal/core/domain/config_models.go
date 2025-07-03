// Package domain 提供核心业务模型定义
// 文件位置: internal/core/domain/config_models.go
package domain

// BizOverallSettings 表示业务组的总体设置，用于支持部分字段更新
type BizOverallSettings struct {
	IsPubliclySearchable *bool   `json:"is_publicly_searchable"` // 是否允许公开搜索
	DefaultQueryTable    *string `json:"default_query_table"`    // 默认查询的表名
}

// BizQueryConfig 表示业务组的完整查询配置
type BizQueryConfig struct {
	BizName              string                  `json:"biz_name"`               // 业务组名称
	IsPubliclySearchable bool                    `json:"is_publicly_searchable"` // 是否允许公开搜索
	DefaultQueryTable    string                  `json:"default_query_table"`    // 默认查询的表名
	Tables               map[string]*TableConfig `json:"tables"`                 // 表配置映射
}

// TableConfig 表示单个表的查询和写操作配置
type TableConfig struct {
	TableName    string                  `json:"table_name"`    // 表名称
	IsSearchable bool                    `json:"is_searchable"` // 是否可被查询
	Fields       map[string]FieldSetting `json:"fields"`        // 字段配置
	AllowCreate  bool                    `json:"allow_create"`  // 是否允许新增数据
	AllowUpdate  bool                    `json:"allow_update"`  // 是否允许更新数据
	AllowDelete  bool                    `json:"allow_delete"`  // 是否允许删除数据
}

// FieldSetting 表示单个字段的查询与返回配置
type FieldSetting struct {
	FieldName    string `json:"field_name"`    // 字段名称
	IsSearchable bool   `json:"is_searchable"` // 是否可作为查询条件
	IsReturnable bool   `json:"is_returnable"` // 是否包含在返回结果中
	DataType     string `json:"dataType"`      // 字段的数据类型
}

// ViewConfig 表示一个完整的视图配置，用于定义展示方式
type ViewConfig struct {
	ViewName    string      `json:"view_name"`    // 视图名称
	ViewType    string      `json:"view_type"`    // 视图类型
	DisplayName string      `json:"display_name"` // 展示名称
	IsDefault   bool        `json:"is_default"`   // 是否为默认视图
	Binding     ViewBinding `json:"binding"`      // 视图绑定配置
}

// ViewBinding 表示各类视图类型的绑定配置
type ViewBinding struct {
	Card  *CardBinding  `json:"card,omitempty"`  // 卡片视图绑定配置
	Table *TableBinding `json:"table,omitempty"` // 表格视图绑定配置
}

// CardBinding 表示卡片视图的字段绑定方式
type CardBinding struct {
	Title       string `json:"title"`       // 卡片标题字段
	Subtitle    string `json:"subtitle"`    // 卡片副标题字段
	Description string `json:"description"` // 卡片描述字段
	ImageUrl    string `json:"imageUrl"`    // 卡片图像字段
	Tag         string `json:"tag"`         // 卡片标签字段
}

// TableBinding 表示表格视图的配置
type TableBinding struct {
	Columns []TableColumnBinding `json:"columns"` // 表格列配置
}

// TableColumnBinding 表示表格中单列的配置
type TableColumnBinding struct {
	Field       string `json:"field"`            // 字段名
	DisplayName string `json:"displayName"`      // 显示名称
	Format      string `json:"format,omitempty"` // 格式化方式（可选）
}

// IPLimitSetting 表示全局 IP 限速配置
type IPLimitSetting struct {
	RateLimitPerMinute float64 `json:"rate_limit_per_minute"` // 每分钟最大请求次数
	BurstSize          int     `json:"burst_size"`            // 突发请求上限
}

// UserLimitSetting 表示单个用户的限速配置
type UserLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"` // 每秒最大请求次数
	BurstSize          int     `json:"burst_size"`            // 突发请求上限
}

// BizRateLimitSetting 表示业务组维度的限速配置
type BizRateLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"` // 每秒最大请求次数
	BurstSize          int     `json:"burst_size"`            // 突发请求上限
}
