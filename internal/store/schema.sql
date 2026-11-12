-- 题目 05 企业模型服务：核心业务表结构。
-- 数据库类型：SQLite（modernc.org/sqlite 纯 Go 驱动，无需 CGO）。
-- 业务规则编号 R1..R8 对应题面「需要完成的业务范围与规则」。
-- 本文件是数据库初始化脚本的唯一事实来源，由 internal/store 通过 go:embed 加载。

CREATE TABLE IF NOT EXISTS tenants (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    status        TEXT NOT NULL,              -- pending / active / disabled
    daily_quota   INTEGER NOT NULL,           -- 租户级日额度，所有凭证共享（R2）
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admins (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT REFERENCES tenants(id),   -- 平台运营账号没有租户，写 NULL
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL,              -- ops / tenant_admin
    created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS credentials (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL REFERENCES tenants(id),
    name           TEXT NOT NULL,
    key_hash       TEXT NOT NULL UNIQUE,
    status         TEXT NOT NULL,             -- active / disabled
    created_at     TEXT NOT NULL,
    created_by     TEXT NOT NULL,
    disabled_at    TEXT,
    disabled_by    TEXT,
    disable_reason TEXT
);

-- 一次**已受理**的调用请求。request_no 在租户内全局唯一且跨日不复用（R3），
-- 因此唯一索引建在 (tenant_id, request_no) 上而不是按日分区。
-- 被拒绝的尝试不入此表（见 rejected_attempts），以免占用请求编号。
CREATE TABLE IF NOT EXISTS model_requests (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL REFERENCES tenants(id),
    credential_id   TEXT NOT NULL REFERENCES credentials(id),
    request_no      TEXT NOT NULL,
    params_hash     TEXT NOT NULL,
    day             TEXT NOT NULL,            -- UTC 自然日，首次受理时间归属（R2）
    status          TEXT NOT NULL,            -- processing/success/failed/timeout
    model           TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    upstream_result TEXT,
    error_reason    TEXT,
    received_at     TEXT NOT NULL,
    completed_at    TEXT,
    replay_count    INTEGER NOT NULL DEFAULT 0 -- 幂等命中次数，不重复计量（R3）
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_model_requests_no
    ON model_requests(tenant_id, request_no);
CREATE INDEX IF NOT EXISTS ix_model_requests_day
    ON model_requests(tenant_id, day, status);
CREATE INDEX IF NOT EXISTS ix_model_requests_credential
    ON model_requests(credential_id, day);

-- 被拒绝的调用尝试（独立表，不占用 request_no 唯一性）。
-- 供日终核查统计「被拒绝请求」（R8）。
CREATE TABLE IF NOT EXISTS rejected_attempts (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT,
    credential_id TEXT,
    request_no    TEXT,
    day           TEXT NOT NULL,               -- UTC 自然日
    status        TEXT NOT NULL,               -- 终态名：rejected
    reason        TEXT NOT NULL,
    actor         TEXT NOT NULL,               -- 发起方：ops/admin/credential
    occurred_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_rejected_attempts_day ON rejected_attempts(tenant_id, day);

-- 每租户每 UTC 自然日的用量计数（题面 R2 口径）。
-- 额度占用 = success_count + inflight_count；
-- 降额下限（R5）与剩余额度（R7）均以此表为准。
CREATE TABLE IF NOT EXISTS tenant_day_usage (
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    day           TEXT NOT NULL,
    success_count INTEGER NOT NULL DEFAULT 0,
    inflight_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day)
);

-- 配额变更留痕：操作人、原因、生效时间（R5）。
CREATE TABLE IF NOT EXISTS quota_changes (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants(id),
    old_quota    INTEGER NOT NULL,
    new_quota    INTEGER NOT NULL,
    operator     TEXT NOT NULL,
    reason       TEXT NOT NULL,
    effective_at TEXT NOT NULL,
    day          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_quota_changes_day ON quota_changes(tenant_id, day);

-- 停用等影响用量核查的业务事件（R6/R8）。
CREATE TABLE IF NOT EXISTS tenant_events (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL REFERENCES tenants(id),
    credential_id TEXT,
    kind          TEXT NOT NULL,              -- tenant_disabled / credential_disabled / tenant_enabled
    actor         TEXT NOT NULL,
    reason        TEXT NOT NULL,
    detail        TEXT,
    day           TEXT NOT NULL,
    occurred_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_tenant_events_day ON tenant_events(tenant_id, day);

-- 迟到结果等只作诊断、不改变计量口径的记录（R4）。
CREATE TABLE IF NOT EXISTS diagnostics (
    id          TEXT PRIMARY KEY,
    request_id  TEXT NOT NULL REFERENCES model_requests(id),
    tenant_id   TEXT NOT NULL REFERENCES tenants(id),
    kind        TEXT NOT NULL,                -- late_result
    detail      TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_diagnostics_request ON diagnostics(request_id);
CREATE INDEX IF NOT EXISTS ix_diagnostics_tenant ON diagnostics(tenant_id);

-- 会话令牌：登录后签发，角色与租户归属由服务端签发，客户端不可改（R1/R7）。
CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL,               -- admin / credential
    subject_id   TEXT NOT NULL,
    tenant_id    TEXT,
    role         TEXT,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

-- 教学时钟偏移（秒），用于跨日演示；业务规则本身不变。
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
