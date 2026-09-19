// Package store 负责打开本地 SQLite 数据库并应用内置迁移。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vancemichael/092002-industrial-visit-intent/migrations"
)

// Open 打开（不存在则创建）DATABASE_PATH 指向的 SQLite 文件，
// 打开外键约束并按文件名顺序应用尚未登记的迁移脚本。
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		path = ":memory:"
	}
	var dsn string
	if path == ":memory:" {
		dsn = ":memory:?_fk=1&_busy_timeout=5000"
	} else {
		dsn = "file:" + path + "?_fk=1&_busy_timeout=5000"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// SQLite 单写入者；串行化访问以避免 "database is locked"。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("读取迁移脚本: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 4 && entry.Name()[len(entry.Name())-4:] == ".sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	// schema_migrations 由首个脚本创建，但登记检查要先于脚本执行，
	// 因此这里先确保登记表存在。
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("初始化迁移登记表: %w", err)
	}

	for _, name := range names {
		var applied int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("检查迁移 %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}
		script, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("应用迁移 %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("登记迁移 %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
