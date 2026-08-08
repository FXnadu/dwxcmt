package model

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	_ "modernc.org/sqlite"

	"dwxcmt/migration"
)

// Open 打开 SQLite 数据库并应用优化 PRAGMA 与结构迁移
func Open(path string) (*sql.DB, error) {
	// 连接级 PRAGMA 通过 DSN _pragma 参数下发，保证连接池中每个新建连接都自动应用
	// （旧实现用 db.Exec 逐条执行，只作用于执行语句的那一个连接，
	//   其余池化连接未设置 busy_timeout，并发写时立即返回 SQLITE_BUSY）。
	// 各参数含义：
	//   busy_timeout=5000   写锁冲突时等待最多 5s（并发写不再瞬间 BUSY）
	//   cache_size=-2048    page 缓存 2MB/连接（1G 环境：4 连接上限共 8MB）
	//   synchronous=NORMAL  兼顾持久性与写入吞吐
	//   temp_store=MEMORY   临时表放内存
	//   mmap_size           内存映射上限 64MB（调低避免与页缓存双重占用）
	//   foreign_keys=ON     启用外键约束
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=cache_size(-2048)" +
		"&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)" +
		"&_pragma=mmap_size(67108864)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// WAL 模式支持读写并发；SQLite 单写者，连接数不宜过大——
	// 连接过多只会放大每连接页缓存的内存占用，对 1G 环境收紧到 4。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	// journal_mode 为数据库级属性，显式执行一次即可
	if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("启用 WAL 失败: %w", err)
	}

	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate 基于 migration_versions 表逐级应用未执行的迁移脚本
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS migration_versions (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	);`); err != nil {
		return fmt.Errorf("创建迁移版本表失败: %w", err)
	}

	scripts, err := migration.SQLScripts()
	if err != nil {
		return err
	}

	var maxVer int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM migration_versions`).Scan(&maxVer); err != nil {
		return fmt.Errorf("读取迁移版本失败: %w", err)
	}

	now := time.Now().Unix()
	for _, ver := range migration.Versions(scripts) {
		if ver <= maxVer {
			continue
		}
		log.Printf("[migrate] 应用迁移 v%d", ver)
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		rollback := func(err error) error {
			tx.Rollback()
			return fmt.Errorf("迁移 v%d 失败: %w", ver, err)
		}
		for _, stmt := range migration.SplitStatements(scripts[ver]) {
			if _, err := tx.Exec(stmt); err != nil {
				return rollback(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO migration_versions (version, applied_at) VALUES (?, ?)`, ver, now); err != nil {
			return rollback(err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("迁移 v%d 提交失败: %w", ver, err)
		}
	}
	return nil
}

// ErrNotFound 统一的「记录不存在」哨兵错误
var ErrNotFound = errors.New("record not found")
