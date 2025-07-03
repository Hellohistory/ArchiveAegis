// Package sqlite 提供 SQLite 数据源的辅助 SQL 构建函数
// 文件位置: internal/adapter/datasource/sqlite/helpers.go
package sqlite

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// buildQuerySQL 构建 SELECT 查询语句及其参数列表
func buildQuerySQL(tableName string, fields []string, params []queryParam) (string, []any, error) {
	if tableName == "" || len(fields) == 0 {
		return "", nil, errors.New("表名和查询字段不能为空 (buildQuerySQL)")
	}

	quotedFields := make([]string, len(fields))
	sort.Strings(fields)
	for i, f := range fields {
		quotedFields[i] = fmt.Sprintf("%q", f)
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString("SELECT ")
	queryBuilder.WriteString(strings.Join(quotedFields, ", "))
	queryBuilder.WriteString(fmt.Sprintf(" FROM %q", tableName))

	whereClause, whereArgs, err := buildWhereClause(params)
	if err != nil {
		return "", nil, err
	}
	if whereClause != "" {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(whereClause)
	}

	queryBuilder.WriteString(" ORDER BY id ASC")

	return queryBuilder.String(), whereArgs, nil
}

// buildCountSQL 构建 SELECT COUNT(*) 查询语句及其参数列表
func buildCountSQL(tableName string, params []queryParam) (string, []any, error) {
	if tableName == "" {
		return "", nil, errors.New("表名不能为空 (buildCountSQL)")
	}

	var queryBuilder strings.Builder
	queryBuilder.WriteString(fmt.Sprintf("SELECT COUNT(*) FROM %q", tableName))

	whereClause, whereArgs, err := buildWhereClause(params)
	if err != nil {
		return "", nil, err
	}
	if whereClause != "" {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(whereClause)
	}

	return queryBuilder.String(), whereArgs, nil
}

// buildWhereClause 构建 WHERE 子句及其参数列表
func buildWhereClause(filters []queryParam) (string, []interface{}, error) {
	if len(filters) == 0 {
		return "", make([]interface{}, 0), nil
	}

	var conditions []string
	var args []interface{}

	for i, p := range filters {
		quotedField := fmt.Sprintf("%q", p.Field)
		var operator, value string

		if p.Fuzzy {
			operator = "LIKE"
			escapedValue := strings.ReplaceAll(p.Value, `\`, `\\`)
			escapedValue = strings.ReplaceAll(escapedValue, `%`, `\%`)
			escapedValue = strings.ReplaceAll(escapedValue, `_`, `\_`)
			value = "%" + escapedValue + "%"
		} else {
			operator = "="
			value = p.Value
		}
		conditions = append(conditions, fmt.Sprintf("%s %s ?", quotedField, operator))
		args = append(args, value)

		if i < len(filters)-1 {
			nextLogic := strings.ToUpper(filters[i+1].Logic)
			if nextLogic == "OR" {
				conditions = append(conditions, "OR")
			} else {
				conditions = append(conditions, "AND")
			}
		}
	}
	return "WHERE " + strings.Join(conditions, " "), args, nil
}

// buildInsertSQL 构建 INSERT INTO 语句及其参数列表
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

// buildUpdateSQL 构建 UPDATE 语句及其参数列表
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
	if whereClause == "" {
		return "", nil, errors.New("出于安全考虑，不允许无条件的UPDATE操作")
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("UPDATE %q SET %s %s", tableName, strings.Join(setClauses, ", "), whereClause)
	return query, args, nil
}

// buildDeleteSQL 构建 DELETE FROM 语句及其参数列表
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
