// Package sqlitedb 提供对系统 libsqlite3 的最小封装。
// 构建环境无法获取外部 Go 模块，因此直接通过 cgo 绑定系统库，
// 仅暴露本服务需要的打开、执行与查询能力。
package sqlitedb

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>

static int bind_text_transient(sqlite3_stmt *stmt, int idx, const char *val, int n) {
    return sqlite3_bind_text(stmt, idx, val, n, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// DB 是对单个 SQLite 连接的封装。SQLite 默认以串行模式编译，
// 多 goroutine 共享连接是安全的；跨语句事务由调用方自行加锁。
type DB struct {
	handle *C.sqlite3
}

// Row 表示一行查询结果，键为列名，值为 string / int64 / float64 / nil。
type Row map[string]any

// Open 打开（必要时创建）指定路径的数据文件。
func Open(path string) (*DB, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var handle *C.sqlite3
	if rc := C.sqlite3_open(cPath, &handle); rc != C.SQLITE_OK {
		msg := "未知错误"
		if handle != nil {
			msg = C.GoString(C.sqlite3_errmsg(handle))
			C.sqlite3_close(handle)
		}
		return nil, fmt.Errorf("打开数据库失败: %s", msg)
	}
	db := &DB{handle: handle}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if err := db.ExecScript(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

// Close 关闭连接。
func (d *DB) Close() error {
	if d.handle == nil {
		return nil
	}
	rc := C.sqlite3_close(d.handle)
	d.handle = nil
	if rc != C.SQLITE_OK {
		return fmt.Errorf("关闭数据库失败")
	}
	return nil
}

func (d *DB) errmsg() string {
	return C.GoString(C.sqlite3_errmsg(d.handle))
}

// ExecScript 执行可能包含多条语句的脚本，供迁移使用。
func (d *DB) ExecScript(script string) error {
	cScript := C.CString(script)
	defer C.free(unsafe.Pointer(cScript))
	var errMsg *C.char
	if rc := C.sqlite3_exec(d.handle, cScript, nil, nil, &errMsg); rc != C.SQLITE_OK {
		msg := d.errmsg()
		if errMsg != nil {
			msg = C.GoString(errMsg)
			C.sqlite3_free(unsafe.Pointer(errMsg))
		}
		return fmt.Errorf("执行脚本失败: %s", msg)
	}
	return nil
}

func (d *DB) prepare(query string) (*C.sqlite3_stmt, error) {
	cQuery := C.CString(query)
	defer C.free(unsafe.Pointer(cQuery))
	var stmt *C.sqlite3_stmt
	if rc := C.sqlite3_prepare_v2(d.handle, cQuery, -1, &stmt, nil); rc != C.SQLITE_OK {
		return nil, fmt.Errorf("预处理语句失败: %s", d.errmsg())
	}
	if stmt == nil {
		return nil, fmt.Errorf("预处理语句失败: 空语句")
	}
	return stmt, nil
}

func bindArgs(stmt *C.sqlite3_stmt, args []any) error {
	for i, arg := range args {
		idx := C.int(i + 1)
		var rc C.int
		switch v := arg.(type) {
		case nil:
			rc = C.sqlite3_bind_null(stmt, idx)
		case string:
			cVal := C.CString(v)
			rc = C.bind_text_transient(stmt, idx, cVal, C.int(len(v)))
			C.free(unsafe.Pointer(cVal))
		case int:
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case int64:
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(v))
		case bool:
			n := 0
			if v {
				n = 1
			}
			rc = C.sqlite3_bind_int64(stmt, idx, C.sqlite3_int64(n))
		case float64:
			rc = C.sqlite3_bind_double(stmt, idx, C.double(v))
		default:
			return fmt.Errorf("不支持的参数类型 %T", arg)
		}
		if rc != C.SQLITE_OK {
			return fmt.Errorf("绑定第 %d 个参数失败", i+1)
		}
	}
	return nil
}

// Exec 执行单条语句，不返回结果集。
func (d *DB) Exec(query string, args ...any) error {
	stmt, err := d.prepare(query)
	if err != nil {
		return err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindArgs(stmt, args); err != nil {
		return err
	}
	for {
		rc := C.sqlite3_step(stmt)
		switch rc {
		case C.SQLITE_DONE:
			return nil
		case C.SQLITE_ROW:
			continue
		default:
			return fmt.Errorf("执行失败: %s", d.errmsg())
		}
	}
}

// Query 执行查询并返回全部行。
func (d *DB) Query(query string, args ...any) ([]Row, error) {
	stmt, err := d.prepare(query)
	if err != nil {
		return nil, err
	}
	defer C.sqlite3_finalize(stmt)
	if err := bindArgs(stmt, args); err != nil {
		return nil, err
	}
	colCount := int(C.sqlite3_column_count(stmt))
	names := make([]string, colCount)
	var rows []Row
	for {
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			return nil, fmt.Errorf("查询失败: %s", d.errmsg())
		}
		row := Row{}
		for i := 0; i < colCount; i++ {
			ci := C.int(i)
			if names[i] == "" {
				names[i] = C.GoString(C.sqlite3_column_name(stmt, ci))
			}
			switch C.sqlite3_column_type(stmt, ci) {
			case C.SQLITE_NULL:
				row[names[i]] = nil
			case C.SQLITE_INTEGER:
				row[names[i]] = int64(C.sqlite3_column_int64(stmt, ci))
			case C.SQLITE_FLOAT:
				row[names[i]] = float64(C.sqlite3_column_double(stmt, ci))
			case C.SQLITE_BLOB:
				n := C.sqlite3_column_bytes(stmt, ci)
				if n == 0 {
					row[names[i]] = ""
					break
				}
				row[names[i]] = string(C.GoBytes(C.sqlite3_column_blob(stmt, ci), n))
			default:
				ptr := C.sqlite3_column_text(stmt, ci)
				if ptr == nil {
					row[names[i]] = ""
				} else {
					row[names[i]] = C.GoString((*C.char)(unsafe.Pointer(ptr)))
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// QueryOne 返回第一行；found 为 false 表示没有匹配行。
func (d *DB) QueryOne(query string, args ...any) (row Row, found bool, err error) {
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return rows[0], true, nil
}
