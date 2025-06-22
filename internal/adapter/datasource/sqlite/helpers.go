// Package sqlite file: internal/adapter/datasource/sqlite/helpers.go
package sqlite

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// buildQuerySQL 根据管理员配置动态构建数据查询的 SQL 语句
func buildQuerySQL(
	tableName string,
	selectDBFields []string,
	queryParams []queryParam,
	page int,
	size int,
) (string, []any, error) {
	if tableName == "" || len(selectDBFields) == 0 {
		return "", nil, errors.New("表名和查询字段不能为空 (buildQuerySQL)")
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 2000 {
		size = 50
	}

	selectClause := `"` + strings.Join(selectDBFields, `", "`) + `"`
	whereClause, whereArgs, err := buildWhereClause(queryParams)
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(selectClause)
	sb.WriteString(fmt.Sprintf(" FROM %q", tableName))
	if whereClause != "" {
		sb.WriteString(" ")
		sb.WriteString(whereClause)
	}
	sb.WriteString(" LIMIT ? OFFSET ?")

	args := append(whereArgs, size, (page-1)*size)
	return sb.String(), args, nil
}

// buildCountSQL 用于构建计算总数的SQL查询
func buildCountSQL(tableName string, queryParams []queryParam) (string, []any, error) {
	if tableName == "" {
		return "", nil, errors.New("表名不能为空 (buildCountSQL)")
	}
	whereClause, whereArgs, err := buildWhereClause(queryParams)
	if err != nil {
		return "", nil, err
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SELECT COUNT(*) FROM %q", tableName))
	if whereClause != "" {
		sb.WriteString(" ")
		sb.WriteString(whereClause)
	}
	return sb.String(), whereArgs, nil
}

// buildInsertSQL 安全地构建 INSERT 语句
func buildInsertSQL(tableName string, data map[string]interface{}) (string, []interface{}, error) {
	if len(data) == 0 {
		return "", nil, errors.New("INSERT 操作需要提供数据")
	}
	var cols, placeholders []string
	var args []interface{}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cols = append(cols, fmt.Sprintf("%q", k))
		placeholders = append(placeholders, "?")
		args = append(args, data[k])
	}
	query := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)", tableName, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return query, args, nil
}

// buildUpdateSQL 安全地构建 UPDATE 语句
func buildUpdateSQL(tableName string, data map[string]interface{}, filters []queryParam) (string, []interface{}, error) {
	if len(data) == 0 {
		return "", nil, errors.New("UPDATE 操作需要提供更新数据")
	}
	var setClauses []string
	var args []interface{}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		setClauses = append(setClauses, fmt.Sprintf("%q = ?", k))
		args = append(args, data[k])
	}
	whereClause, whereArgs, err := buildWhereClause(filters)
	if err != nil {
		return "", nil, err
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("UPDATE %q SET %s %s", tableName, strings.Join(setClauses, ", "), whereClause)
	return query, args, nil
}

// buildDeleteSQL 安全地构建 DELETE 语句
func buildDeleteSQL(tableName string, filters []queryParam) (string, []interface{}, error) {
	whereClause, whereArgs, err := buildWhereClause(filters)
	if err != nil {
		return "", nil, err
	}
	if whereClause == "" {
		return "", nil, errors.New("出于安全考虑，不允许无条件的DELETE操作")
	}
	query := fmt.Sprintf("DELETE FROM %q %s", tableName, whereClause)
	return query, whereArgs, nil
}

// buildWhereClause 是一个用于构建 WHERE 子句的通用辅助函数
func buildWhereClause(filters []queryParam) (string, []interface{}, error) {
	if len(filters) == 0 {
		return "", make([]interface{}, 0), nil
	}

	var conditions []string
	args := make([]interface{}, 0, len(filters))

	for i, p := range filters {
		var operator, value string
		if p.Fuzzy {
			operator = "LIKE"
			likeValue := strings.ReplaceAll(p.Value, `\`, `\\`)
			likeValue = strings.ReplaceAll(likeValue, `%`, `\%`)
			likeValue = strings.ReplaceAll(likeValue, `_`, `\_`)
			value = "%" + likeValue + "%"
		} else {
			operator = "="
			value = p.Value
		}
		conditions = append(conditions, fmt.Sprintf("%q %s ?", p.Field, operator))
		args = append(args, value)
		if i < len(filters)-1 {
			logic := strings.ToUpper(p.Logic)
			if logic == "AND" || logic == "OR" {
				conditions = append(conditions, logic)
			} else if logic != "" {
				return "", nil, fmt.Errorf("无效的逻辑操作符: %s", p.Logic)
			}
		}
	}
	return "WHERE " + strings.Join(conditions, " "), args, nil
}
