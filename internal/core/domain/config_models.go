// Package domain file: internal/core/domain/config_models.go
package domain

// BizOverallSettings 定义了业务组的总体设置，用于更新操作。
// 使用指针类型是为了方便地判断客户端是否传递了某个字段，从而实现部分更新。
type BizOverallSettings struct {
	IsPubliclySearchable *bool   `json:"is_publicly_searchable"`
	DefaultQueryTable    *string `json:"default_query_table"`
}

// BizQueryConfig 定义了单个业务组的完整查询配置
type BizQueryConfig struct {
	BizName              string                  `json:"biz_name"`
	IsPubliclySearchable bool                    `json:"is_publicly_searchable"`
	DefaultQueryTable    string                  `json:"default_query_table"`
	Tables               map[string]*TableConfig `json:"tables"`
}

// TableConfig 定义了单个表的查询和写操作配置
type TableConfig struct {
	TableName    string                  `json:"table_name"`
	IsSearchable bool                    `json:"is_searchable"`
	Fields       map[string]FieldSetting `json:"fields"`
	AllowCreate  bool                    `json:"allow_create"`
	AllowUpdate  bool                    `json:"allow_update"`
	AllowDelete  bool                    `json:"allow_delete"`
}

// FieldSetting 定义了单个字段的查询和返回配置
type FieldSetting struct {
	FieldName    string `json:"field_name"`
	IsSearchable bool   `json:"is_searchable"`
	IsReturnable bool   `json:"is_returnable"`
	DataType     string `json:"dataType"`
}

// ViewConfig 是一个完整的视图配置对象，代表一种展示方案
type ViewConfig struct {
	ViewName    string      `json:"view_name"`
	ViewType    string      `json:"view_type"`
	DisplayName string      `json:"display_name"`
	IsDefault   bool        `json:"is_default"`
	Binding     ViewBinding `json:"binding"`
}

// ViewBinding 包含了所有可能的视图类型的绑定配置
type ViewBinding struct {
	Card  *CardBinding  `json:"card,omitempty"`
	Table *TableBinding `json:"table,omitempty"`
}

// CardBinding 定义了卡片视图的字段如何与数据源绑定
type CardBinding struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	ImageUrl    string `json:"imageUrl"`
	Tag         string `json:"tag"`
}

// TableBinding 定义了表格视图的配置
type TableBinding struct {
	Columns []TableColumnBinding `json:"columns"`
}

// TableColumnBinding 定义了表格视图中单列的配置
type TableColumnBinding struct {
	Field       string `json:"field"`
	DisplayName string `json:"displayName"`
	Format      string `json:"format,omitempty"`
}

// IPLimitSetting 定义了全局IP速率限制的配置
type IPLimitSetting struct {
	RateLimitPerMinute float64 `json:"rate_limit_per_minute"`
	BurstSize          int     `json:"burst_size"`
}

// UserLimitSetting 定义了单个用户的速率限制配置
type UserLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"`
	BurstSize          int     `json:"burst_size"`
}

// BizRateLimitSetting 定义了单个业务组的速率限制配置
type BizRateLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"`
	BurstSize          int     `json:"burst_size"`
}
