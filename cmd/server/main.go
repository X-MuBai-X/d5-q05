// Command server 是题目 05「企业模型服务的配额开通、调用、停用与用量核查」的服务端。
//
// 运行：
//
//	go run ./cmd/server -addr 127.0.0.1:8080 -db ./data/app.db -upstream http://127.0.0.1:9091
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/httpapi"
	"modelquota/internal/service"
	"modelquota/internal/store"
	"modelquota/internal/upstream"
)

// main 是业务服务的进程入口。
//
// 参数解析与装配放在 runMain / buildServer 里，便于单元测试直接验证。
func main() {
	if err := runMain(os.Args[1:]); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

// runMain 解析命令行参数并启动服务。
//
// 单独拆出来是为了让参数解析与启动流程可被单元测试直接验证。
func runMain(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "监听地址")
	dbPath := fs.String("db", "./data/app.db", "SQLite 数据库路径")
	upstreamURL := fs.String("upstream", "http://127.0.0.1:9091", "上游模型服务地址")
	enableTestHooks := fs.Bool("enable-test-hooks", false, "是否开放平台侧测试钩子（含迟到结果注入）")
	seed := fs.Bool("seed", true, "是否写入预置数据（幂等：只补缺失，不改写已有业务数据）")
	resetDemo := fs.Bool("reset-demo", false, "是否把预置租户的额度与状态重置回题面基线（演示前显式使用）")
	webDir := fs.String("web-dir", httpapi.StaticDir, "前端构建产物目录；存在时由本进程一并托管")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return run(*addr, *dbPath, *upstreamURL, *enableTestHooks, *seed, *resetDemo, *webDir)
}

// run 启动 HTTP 服务并等待退出信号。
//
// 收到 Ctrl+C / SIGTERM 后走优雅关闭流程（最多等 10 秒），
// 保证在途请求处理完再退出，避免数据库写入被打断。
func run(addr, dbPath, upstreamURL string, enableTestHooks, seed, resetDemo bool, webDir string) error {
	db, _, srv, err := buildServer(addr, dbPath, upstreamURL, enableTestHooks, seed, resetDemo, webDir)
	if err != nil {
		return err
	}
	defer db.Close()

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("收到退出信号，开始优雅关闭")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("优雅关闭失败: %v", err)
		}
		close(idle)
	}()

	log.Printf("服务已启动: http://%s （数据库 %s，上游 %s，测试钩子 %v）",
		addr, dbPath, upstreamURL, enableTestHooks)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	<-idle
	return nil
}

// buildServer 完成服务装配：建库、写入预置数据、恢复教学时钟、装配 HTTP 服务。
//
// 单独拆出来是为了让「装配」这一步可被单元测试直接验证。
func buildServer(addr, dbPath, upstreamURL string, enableTestHooks, seed, resetDemo bool, webDir string) (*store.DB, *service.Service, *http.Server, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, nil, err
		}
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}

	if seed {
		if err := seedData(ctx, db); err != nil {
			_ = db.Close()
			return nil, nil, nil, err
		}
	}

	// 显式的演示重置：只在调用方要求时把预置租户恢复成题面基线。
	// 默认不执行 —— 重启不得改写已有业务数据（共同要求第 1 节第 3 条）。
	if resetDemo {
		if err := resetDemoData(ctx, db); err != nil {
			_ = db.Close()
			return nil, nil, nil, err
		}
	}

	// 上游不可用时服务仍可启动，调用会返回上游失败——这正是需要观察的异常路径。
	client := upstream.NewHTTPCallier(upstreamURL, &http.Client{}, "mock-upstream")
	svc := service.New(db, service.SystemClock{}, client)
	svc.EnableTestHooks = enableTestHooks

	// 恢复教学时钟偏移（仅影响当前时间，不改变业务规则）。
	if offsetRaw, err := db.GetSetting(ctx, "clock_offset_seconds"); err == nil {
		if secs, convErr := parseInt(offsetRaw); convErr == nil {
			svc.SetClockOffset(time.Duration(secs) * time.Second)
		}
	}

	tmpl, err := httpapi.LoadTemplates()
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}

	handler := httpapi.New(svc, tmpl)
	// 前端构建产物存在时由本进程一并托管：一个端口同时提供 API 与界面。
	// MountStatic 返回**新的根处理器**（静态资源包在 mux 外面），
	// 不能对 "/" 重复注册 —— ServeMux 会因此 panic。
	root := handler.MountStatic(webDir)
	srv := &http.Server{
		Addr:              addr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return db, svc, srv, nil
}

// seedData 写入预置数据：两个租户、各一名客户管理员、一个运营账号。
//
// 题面预置条件：「预置两个企业管理员和一个运营账号；每个企业初始日额度 10 次」。
// 依确认结论 Q1/A1，两个企业管理员分属两个不同租户。
//
// 口令性质说明：下面的 `admin-a` / `admin-b` / `ops` 口令是**题面要求的公开演示值**，
// 仅用于本地演示与自动化冒烟脚本，不对应任何真实系统或真实用户，仓库内不含任何
// 真实密钥、访问令牌或用户敏感信息。登录页只展示账号名、不回显口令
// （见 internal/httpapi/pages_test.go 的对应断言）。
func seedData(ctx context.Context, db *store.DB) error {
	now := time.Now().UTC()

	tenants := []struct {
		ID    string
		Code  string
		Name  string
		Quota int
		Admin struct {
			Username string
			Password string
		}
	}{
		{ID: "tenant-a", Code: "GEEK-AI", Name: "极客智联科技有限公司 [GEEK-AI]", Quota: 10},
		{ID: "tenant-b", Code: "SHIELD-SEC", Name: "智汇数安企业计算中心 [SHIELD-SEC]", Quota: 10},
	}
	tenants[0].Admin.Username = "admin-a"
	tenants[0].Admin.Password = "admin-a-secret"
	tenants[1].Admin.Username = "admin-b"
	tenants[1].Admin.Password = "admin-b-secret"

	for _, t := range tenants {
		if _, err := db.GetTenant(ctx, t.ID); err == nil {
			// 租户已存在：**不改动任何已有业务状态**。
			// 共同要求第 1 节第 3 条要求「服务重启后业务结果仍可查询」，
			// 运营调整过的日额度属于业务数据，重启不能把它改回预置值；
			// 演示前要回到题面基线，请显式调用 resetDemoData（-reset-demo 开关）。
			// 这里只补齐缺失的客户管理员账号，同样不动已有账号。
			if _, adminErr := db.GetAdminByUsername(ctx, t.Admin.Username); adminErr != nil {
				if err := db.CreateAdmin(ctx, &domain.Admin{
					ID:           "admin_" + t.ID,
					TenantID:     t.ID,
					Username:     t.Admin.Username,
					PasswordHash: domain.HashSecret(t.Admin.Password),
					Role:         domain.AdminTenant,
					CreatedAt:    now,
				}); err != nil {
					return err
				}
			}
			continue
		}
		if err := db.CreateTenant(ctx, &domain.Tenant{
			ID:                t.ID,
			Code:              t.Code,
			Name:              t.Name,
			Status:            domain.TenantActive,
			DailyQuota:        t.Quota,
			InitialDailyQuota: t.Quota,
			CreatedAt:         now,
		}); err != nil {
			return err
		}
		if err := db.InsertQuotaChange(ctx, &domain.QuotaChange{
			ID:          "qc_seed_" + t.ID,
			TenantID:    t.ID,
			OldQuota:    0,
			NewQuota:    t.Quota,
			Operator:    "ops",
			Reason:      "预置租户并设置初始日额度",
			EffectiveAt: now,
			Day:         domain.DayOf(now),
		}); err != nil {
			return err
		}
		if err := db.CreateAdmin(ctx, &domain.Admin{
			ID:           "admin_" + t.ID,
			TenantID:     t.ID,
			Username:     t.Admin.Username,
			PasswordHash: domain.HashSecret(t.Admin.Password),
			Role:         domain.AdminTenant,
			CreatedAt:    now,
		}); err != nil {
			return err
		}
	}

	// 运营账号（全局角色，跨租户核查）。
	if _, err := db.GetAdminByUsername(ctx, "ops"); err != nil {
		if err := db.CreateAdmin(ctx, &domain.Admin{
			ID:           "admin_ops",
			TenantID:     "",
			Username:     "ops",
			PasswordHash: domain.HashSecret("ops-secret"),
			Role:         domain.AdminOps,
			CreatedAt:    now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// resetDemoData 把预置租户的日额度恢复到题面基线（每个企业 10 次），并把租户状态复位为 active。
//
// 这是**演示前显式重置**用的入口，默认不执行：
// 共同要求第 1 节第 3 条要求「服务重启后业务结果仍可查询」，
// 因此 seedData 只补齐缺失的预置数据、绝不改写已有业务状态；
// 教学演示需要回到题面起点时，由部署脚本显式带上 -reset-demo 调用本函数。
func resetDemoData(ctx context.Context, db *store.DB) error {
	now := time.Now().UTC()
	for _, t := range []struct {
		ID    string
		Quota int
	}{
		{ID: "tenant-a", Quota: 10},
		{ID: "tenant-b", Quota: 10},
	} {
		tenant, err := db.GetTenant(ctx, t.ID)
		if err != nil {
			// 租户不存在时无需重置（seedData 会负责创建）。
			continue
		}
		if tenant.DailyQuota != t.Quota {
			if err := db.UpdateTenantQuota(ctx, t.ID, t.Quota); err != nil {
				return err
			}
		}
		if tenant.Status != domain.TenantActive {
			if err := db.UpdateTenantStatus(ctx, t.ID, domain.TenantActive); err != nil {
				return err
			}
		}
		_ = now
	}
	return nil
}

// parseInt 解析整数参数，解析失败时返回错误（不静默取 0）。
//
// 用于读取设置表里的数值型配置（如教学时钟偏移秒数）。
func parseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
