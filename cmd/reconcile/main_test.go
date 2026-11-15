// 本文件是 cmd/reconcile 的单元测试：对账口径与**真实退出码语义**（需求文档 §11「脚本语义」）。
//
// 覆盖三件事：
//  1. 账实一致 → 退出码 0；
//  2. 扣额与成功数不一致 → 退出码 1，且输出差异明细；
//  3. 参数/环境错误（租户不存在、日期非法）→ 退出码 2。
package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestDB 建一个带预置数据的对账用数据库。
func newTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE tenants (id TEXT PRIMARY KEY, code TEXT, name TEXT, status TEXT, daily_quota INTEGER, created_at TEXT)`,
		`CREATE TABLE model_requests (id TEXT PRIMARY KEY, tenant_id TEXT, credential_id TEXT, request_no TEXT,
			params_hash TEXT, day TEXT, status TEXT, model TEXT, prompt TEXT, upstream_result TEXT, error_reason TEXT,
			received_at TEXT, completed_at TEXT, replay_count INTEGER DEFAULT 0)`,
		`CREATE TABLE tenant_day_usage (tenant_id TEXT, day TEXT, success_count INTEGER DEFAULT 0,
			inflight_count INTEGER DEFAULT 0, PRIMARY KEY (tenant_id, day))`,
		`CREATE TABLE diagnostics (id TEXT PRIMARY KEY, request_id TEXT, tenant_id TEXT, kind TEXT, detail TEXT, recorded_at TEXT)`,
		`INSERT INTO tenants VALUES ('tenant-a','GEEK-AI','极客智联','ACTIVE',10,'2026-05-19T00:00:00Z')`,
		`INSERT INTO model_requests VALUES
		 ('r1','tenant-a','k1','q1','h1','2026-05-19','success','m','p',NULL,NULL,'2026-05-19T06:00:00Z','2026-05-19T06:00:01Z',0),
		 ('r2','tenant-a','k1','q2','h2','2026-05-19','success','m','p',NULL,NULL,'2026-05-19T06:01:00Z','2026-05-19T06:01:01Z',0),
		 ('r3','tenant-a','k1','q3','h3','2026-05-19','timeout','m','p',NULL,'超时','2026-05-19T06:02:00Z','2026-05-19T06:02:03Z',0),
		 ('r4','tenant-a','k1','q4','h4','2026-05-19','rejected','m','p',NULL,'429','2026-05-19T06:03:00Z','2026-05-19T06:03:00Z',0)`,
		`INSERT INTO tenant_day_usage VALUES ('tenant-a','2026-05-19',2,0)`,
		`INSERT INTO diagnostics VALUES ('d1','r3','tenant-a','late_result','迟到结果','2026-05-19T06:02:03Z')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("准备测试数据失败（%s）: %v", s, err)
		}
	}
	return path
}

func TestReconcileConsistentExitsZero(t *testing.T) {
	dbPath := newTestDB(t)
	code := run([]string{"--tenant", "tenant-a", "--date", "2026-05-19", "--db", dbPath, "--verify"})
	if code != 0 {
		t.Fatalf("账实一致时退出码 = %d, 期望 0", code)
	}
}

func TestReconcileMismatchExitsOne(t *testing.T) {
	dbPath := newTestDB(t)
	// 人为制造差异：用量表记 5 次成功，流水里只有 2 次成功。
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if _, err := db.Exec(`UPDATE tenant_day_usage SET success_count=5 WHERE tenant_id='tenant-a'`); err != nil {
		t.Fatalf("注入差异失败: %v", err)
	}
	db.Close()

	code := run([]string{"--tenant", "tenant-a", "--date", "2026-05-19", "--db", dbPath, "--verify"})
	if code != 1 {
		t.Fatalf("存在差异时退出码 = %d, 期望 1", code)
	}
}

func TestReconcileBadArgumentsExitTwo(t *testing.T) {
	dbPath := newTestDB(t)
	cases := [][]string{
		{"--date", "2026-05-19", "--db", dbPath},                          // 缺 tenant
		{"--tenant", "tenant-a", "--db", dbPath},                          // 缺 date
		{"--tenant", "tenant-a", "--date", "not-a-date", "--db", dbPath},  // 日期非法
		{"--tenant", "not-exist", "--date", "2026-05-19", "--db", dbPath}, // 租户不存在
	}
	for _, args := range cases {
		if code := run(args); code != 2 {
			t.Fatalf("参数/环境错误时退出码 = %d（args=%v）, 期望 2", code, args)
		}
	}
}

func TestReconcileOutputMentionsStatesAndAudit(t *testing.T) {
	dbPath := newTestDB(t)
	// 捕获标准输出，检查关键行都在。
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	os.Stdout = w
	code := run([]string{"--tenant", "tenant-a", "--date", "2026-05-19", "--db", dbPath, "--verify"})
	w.Close()
	os.Stdout = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if code != 0 {
		t.Fatalf("退出码 = %d, 期望 0", code)
	}
	for _, want := range []string{
		"Accounting Natural Date: 2026-05-19",
		"SUCCESS (200)",
		"TIMEOUT (504)",
		"REJECTED (429/403/409)",
		"Billed Usage (2) == Success Count (2) ... PASS",
		"Late-Arrival Pool: 1 record found",
		"Exit code: 0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("对账输出缺少 %q：\n%s", want, out)
		}
	}
}
