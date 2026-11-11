// Package service 实现题目 05 的业务编排：租户开通、凭证管理、配额调整、
// 调用受理与计量、日终核查。
//
// 计量口径集中在这一层，保证 R2/R4/R5 三条规则的口径一致：
//   - 额度占用 = 当日成功数 + 当日处理中数；
//   - 成功：处理中 −1、成功 +1（占用不变，实扣一次）；
//   - 失败/超时：处理中 −1（释放占用，不计成功量）；
//   - 幂等命中：不新增记录、不占额度、不调用上游。
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/store"
	"modelquota/internal/upstream"
)

// Clock 提供当前时间。抽象出来是为了支持教学时钟（跨日演示）与确定性测试。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用真实 UTC 时间。
type SystemClock struct{}

// Now 返回当前 UTC 时间。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// OffsetClock 在系统时间上叠加一个可配置的偏移量，用于跨日演示。
// 偏移只影响「当前时间」，业务规则本身不变。
type OffsetClock struct {
	Base   Clock
	Offset time.Duration
}

// Now 返回叠加偏移后的 UTC 时间。
func (c OffsetClock) Now() time.Time {
	base := c.Base
	if base == nil {
		base = SystemClock{}
	}
	return base.Now().Add(c.Offset).UTC()
}

// Service 是题目 05 的业务服务。
type Service struct {
	db  *store.DB
	clk Clock
	up  upstream.Client
	// MaxCredentialsPerTenant 是租户级凭证数量硬上限（题面预置条件「最多创建 2 个凭证」）。
	MaxCredentialsPerTenant int
	// SessionTTL 是会话有效期。
	SessionTTL time.Duration
	// EnableTestHooks 控制是否开放测试钩子（含迟到结果注入）。
	EnableTestHooks bool
	// TestHooks 收集待执行的测试钩子动作。
	TestHooks []TestHook
}

// TestHook 是平台侧测试钩子动作。依确认结论 Q11，迟到结果由平台侧钩子产生。
type TestHook struct {
	// Kind 为 late_result 时，在请求已按超时结案后写入诊断记录。
	Kind string
	// RequestNo 指向目标请求编号。
	RequestNo string
	// Detail 是诊断详情。
	Detail string
}

// New 构造业务服务。
func New(db *store.DB, clk Clock, up upstream.Client) *Service {
	if clk == nil {
		clk = SystemClock{}
	}
	return &Service{
		db:                      db,
		clk:                     clk,
		up:                      up,
		MaxCredentialsPerTenant: 2,
		SessionTTL:              12 * time.Hour,
		EnableTestHooks:         false,
	}
}

// DB 暴露持久化句柄，供 HTTP 层与测试使用。
func (s *Service) DB() *store.DB { return s.db }

// Clock 返回当前时钟。
func (s *Service) Clock() Clock { return s.clk }

// SetClockOffset 设置教学时钟偏移（仅影响「当前时间」，业务规则不变）。
func (s *Service) SetClockOffset(d time.Duration) {
	s.clk = OffsetClock{Base: SystemClock{}, Offset: d}
}

// CurrentDay 返回当前 UTC 自然日。
func (s *Service) CurrentDay() string { return domain.DayOf(s.clk.Now()) }

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时退回时间戳，保证流程可继续。
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// ---------- 租户与配额（R1/R5） ----------

// TenantInput 是运营开通/调整租户时的输入。
type TenantInput struct {
	Name       string
	DailyQuota int
	Reason     string
	// TenantID 为空时新建租户；非空时表示对该租户执行开通或调整。
	TenantID string
}

// OpenTenant 为租户开通服务并设置额度（R1）。
// 若 TenantID 为空则新建租户；否则把已有租户置为 active。
func (s *Service) OpenTenant(ctx context.Context, operator string, in TenantInput) (*domain.Tenant, error) {
	if strings.TrimSpace(in.Name) == "" && in.TenantID == "" {
		return nil, fmt.Errorf("租户名称不能为空")
	}
	if err := domain.ValidateQuota(in.DailyQuota); err != nil {
		return nil, err
	}
	now := s.clk.Now()

	if in.TenantID == "" {
		tenant := &domain.Tenant{
			ID:         newID("tenant"),
			Name:       in.Name,
			Status:     domain.TenantActive,
			DailyQuota: in.DailyQuota,
			CreatedAt:  now,
		}
		if err := s.db.CreateTenant(ctx, tenant); err != nil {
			return nil, err
		}
		change := &domain.QuotaChange{
			ID:          newID("qc"),
			TenantID:    tenant.ID,
			OldQuota:    0,
			NewQuota:    in.DailyQuota,
			Operator:    operator,
			Reason:      firstNonEmpty(in.Reason, "开通租户并设置初始日额度"),
			EffectiveAt: now,
			Day:         domain.DayOf(now),
		}
		if err := s.db.InsertQuotaChange(ctx, change); err != nil {
			return nil, err
		}
		return tenant, nil
	}

	tenant, err := s.db.GetTenant(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}
	oldQuota := tenant.DailyQuota
	if err := s.db.UpdateTenantStatus(ctx, tenant.ID, domain.TenantActive); err != nil {
		return nil, err
	}
	if err := s.db.UpdateTenantQuota(ctx, tenant.ID, in.DailyQuota); err != nil {
		return nil, err
	}
	change := &domain.QuotaChange{
		ID:          newID("qc"),
		TenantID:    tenant.ID,
		OldQuota:    oldQuota,
		NewQuota:    in.DailyQuota,
		Operator:    operator,
		Reason:      firstNonEmpty(in.Reason, "开通租户并设置日额度"),
		EffectiveAt: now,
		Day:         domain.DayOf(now),
	}
	if err := s.db.InsertQuotaChange(ctx, change); err != nil {
		return nil, err
	}
	if err := s.db.InsertEvent(ctx, &domain.TenantEvent{
		ID:         newID("ev"),
		TenantID:   tenant.ID,
		Kind:       "tenant_enabled",
		Actor:      operator,
		Reason:     change.Reason,
		Detail:     fmt.Sprintf("日额度 %d -> %d", oldQuota, in.DailyQuota),
		Day:        domain.DayOf(now),
		OccurredAt: now,
	}); err != nil {
		return nil, err
	}
	tenant.Status = domain.TenantActive
	tenant.DailyQuota = in.DailyQuota
	return tenant, nil
}

// AdjustQuota 调整租户日额度（R5）。
// 降低额度时不得低于当日已成功数量加处理中数量；每次变更记录操作人、原因和生效时间。
func (s *Service) AdjustQuota(ctx context.Context, operator, tenantID string, newQuota int, reason string) (*domain.QuotaChange, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, domain.ErrReasonRequired
	}
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	day := domain.DayOf(now)

	usage, err := s.db.GetDayUsage(ctx, tenantID, day, tenant.DailyQuota)
	if err != nil {
		return nil, err
	}
	// 降额下限按 UTC 当日口径（确认结论 Q2）。
	if err := domain.ValidateQuotaChange(newQuota, usage); err != nil {
		return nil, err
	}

	oldQuota := tenant.DailyQuota
	if err := s.db.UpdateTenantQuota(ctx, tenantID, newQuota); err != nil {
		return nil, err
	}
	change := &domain.QuotaChange{
		ID:          newID("qc"),
		TenantID:    tenantID,
		OldQuota:    oldQuota,
		NewQuota:    newQuota,
		Operator:    operator,
		Reason:      reason,
		EffectiveAt: now,
		Day:         day,
	}
	if err := s.db.InsertQuotaChange(ctx, change); err != nil {
		return nil, err
	}
	return change, nil
}

// DisableTenant 停用租户（R6）。停用后不再受理新调用，已受理请求正常结束。
func (s *Service) DisableTenant(ctx context.Context, operator, tenantID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return domain.ErrReasonRequired
	}
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	if err := s.db.UpdateTenantStatus(ctx, tenantID, domain.TenantDisabled); err != nil {
		return err
	}
	return s.db.InsertEvent(ctx, &domain.TenantEvent{
		ID:         newID("ev"),
		TenantID:   tenantID,
		Kind:       "tenant_disabled",
		Actor:      operator,
		Reason:     reason,
		Detail:     fmt.Sprintf("停用租户 %s", tenant.Name),
		Day:        domain.DayOf(now),
		OccurredAt: now,
	})
}

// ---------- 凭证（R1/R6） ----------

// CredentialInput 是管理员创建凭证的输入。
type CredentialInput struct {
	Name string
}

// CreateCredential 由客户管理员为本租户创建凭证（R1）。
// 凭证所属租户由系统从调用者身份确定，不接受请求参数指定（R1）。
func (s *Service) CreateCredential(ctx context.Context, actor, tenantID string, in CredentialInput) (*domain.Credential, string, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, "", fmt.Errorf("凭证名称不能为空")
	}
	count, err := s.db.CountCredentials(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}
	if count >= s.MaxCredentialsPerTenant {
		return nil, "", fmt.Errorf("%w: 每个租户最多 %d 个凭证", domain.ErrCredentialLimit, s.MaxCredentialsPerTenant)
	}

	secret := newID("sk")
	now := s.clk.Now()
	cred := &domain.Credential{
		ID:        newID("cred"),
		TenantID:  tenantID,
		Name:      in.Name,
		KeyHash:   domain.HashSecret(secret),
		Status:    domain.CredentialActive,
		CreatedAt: now,
		CreatedBy: actor,
	}
	if err := s.db.CreateCredential(ctx, cred); err != nil {
		return nil, "", err
	}
	return cred, secret, nil
}

// DisableCredential 由客户管理员停用本租户凭证（R6）。
func (s *Service) DisableCredential(ctx context.Context, actor, tenantID, credentialID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return domain.ErrReasonRequired
	}
	cred, err := s.db.GetCredential(ctx, credentialID)
	if err != nil {
		return err
	}
	if cred.TenantID != tenantID {
		return domain.ErrTenantMismatch
	}
	now := s.clk.Now()
	if err := s.db.DisableCredential(ctx, credentialID, actor, reason, now); err != nil {
		return err
	}
	return s.db.InsertEvent(ctx, &domain.TenantEvent{
		ID:           newID("ev"),
		TenantID:     tenantID,
		CredentialID: credentialID,
		Kind:         "credential_disabled",
		Actor:        actor,
		Reason:       reason,
		Detail:       fmt.Sprintf("停用凭证 %s", cred.Name),
		Day:          domain.DayOf(now),
		OccurredAt:   now,
	})
}

// ---------- 调用受理与计量（R2/R3/R4） ----------

// InvokeInput 是一次调用请求的输入。
type InvokeInput struct {
	RequestNo string
	Model     string
	Prompt    string
	Params    map[string]any
}

// InvokeResult 是一次调用的结果。
type InvokeResult struct {
	// Request 是受理后的请求记录。
	Request *domain.ModelRequest
	// Replayed 表示本次为幂等命中，未调用上游、未重复计量（R3）。
	Replayed bool
	// Usage 是本次租户当日的用量口径。
	Usage domain.Usage
	// Diagnostics 是与该请求相关的诊断记录（迟到结果等）。
	Diagnostics []domain.Diagnostic
}

// Invoke 受理一次模型调用并完成计量。
//
// 受理顺序严格对应题面规则：
//  1. 校验请求编号（R3）；
//  2. 幂等判定：同编号同参数 → 直接返回原记录，不调用上游、不重复计量（R3）；
//     同编号异参数 → 拒绝；
//  3. 租户/凭证状态校验（R6）；
//  4. 原子占用额度名额，额度满则拒绝（R2）；
//  5. 调用上游，按 2 秒时限区分成功/失败/超时（R4）。
func (s *Service) Invoke(ctx context.Context, actor, tenantID, credentialID string, in InvokeInput) (*InvokeResult, error) {
	now := s.clk.Now()
	day := domain.DayOf(now)

	if err := domain.ValidateRequestNo(in.RequestNo); err != nil {
		s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
		return nil, err
	}
	paramsHash, err := domain.ParamsFingerprint(in.Model, in.Prompt, in.Params)
	if err != nil {
		return nil, err
	}

	// 幂等判定（R3）：先查同编号请求。
	existing, err := s.db.GetRequestByNo(ctx, tenantID, in.RequestNo)
	switch {
	case err == nil:
		if existing.ParamsHash != paramsHash {
			err := domain.ErrRequestNoConflict
			s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
			return nil, err
		}
		// 同编号同参数：幂等命中。不调用上游、不占额度、不重复计量。
		if err := s.db.IncrementReplay(ctx, existing.ID); err != nil {
			return nil, err
		}
		existing.ReplayCount++
		usage, err := s.currentUsage(ctx, tenantID, day)
		if err != nil {
			return nil, err
		}
		diags, err := s.db.ListDiagnosticsByRequest(ctx, existing.ID)
		if err != nil {
			return nil, err
		}
		return &InvokeResult{Request: existing, Replayed: true, Usage: usage, Diagnostics: diags}, nil
	case errors.Is(err, domain.ErrNotFound):
		// 继续正常受理流程。
	default:
		return nil, err
	}

	// 租户状态校验（R6）：停用后不再受理新调用。
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant.Status == domain.TenantDisabled {
		err := domain.ErrTenantDisabled
		s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
		return nil, err
	}

	// 凭证状态校验（R6）。
	cred, err := s.db.GetCredential(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if cred.TenantID != tenantID {
		err := domain.ErrTenantMismatch
		s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
		return nil, err
	}
	if cred.Status == domain.CredentialDisabled {
		err := domain.ErrCredentialDisabled
		s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
		return nil, err
	}

	// 原子占用额度名额（R2）：所有凭证共享租户日额度。
	reserved, err := s.db.ReserveInflight(ctx, tenantID, day, tenant.DailyQuota)
	if err != nil {
		return nil, err
	}
	if !reserved {
		err := domain.ErrQuotaExceeded
		s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, err, actor, now)
		return nil, err
	}

	req := &domain.ModelRequest{
		ID:           newID("req"),
		TenantID:     tenantID,
		CredentialID: credentialID,
		RequestNo:    in.RequestNo,
		ParamsHash:   paramsHash,
		Day:          day,
		Status:       domain.RequestProcessing,
		Model:        in.Model,
		Prompt:       in.Prompt,
		ReceivedAt:   now,
	}
	if err := s.db.InsertRequest(ctx, req); err != nil {
		// 可能是并发下同一编号被另一个请求抢先写入：此时按幂等处理（R3），
		// 而不是把数据库唯一约束错误抛给应用。
		if existing, getErr := s.db.GetRequestByNo(ctx, tenantID, in.RequestNo); getErr == nil {
			if existing.ParamsHash != paramsHash {
				_ = s.db.ReleaseInflight(ctx, tenantID, day)
				s.recordRejected(ctx, tenantID, credentialID, in.RequestNo, domain.ErrRequestNoConflict, actor, now)
				return nil, domain.ErrRequestNoConflict
			}
			// 幂等命中：归还本次占用的额度名额，不重复计量、不重复调用上游。
			_ = s.db.ReleaseInflight(ctx, tenantID, day)
			if err := s.db.IncrementReplay(ctx, existing.ID); err != nil {
				return nil, err
			}
			existing.ReplayCount++
			usage, err := s.currentUsage(ctx, tenantID, day)
			if err != nil {
				return nil, err
			}
			diags, err := s.db.ListDiagnosticsByRequest(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			return &InvokeResult{Request: existing, Replayed: true, Usage: usage, Diagnostics: diags}, nil
		}
		// 写入失败必须归还已占用的额度名额，避免额度泄漏。
		if relErr := s.db.ReleaseInflight(ctx, tenantID, day); relErr != nil {
			return nil, fmt.Errorf("%w（且归还额度失败: %v）", err, relErr)
		}
		return nil, err
	}

	// 调用上游（R4）：2 秒时限。
	timeout := domain.UpstreamTimeout
	resp, callErr := s.up.Complete(ctx, upstream.Request{Model: in.Model, Prompt: in.Prompt, Params: in.Params}, timeout)

	if err := s.settle(ctx, req, resp, callErr); err != nil {
		return nil, err
	}

	// 平台侧测试钩子：迟到结果仅写诊断，不改变终态、不追加扣额（R4，确认结论 Q11）。
	s.runTestHooks(ctx, req)

	final, err := s.db.GetRequest(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	usage, err := s.currentUsage(ctx, tenantID, day)
	if err != nil {
		return nil, err
	}
	diags, err := s.db.ListDiagnosticsByRequest(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return &InvokeResult{Request: final, Usage: usage, Diagnostics: diags}, nil
}

// settle 把已受理的请求结算为终态，并同步计量口径（题面 R4）。
//
// 三条分支的计量口径完全一致：
//   - 成功：处理中 −1、成功 +1（总占用不变，实扣一次）；
//   - 失败：处理中 −1（释放占用，不计成功量）；
//   - 超时：处理中 −1（释放占用，不计成功量）。
func (s *Service) settle(ctx context.Context, req *domain.ModelRequest, resp *upstream.Response, callErr error) error {
	switch {
	case callErr == nil:
		if err := s.db.FinishRequest(ctx, req.ID, domain.RequestSuccess, resp.Content, "", s.clk.Now()); err != nil {
			return err
		}
		// 成功：处理中 −1、成功 +1，总占用不变（R2/R4）。
		return s.db.CommitSuccess(ctx, req.TenantID, req.Day)
	case errors.Is(callErr, upstream.ErrTimeout):
		if err := s.db.FinishRequest(ctx, req.ID, domain.RequestTimeout, "", "上游调用超过 2 秒，判为超时", s.clk.Now()); err != nil {
			return err
		}
		// 超时：释放额度占用，不计成功量（R4）。
		return s.db.ReleaseInflight(ctx, req.TenantID, req.Day)
	default:
		reason := callErr.Error()
		if errors.Is(callErr, upstream.ErrFailed) {
			reason = "上游明确失败"
		}
		if err := s.db.FinishRequest(ctx, req.ID, domain.RequestFailed, "", reason, s.clk.Now()); err != nil {
			return err
		}
		// 失败：释放额度占用，不计成功量（R4）。
		return s.db.ReleaseInflight(ctx, req.TenantID, req.Day)
	}
}

// runTestHooks 执行平台侧测试钩子。
// 依确认结论 Q11，迟到结果由平台侧钩子产生：仅写 diagnostics，
// 不修改请求终态、不追加扣额、不计入成功量（R4）。
func (s *Service) runTestHooks(ctx context.Context, req *domain.ModelRequest) {
	if !s.EnableTestHooks {
		return
	}
	for _, hook := range s.TestHooks {
		if hook.Kind != "late_result" || hook.RequestNo != req.RequestNo {
			continue
		}
		detail := hook.Detail
		if detail == "" {
			detail = fmt.Sprintf("迟到结果（上游在超时后返回）：request_no=%s", req.RequestNo)
		}
		// 忽略写入失败：诊断记录不得影响主流程结论。
		_ = s.db.InsertDiagnostic(ctx, &domain.Diagnostic{
			ID:         newID("diag"),
			RequestID:  req.ID,
			TenantID:   req.TenantID,
			Kind:       "late_result",
			Detail:     detail,
			RecordedAt: s.clk.Now(),
		})
	}
}

// AddTestHook 注册一个测试钩子。
func (s *Service) AddTestHook(hook TestHook) { s.TestHooks = append(s.TestHooks, hook) }

// recordRejected 记录被拒绝的调用尝试（R8）。独立表存储，不占用 request_no 唯一性。
func (s *Service) recordRejected(ctx context.Context, tenantID, credentialID, requestNo string, cause error, actor string, at time.Time) {
	_ = s.db.InsertRejectedAttempt(ctx, &domain.RejectedAttempt{
		ID:           newID("rej"),
		TenantID:     tenantID,
		CredentialID: credentialID,
		RequestNo:    requestNo,
		Day:          domain.DayOf(at),
		Status:       domain.RequestRejected,
		Reason:       cause.Error(),
		Actor:        actor,
		OccurredAt:   at,
	})
}

// currentUsage 读取某租户某日的用量口径。
func (s *Service) currentUsage(ctx context.Context, tenantID, day string) (domain.Usage, error) {
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		return domain.Usage{}, err
	}
	return s.db.GetDayUsage(ctx, tenantID, day, tenant.DailyQuota)
}

// TenantUsage 汇总某租户某日的用量与额度（管理员视图，R7）。
func (s *Service) TenantUsage(ctx context.Context, tenantID, day string) (domain.Usage, error) {
	return s.currentUsage(ctx, tenantID, day)
}

// ---------- 日终核查（R8） ----------

// DailyReport 生成某租户某日的日终核查报表。
func (s *Service) DailyReport(ctx context.Context, tenantID, day string) (*domain.DailyReport, error) {
	tenant, err := s.db.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	usage, err := s.db.GetDayUsage(ctx, tenantID, day, tenant.DailyQuota)
	if err != nil {
		return nil, err
	}
	counts, err := s.db.CountRequestsByStatus(ctx, tenantID, day)
	if err != nil {
		return nil, err
	}
	// 被拒绝请求单独计数（R8，确认结论 Q7）。
	rejected, err := s.db.CountRejectedAttempts(ctx, tenantID, day)
	if err != nil {
		return nil, err
	}
	counts[domain.RequestRejected] = rejected

	changes, err := s.db.ListQuotaChanges(ctx, tenantID, day)
	if err != nil {
		return nil, err
	}
	events, err := s.db.ListEvents(ctx, tenantID, day)
	if err != nil {
		return nil, err
	}
	report := domain.BuildDailyReport(tenantID, day, counts, usage, changes, events)
	return &report, nil
}

// RequestsByTenant 返回某租户某日的请求明细（R8）。
func (s *Service) RequestsByTenant(ctx context.Context, tenantID, day string) ([]domain.ModelRequest, error) {
	return s.db.ListRequestsByTenant(ctx, tenantID, day)
}

// RequestsByCredential 返回某凭证某日创建的请求（R7，应用视图）。
func (s *Service) RequestsByCredential(ctx context.Context, credentialID, day string) ([]domain.ModelRequest, error) {
	return s.db.ListRequestsByCredential(ctx, credentialID, day)
}

// RejectedAttempts 返回某租户某日被拒绝的尝试（R8）。
func (s *Service) RejectedAttempts(ctx context.Context, tenantID, day string) ([]domain.RejectedAttempt, error) {
	return s.db.ListRejectedAttempts(ctx, tenantID, day)
}

// Diagnostics 返回某租户的诊断记录（R4/R8）。
func (s *Service) Diagnostics(ctx context.Context, tenantID string) ([]domain.Diagnostic, error) {
	return s.db.ListDiagnosticsByTenant(ctx, tenantID)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
