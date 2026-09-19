// Package migrations 以嵌入文件形式提供数据库迁移脚本，
// 使服务与迁移工具无需依赖外部 sqlite3 命令行。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
