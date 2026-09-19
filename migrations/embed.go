// Package migrations 内置数据库建表脚本，随服务启动自动应用。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
