// migrate 对 DATABASE_PATH 指定的数据文件应用全部迁移脚本。
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/leads"
	"github.com/vancemichael/092002-industrial-visit-intent/internal/sqlitedb"
)

func main() {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}
	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}
	db, err := sqlitedb.Open(dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if err := leads.Migrate(db); err != nil {
		log.Fatalf("应用迁移失败: %v", err)
	}
	log.Printf("迁移完成: %s", dbPath)
}
