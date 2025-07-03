# Git 提交规范 / Git Commit Convention

## 📌 格式 / Format

```
<branch-name>
[类型] 提交简要说明 / [Type] Brief Description
```

* 分支名：当前所在分支的逻辑名称，例如：`feature-auth`、`fix-router`
* 类型：方括号内的中文（可选英文对照）
* 提交说明：一句话描述改动内容，尽量控制在 50 字以内

---

## 📌 类型规范 / Commit Types

| 中文类型  | 英文对应        | 用途说明 / Description       |
| ----- | ----------- | ------------------------ |
| \[新增] | \[feat]     | 新增功能 / New feature       |
| \[修复] | \[fix]      | 修复问题 / Bug fix           |
| \[优化] | \[perf]     | 性能优化 / Performance       |
| \[重构] | \[refactor] | 重构代码 / Code refactor     |
| \[文档] | \[docs]     | 文档修改 / Documentation     |
| \[格式] | \[style]    | 代码格式 / Code style only   |
| \[测试] | \[test]     | 添加或修改测试 / Tests          |
| \[依赖] | \[chore]    | 构建工具、依赖管理 / Misc changes |
| \[构建] | \[build]    | 编译相关改动 / Build system    |
| \[回滚] | \[revert]   | 回退某次提交 / Revert commit   |
| \[CI] | \[ci]       | 持续集成配置 / CI config       |

---

## 📦 示例 / Examples

```
feature-auth
[新增] 支持用户登录接口 / Add user login endpoint

fix-router
[修复] 修复初始化流程中的 nil 异常 / Fix nil panic during setup

docs-readme
[文档] 完善 README 安装说明 / Improve installation guide in README

perf-query
[优化] 减少数据库查询次数 / Reduce redundant DB queries

refactor-plugin
[重构] 拆分插件启动流程 / Refactor plugin bootstrap logic
```