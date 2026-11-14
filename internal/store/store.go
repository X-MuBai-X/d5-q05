// Package store 提供题目 05 的 SQLite 持久化。
//
// 关键点：受理请求时的额度判定必须在数据库层原子完成（题面 R2「防止并发超过上限」），
// 因此使用带条件的 UPDATE 语句一次完成「计数 + 判定」，而不是先查后写。
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"modelquota/internal/domain"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DB 包装 *sql.DB 并提供题目所需的业务查询。
type DB struct {
	sql *sql.DB
}

// Open 打开（并在需要时创建）SQLite 数据库并执行建表脚本。
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 为单写模型，限制连接数可避免写冲突导致的 SQLITE_BUSY。
	sdb.SetMaxOpenConns(1)
	sdb.SetMaxIdleConns(1)
	if err := sdb.Ping(); err != nil {
		return nil, fmt.Errorf("数据库连通性检查失败: %w", err)
	}
	return &DB{sql: sdb}, nil
}

// New 使用给定 *sql.DB 构造 DB，主要用于测试注入。
func New(sdb *sql.DB) *DB { return &DB{sql: sdb} }

// SQL 暴露底层句柄，仅用于测试与迁移。
func (d *DB) SQL() *sql.DB { return d.sql }

// Migrate 建表（幂等）。
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("执行建表脚本失败: %w", err)
	}
	return nil
}

// Close 关闭底层连接。
func (d *DB) Close() error { return d.sql.Close() }

// ts 把时间统一格式化为 UTC 的 RFC3339Nano 字符串后入库。
//
// 统一 UTC 与格式，避免各处时间表示不一致导致日期归属算错。
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseTS 解析库里的时间字符串，解析失败时返回零值时间。
//
// 零值时间表示「该时间未知」，调用方据此判断字段是否为空。
func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// nullIfEmpty 把空字符串转成 SQL NULL，非空则原样返回。
//
// 用于可空列的写入，避免把空串和「没有值」混为一谈。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------- 租户 ----------

// CreateTenant 创建租户。
func (d *DB) CreateTenant(ctx context.Context, t *domain.Tenant) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO tenants(id, name, status, daily_quota, created_at) VALUES(?,?,?,?,?)`,
		t.ID, t.Name, string(t.Status), t.DailyQuota, ts(t.CreatedAt))
	if err != nil {
		return fmt.Errorf("创建租户失败: %w", err)
	}
	return nil
}

// GetTenant 按 ID 读取租户。
func (d *DB) GetTenant(ctx context.Context, id string) (*domain.Tenant, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT id, name, status, daily_quota, created_at FROM tenants WHERE id=?`, id)
	var t domain.Tenant
	var status, created string
	if err := row.Scan(&t.ID, &t.Name, &status, &t.DailyQuota, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("读取租户失败: %w", err)
	}
	t.Status = domain.TenantStatus(status)
	t.CreatedAt = parseTS(created)
	return &t, nil
}

// ListTenants 列出全部租户。
func (d *DB) ListTenants(ctx context.Context) ([]domain.Tenant, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, name, status, daily_quota, created_at FROM tenants ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("列出租户失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Tenant, 0)
	for rows.Next() {
		var t domain.Tenant
		var status, created string
		if err := rows.Scan(&t.ID, &t.Name, &status, &t.DailyQuota, &created); err != nil {
			return nil, fmt.Errorf("扫描租户失败: %w", err)
		}
		t.Status = domain.TenantStatus(status)
		t.CreatedAt = parseTS(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTenantStatus 更新租户状态（R6）。
func (d *DB) UpdateTenantStatus(ctx context.Context, tenantID string, status domain.TenantStatus) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE tenants SET status=? WHERE id=?`, string(status), tenantID)
	if err != nil {
		return fmt.Errorf("更新租户状态失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateTenantQuota 更新租户日额度。
func (d *DB) UpdateTenantQuota(ctx context.Context, tenantID string, quota int) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE tenants SET daily_quota=? WHERE id=?`, quota, tenantID)
	if err != nil {
		return fmt.Errorf("更新租户额度失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------- 管理员 ----------

// CreateAdmin 创建管理员账号。
// 平台运营账号没有租户归属，tenant_id 写 NULL（对应表结构中的可空外键）。
func (d *DB) CreateAdmin(ctx context.Context, a *domain.Admin) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO admins(id, tenant_id, username, password_hash, role, created_at) VALUES(?,?,?,?,?,?)`,
		a.ID, nullIfEmpty(a.TenantID), a.Username, a.PasswordHash, string(a.Role), ts(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("创建管理员失败: %w", err)
	}
	return nil
}

// GetAdminByUsername 按用户名读取管理员。
func (d *DB) GetAdminByUsername(ctx context.Context, username string) (*domain.Admin, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT id, tenant_id, username, password_hash, role, created_at FROM admins WHERE username=?`, username)
	var a domain.Admin
	var role, created string
	var tenantID sql.NullString
	if err := row.Scan(&a.ID, &tenantID, &a.Username, &a.PasswordHash, &role, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("读取管理员失败: %w", err)
	}
	a.TenantID = tenantID.String
	a.Role = domain.AdminRole(role)
	a.CreatedAt = parseTS(created)
	return &a, nil
}

// GetAdminByID 按 ID 读取管理员。
func (d *DB) GetAdminByID(ctx context.Context, id string) (*domain.Admin, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT id, tenant_id, username, password_hash, role, created_at FROM admins WHERE id=?`, id)
	var a domain.Admin
	var role, created string
	var tenantID sql.NullString
	if err := row.Scan(&a.ID, &tenantID, &a.Username, &a.PasswordHash, &role, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("按 ID 读取管理员失败: %w", err)
	}
	a.TenantID = tenantID.String
	a.Role = domain.AdminRole(role)
	a.CreatedAt = parseTS(created)
	return &a, nil
}

// ListAdminsByTenant 列出某租户的管理员。
func (d *DB) ListAdminsByTenant(ctx context.Context, tenantID string) ([]domain.Admin, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, tenant_id, username, password_hash, role, created_at FROM admins WHERE tenant_id=? ORDER BY username`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("列出管理员失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Admin, 0)
	for rows.Next() {
		var a domain.Admin
		var role, created string
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Username, &a.PasswordHash, &role, &created); err != nil {
			return nil, fmt.Errorf("扫描管理员失败: %w", err)
		}
		a.Role = domain.AdminRole(role)
		a.CreatedAt = parseTS(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- 凭证 ----------

// CreateCredential 创建凭证。
func (d *DB) CreateCredential(ctx context.Context, c *domain.Credential) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO credentials(id, tenant_id, name, key_hash, status, created_at, created_by) VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.Name, c.KeyHash, string(c.Status), ts(c.CreatedAt), c.CreatedBy)
	if err != nil {
		return fmt.Errorf("创建凭证失败: %w", err)
	}
	return nil
}

const credentialCols = `id, tenant_id, name, key_hash, status, created_at, created_by, disabled_at, disabled_by, disable_reason`

// scanCredential 把一行凭证记录扫描成 domain.Credential。
//
// 可空列（停用时间/停用人/停用原因）用 sql.NullString 接，
// 空值转成零值字段；状态与时间在这里统一转换。
func scanCredential(sc interface{ Scan(...any) error }) (*domain.Credential, error) {
	var c domain.Credential
	var status, created string
	var disabledAt sql.NullString
	var disabledBy, disableReason sql.NullString
	if err := sc.Scan(&c.ID, &c.TenantID, &c.Name, &c.KeyHash, &status, &created,
		&c.CreatedBy, &disabledAt, &disabledBy, &disableReason); err != nil {
		return nil, err
	}
	c.Status = domain.CredentialStatus(status)
	c.CreatedAt = parseTS(created)
	if disabledAt.Valid {
		v := parseTS(disabledAt.String)
		c.DisabledAt = &v
	}
	c.DisabledBy = disabledBy.String
	c.DisableWhy = disableReason.String
	return &c, nil
}

// GetCredential 按 ID 读取凭证。
func (d *DB) GetCredential(ctx context.Context, id string) (*domain.Credential, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+credentialCols+` FROM credentials WHERE id=?`, id)
	c, err := scanCredential(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("读取凭证失败: %w", err)
	}
	return c, nil
}

// GetCredentialByKeyHash 按凭证摘要读取凭证（应用登录用）。
func (d *DB) GetCredentialByKeyHash(ctx context.Context, keyHash string) (*domain.Credential, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+credentialCols+` FROM credentials WHERE key_hash=?`, keyHash)
	c, err := scanCredential(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("按摘要读取凭证失败: %w", err)
	}
	return c, nil
}

// ListCredentialsByTenant 列出某租户的凭证。
func (d *DB) ListCredentialsByTenant(ctx context.Context, tenantID string) ([]domain.Credential, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+credentialCols+` FROM credentials WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("列出凭证失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Credential, 0)
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描凭证失败: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CountCredentials 统计某租户的凭证数量（上限校验用）。
func (d *DB) CountCredentials(ctx context.Context, tenantID string) (int, error) {
	var n int
	if err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM credentials WHERE tenant_id=?`, tenantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计凭证失败: %w", err)
	}
	return n, nil
}

// DisableCredential 停用凭证（R6）。
func (d *DB) DisableCredential(ctx context.Context, id, actor, reason string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE credentials SET status=?, disabled_at=?, disabled_by=?, disable_reason=? WHERE id=?`,
		string(domain.CredentialDisabled), ts(at), actor, reason, id)
	if err != nil {
		return fmt.Errorf("停用凭证失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------- 调用请求 ----------

const requestCols = `id, tenant_id, credential_id, request_no, params_hash, day, status, model, prompt, upstream_result, error_reason, received_at, completed_at, replay_count`

// InsertRequest 写入一条已受理的调用请求。
func (d *DB) InsertRequest(ctx context.Context, r *domain.ModelRequest) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO model_requests(id, tenant_id, credential_id, request_no, params_hash, day, status, model, prompt, received_at, replay_count)
		 VALUES(?,?,?,?,?,?,?,?,?,?,0)`,
		r.ID, r.TenantID, r.CredentialID, r.RequestNo, r.ParamsHash, r.Day,
		string(r.Status), r.Model, r.Prompt, ts(r.ReceivedAt))
	if err != nil {
		return fmt.Errorf("写入调用请求失败: %w", err)
	}
	return nil
}

// scanRequest 把一行调用请求记录扫描成 domain.ModelRequest。
//
// 受理时间与完成时间是可空的（处理中的请求没有完成时间），
// 上游结果与失败原因同样可空，都用 sql.NullString 接。
func scanRequest(sc interface{ Scan(...any) error }) (*domain.ModelRequest, error) {
	var r domain.ModelRequest
	var status, received string
	var upstream, errReason sql.NullString
	var completed sql.NullString
	if err := sc.Scan(&r.ID, &r.TenantID, &r.CredentialID, &r.RequestNo, &r.ParamsHash, &r.Day,
		&status, &r.Model, &r.Prompt, &upstream, &errReason, &received, &completed, &r.ReplayCount); err != nil {
		return nil, err
	}
	r.Status = domain.RequestStatus(status)
	r.ReceivedAt = parseTS(received)
	r.UpstreamResult = upstream.String
	r.ErrorReason = errReason.String
	if completed.Valid {
		v := parseTS(completed.String)
		r.CompletedAt = &v
	}
	return &r, nil
}

// GetRequestByNo 按租户与请求编号读取请求（R3 幂等判定）。
func (d *DB) GetRequestByNo(ctx context.Context, tenantID, requestNo string) (*domain.ModelRequest, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+requestCols+` FROM model_requests WHERE tenant_id=? AND request_no=?`, tenantID, requestNo)
	r, err := scanRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("按编号读取请求失败: %w", err)
	}
	return r, nil
}

// GetRequest 按 ID 读取请求。
func (d *DB) GetRequest(ctx context.Context, id string) (*domain.ModelRequest, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+requestCols+` FROM model_requests WHERE id=?`, id)
	r, err := scanRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("读取请求失败: %w", err)
	}
	return r, nil
}

// FinishRequest 把请求置为终态（成功/失败/超时），只允许从 processing 迁移。
func (d *DB) FinishRequest(ctx context.Context, id string, status domain.RequestStatus, upstreamResult, errReason string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE model_requests SET status=?, upstream_result=?, error_reason=?, completed_at=? WHERE id=? AND status=?`,
		string(status), upstreamResult, errReason, ts(at), id, string(domain.RequestProcessing))
	if err != nil {
		return fmt.Errorf("更新请求终态失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// IncrementReplay 记录一次幂等命中（不重复计量）。
func (d *DB) IncrementReplay(ctx context.Context, id string) error {
	if _, err := d.sql.ExecContext(ctx,
		`UPDATE model_requests SET replay_count = replay_count + 1 WHERE id=?`, id); err != nil {
		return fmt.Errorf("记录幂等命中失败: %w", err)
	}
	return nil
}

// CountRequestsByStatus 统计某租户某日各终态数量（R8 日终核查）。
func (d *DB) CountRequestsByStatus(ctx context.Context, tenantID, day string) (map[domain.RequestStatus]int, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM model_requests WHERE tenant_id=? AND day=? GROUP BY status`, tenantID, day)
	if err != nil {
		return nil, fmt.Errorf("统计请求终态失败: %w", err)
	}
	defer rows.Close()
	out := map[domain.RequestStatus]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("扫描终态统计失败: %w", err)
		}
		out[domain.RequestStatus(status)] = n
	}
	return out, rows.Err()
}

// ListRequestsByTenant 列出某租户某日的请求（R8）。
func (d *DB) ListRequestsByTenant(ctx context.Context, tenantID, day string) ([]domain.ModelRequest, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+requestCols+` FROM model_requests WHERE tenant_id=? AND day=? ORDER BY received_at, id`, tenantID, day)
	if err != nil {
		return nil, fmt.Errorf("列出租户请求失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ModelRequest, 0)
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描请求失败: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListRequestsByCredential 列出某凭证创建的请求（R7 应用只可查自己的）。
func (d *DB) ListRequestsByCredential(ctx context.Context, credentialID, day string) ([]domain.ModelRequest, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+requestCols+` FROM model_requests WHERE credential_id=? AND day=? ORDER BY received_at, id`, credentialID, day)
	if err != nil {
		return nil, fmt.Errorf("列出凭证请求失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.ModelRequest, 0)
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描请求失败: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ---------- 被拒绝的尝试（独立表，不占用 request_no） ----------

// InsertRejectedAttempt 记录一次被拒绝的调用尝试（R8）。
func (d *DB) InsertRejectedAttempt(ctx context.Context, a *domain.RejectedAttempt) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO rejected_attempts(id, tenant_id, credential_id, request_no, day, status, reason, actor, occurred_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		a.ID, nullIfEmpty(a.TenantID), nullIfEmpty(a.CredentialID), nullIfEmpty(a.RequestNo),
		a.Day, string(a.Status), a.Reason, a.Actor, ts(a.OccurredAt))
	if err != nil {
		return fmt.Errorf("写入被拒绝尝试失败: %w", err)
	}
	return nil
}

// CountRejectedAttempts 统计某租户某日被拒绝的次数（R8）。
func (d *DB) CountRejectedAttempts(ctx context.Context, tenantID, day string) (int, error) {
	var n int
	if err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rejected_attempts WHERE tenant_id=? AND day=?`, tenantID, day).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计被拒绝尝试失败: %w", err)
	}
	return n, nil
}

// ListRejectedAttempts 列出某租户某日被拒绝的尝试（R8）。
func (d *DB) ListRejectedAttempts(ctx context.Context, tenantID, day string) ([]domain.RejectedAttempt, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, tenant_id, credential_id, request_no, day, status, reason, actor, occurred_at
		 FROM rejected_attempts WHERE tenant_id=? AND day=? ORDER BY occurred_at, id`, tenantID, day)
	if err != nil {
		return nil, fmt.Errorf("列出被拒绝尝试失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RejectedAttempt, 0)
	for rows.Next() {
		var a domain.RejectedAttempt
		var tenantID, credID, reqNo sql.NullString
		var status, occurred string
		if err := rows.Scan(&a.ID, &tenantID, &credID, &reqNo, &a.Day, &status, &a.Reason, &a.Actor, &occurred); err != nil {
			return nil, fmt.Errorf("扫描被拒绝尝试失败: %w", err)
		}
		a.TenantID = tenantID.String
		a.CredentialID = credID.String
		a.RequestNo = reqNo.String
		a.Status = domain.RequestStatus(status)
		a.OccurredAt = parseTS(occurred)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- 用量计数 ----------

// EnsureDayUsage 确保某租户某日的用量计数行存在。
func (d *DB) EnsureDayUsage(ctx context.Context, tenantID, day string) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO tenant_day_usage(tenant_id, day, success_count, inflight_count) VALUES(?,?,0,0)
		 ON CONFLICT(tenant_id, day) DO NOTHING`, tenantID, day)
	if err != nil {
		return fmt.Errorf("初始化当日用量计数失败: %w", err)
	}
	return nil
}

// ReserveInflight 原子地占用一个额度名额（题面 R2）。
// 仅当当日「成功数 + 处理中数」小于额度时才计数加一；
// 返回 false 表示额度已满，调用方应拒绝受理该请求。
//
// 额度上限必须由**本条语句内部**读取 tenants.daily_quota 的实时值，
// 不能由调用方把额度快照当参数传进来：
// 若传快照，并发降额会与本条判定交错，出现「占用数超过额度」的不变量破坏
// （2026-11-11 独立验收 Agent 实测复现，见 evidence/independent-acceptance-report.md D1）。
// 这里用子查询直接读实时额度，使「判定 + 计数」与「额度变更」在数据库层串行化。
func (d *DB) ReserveInflight(ctx context.Context, tenantID, day string) (bool, error) {
	if err := d.EnsureDayUsage(ctx, tenantID, day); err != nil {
		return false, err
	}
	res, err := d.sql.ExecContext(ctx,
		`UPDATE tenant_day_usage SET inflight_count = inflight_count + 1
		 WHERE tenant_id=? AND day=?
		   AND (success_count + inflight_count) < COALESCE((SELECT daily_quota FROM tenants WHERE id=?), 0)`,
		tenantID, day, tenantID)
	if err != nil {
		return false, fmt.Errorf("占用额度名额失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取额度占用结果失败: %w", err)
	}
	return n > 0, nil
}

// UpdateTenantQuotaWithFloor 在「当日占用不超过新额度」的前提下更新租户日额度（R5）。
//
// 降额下限的判定与写入必须在同一条语句内完成：
// 若分成「先读占用、再写额度」两步，并发的额度占用会落在两步之间，
// 出现额度低于实际占用的不变量破坏（同 D1）。
// 返回 false 表示当前占用已高于新额度，调用方应拒绝本次降额。
func (d *DB) UpdateTenantQuotaWithFloor(ctx context.Context, tenantID, day string, newQuota int) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE tenants SET daily_quota=?
		 WHERE id=?
		   AND COALESCE((SELECT success_count + inflight_count FROM tenant_day_usage
		                 WHERE tenant_id=? AND day=?), 0) <= ?`,
		newQuota, tenantID, tenantID, day, newQuota)
	if err != nil {
		return false, fmt.Errorf("更新租户额度失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取额度更新结果失败: %w", err)
	}
	return n > 0, nil
}

// CommitSuccess 把处理中转为成功：处理中减一、成功加一，总占用不变（题面 R2/R4）。
func (d *DB) CommitSuccess(ctx context.Context, tenantID, day string) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE tenant_day_usage SET inflight_count = inflight_count - 1, success_count = success_count + 1
		 WHERE tenant_id=? AND day=? AND inflight_count > 0`, tenantID, day)
	if err != nil {
		return fmt.Errorf("计入成功量失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("处理中计数已异常，无法计入成功量")
	}
	return nil
}

// ReleaseInflight 释放额度占用（失败/超时路径，题面 R4）。
func (d *DB) ReleaseInflight(ctx context.Context, tenantID, day string) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE tenant_day_usage SET inflight_count = inflight_count - 1
		 WHERE tenant_id=? AND day=? AND inflight_count > 0`, tenantID, day)
	if err != nil {
		return fmt.Errorf("释放额度占用失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("处理中计数已异常，无法释放额度占用")
	}
	return nil
}

// GetDayUsage 读取某租户某日的用量计数与额度。
func (d *DB) GetDayUsage(ctx context.Context, tenantID, day string, quota int) (domain.Usage, error) {
	var success, inflight int
	err := d.sql.QueryRowContext(ctx,
		`SELECT success_count, inflight_count FROM tenant_day_usage WHERE tenant_id=? AND day=?`,
		tenantID, day).Scan(&success, &inflight)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Usage{TenantID: tenantID, Day: day, QuotaLimit: quota}, nil
		}
		return domain.Usage{}, fmt.Errorf("读取当日用量失败: %w", err)
	}
	return domain.Usage{TenantID: tenantID, Day: day, QuotaLimit: quota, Success: success, InFlight: inflight}, nil
}

// ---------- 配额变更与事件 ----------

// InsertQuotaChange 记录配额变更（R5）。
func (d *DB) InsertQuotaChange(ctx context.Context, c *domain.QuotaChange) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO quota_changes(id, tenant_id, old_quota, new_quota, operator, reason, effective_at, day) VALUES(?,?,?,?,?,?,?,?)`,
		c.ID, c.TenantID, c.OldQuota, c.NewQuota, c.Operator, c.Reason, ts(c.EffectiveAt), c.Day)
	if err != nil {
		return fmt.Errorf("写入配额变更失败: %w", err)
	}
	return nil
}

// ListQuotaChanges 列出某租户某日的配额变更（R8）。
func (d *DB) ListQuotaChanges(ctx context.Context, tenantID, day string) ([]domain.QuotaChange, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, tenant_id, old_quota, new_quota, operator, reason, effective_at, day
		 FROM quota_changes WHERE tenant_id=? AND day=? ORDER BY effective_at, id`, tenantID, day)
	if err != nil {
		return nil, fmt.Errorf("列出配额变更失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.QuotaChange, 0)
	for rows.Next() {
		var c domain.QuotaChange
		var effective string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.OldQuota, &c.NewQuota, &c.Operator, &c.Reason, &effective, &c.Day); err != nil {
			return nil, fmt.Errorf("扫描配额变更失败: %w", err)
		}
		c.EffectiveAt = parseTS(effective)
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertEvent 记录租户/凭证事件（R6/R8）。
func (d *DB) InsertEvent(ctx context.Context, e *domain.TenantEvent) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO tenant_events(id, tenant_id, credential_id, kind, actor, reason, detail, day, occurred_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TenantID, nullIfEmpty(e.CredentialID), e.Kind, e.Actor, e.Reason, nullIfEmpty(e.Detail), e.Day, ts(e.OccurredAt))
	if err != nil {
		return fmt.Errorf("写入业务事件失败: %w", err)
	}
	return nil
}

// ListEvents 列出某租户某日的事件（R8）。
func (d *DB) ListEvents(ctx context.Context, tenantID, day string) ([]domain.TenantEvent, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, tenant_id, credential_id, kind, actor, reason, detail, day, occurred_at
		 FROM tenant_events WHERE tenant_id=? AND day=? ORDER BY occurred_at, id`, tenantID, day)
	if err != nil {
		return nil, fmt.Errorf("列出业务事件失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.TenantEvent, 0)
	for rows.Next() {
		var e domain.TenantEvent
		var credID, detail sql.NullString
		var occurred string
		if err := rows.Scan(&e.ID, &e.TenantID, &credID, &e.Kind, &e.Actor, &e.Reason, &detail, &e.Day, &occurred); err != nil {
			return nil, fmt.Errorf("扫描业务事件失败: %w", err)
		}
		e.CredentialID = credID.String
		e.Detail = detail.String
		e.OccurredAt = parseTS(occurred)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- 诊断 ----------

// InsertDiagnostic 写入诊断记录（R4 迟到结果）。
func (d *DB) InsertDiagnostic(ctx context.Context, diag *domain.Diagnostic) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO diagnostics(id, request_id, tenant_id, kind, detail, recorded_at) VALUES(?,?,?,?,?,?)`,
		diag.ID, diag.RequestID, diag.TenantID, diag.Kind, diag.Detail, ts(diag.RecordedAt))
	if err != nil {
		return fmt.Errorf("写入诊断记录失败: %w", err)
	}
	return nil
}

// ListDiagnosticsByTenant 列出某租户的诊断记录。
func (d *DB) ListDiagnosticsByTenant(ctx context.Context, tenantID string) ([]domain.Diagnostic, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, request_id, tenant_id, kind, detail, recorded_at FROM diagnostics WHERE tenant_id=? ORDER BY recorded_at, id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("列出诊断记录失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Diagnostic, 0)
	for rows.Next() {
		var diag domain.Diagnostic
		var recorded string
		if err := rows.Scan(&diag.ID, &diag.RequestID, &diag.TenantID, &diag.Kind, &diag.Detail, &recorded); err != nil {
			return nil, fmt.Errorf("扫描诊断记录失败: %w", err)
		}
		diag.RecordedAt = parseTS(recorded)
		out = append(out, diag)
	}
	return out, rows.Err()
}

// ListDiagnosticsByRequest 列出某请求的诊断记录。
func (d *DB) ListDiagnosticsByRequest(ctx context.Context, requestID string) ([]domain.Diagnostic, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id, request_id, tenant_id, kind, detail, recorded_at FROM diagnostics WHERE request_id=? ORDER BY recorded_at, id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("列出请求诊断失败: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Diagnostic, 0)
	for rows.Next() {
		var diag domain.Diagnostic
		var recorded string
		if err := rows.Scan(&diag.ID, &diag.RequestID, &diag.TenantID, &diag.Kind, &diag.Detail, &recorded); err != nil {
			return nil, fmt.Errorf("扫描请求诊断失败: %w", err)
		}
		diag.RecordedAt = parseTS(recorded)
		out = append(out, diag)
	}
	return out, rows.Err()
}

// ---------- 会话 ----------

// CreateSession 保存会话令牌。
func (d *DB) CreateSession(ctx context.Context, token, subjectType, subjectID, tenantID, role string, createdAt, expiresAt time.Time) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO sessions(token, subject_type, subject_id, tenant_id, role, created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		token, subjectType, subjectID, nullIfEmpty(tenantID), nullIfEmpty(role), ts(createdAt), ts(expiresAt))
	if err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

// Session 是已签发的会话。
type Session struct {
	Token       string
	SubjectType domain.SubjectType
	SubjectID   string
	TenantID    string
	Role        domain.AdminRole
	ExpiresAt   time.Time
}

// GetSession 按令牌读取会话。
func (d *DB) GetSession(ctx context.Context, token string) (*Session, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT token, subject_type, subject_id, tenant_id, role, expires_at FROM sessions WHERE token=?`, token)
	var s Session
	var subjectType, expires string
	var tenantID, role sql.NullString
	if err := row.Scan(&s.Token, &subjectType, &s.SubjectID, &tenantID, &role, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	s.SubjectType = domain.SubjectType(subjectType)
	s.TenantID = tenantID.String
	s.Role = domain.AdminRole(role.String)
	s.ExpiresAt = parseTS(expires)
	return &s, nil
}

// DeleteSession 删除会话（登出）。
func (d *DB) DeleteSession(ctx context.Context, token string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE token=?`, token); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// ---------- 设置（教学时钟） ----------

// SetSetting 写入设置项。
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("写入设置失败: %w", err)
	}
	return nil
}

// GetSetting 读取设置项。
func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("读取设置失败: %w", err)
	}
	return v, nil
}
