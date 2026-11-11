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
	seed := fs.Bool("seed", true, "是否写入预置数据")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return run(*addr, *dbPath, *upstreamURL, *enableTestHooks, *seed)
}

func run(addr, dbPath, upstreamURL string, enableTestHooks, seed bool) error {
	db, _, srv, err := buildServer(addr, dbPath, upstreamURL, enableTestHooks, seed)
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
func buildServer(addr, dbPath, upstreamURL string, enableTestHooks, seed bool) (*store.DB, *service.Service, *http.Server, error) {
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

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(svc, tmpl).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return db, svc, srv, nil
}

// seedData 写入预置数据：两个租户、各一名客户管理员、一个运营账号。
//
// 题面预置条件：「预置两个企业管理员和一个运营账号；每个企业初始日额度 10 次」。
// 依确认结论 Q1/A1，两个企业管理员分属两个不同租户。
func seedData(ctx context.Context, db *store.DB) error {
	now := time.Now().UTC()

	tenants := []struct {
		ID    string
		Name  string
		Quota int
		Admin struct {
			Username string
			Password string
		}
	}{
		{ID: "tenant-a", Name: "企业甲（演示活动客户）", Quota: 10},
		{ID: "tenant-b", Name: "企业乙（凭证泄露客户）", Quota: 10},
	}
	tenants[0].Admin.Username = "admin-a"
	tenants[0].Admin.Password = "admin-a-secret"
	tenants[1].Admin.Username = "admin-b"
	tenants[1].Admin.Password = "admin-b-secret"

	for _, t := range tenants {
		existing, err := db.GetTenant(ctx, t.ID)
		if err == nil {
			// 租户已存在则不重复写入，但确保配额与状态正确。
			if existing.DailyQuota != t.Quota {
				if err := db.UpdateTenantQuota(ctx, t.ID, t.Quota); err != nil {
					return err
				}
			}
			continue
		}
		if err := db.CreateTenant(ctx, &domain.Tenant{
			ID:         t.ID,
			Name:       t.Name,
			Status:     domain.TenantActive,
			DailyQuota: t.Quota,
			CreatedAt:  now,
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

func parseInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
