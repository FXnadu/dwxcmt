package migration

import (
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var files embed.FS

// SQLScripts 返回已应用的迁移脚本：version(文件名数字前缀) -> SQL 内容，按版本升序
func SQLScripts() (map[int]string, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	scripts := make(map[int]string)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		verStr := strings.SplitN(name, "_", 2)[0]
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			return nil, fmt.Errorf("migration file %s: 版本号必须为数字前缀", name)
		}
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, err
		}
		scripts[ver] = string(data)
	}
	return scripts, nil
}

// SplitStatements 将 SQL 脚本按分号拆分为独立语句（跳过空语句与纯注释）
func SplitStatements(script string) []string {
	var stmts []string
	for _, part := range strings.Split(script, ";") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		stmts = append(stmts, s)
	}
	return stmts
}

// Versions 返回升序版本号列表
func Versions(scripts map[int]string) []int {
	vs := make([]int, 0, len(scripts))
	for v := range scripts {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}
