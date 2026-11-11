// Package domain 定义题目 05 的核心领域模型：租户、配额、凭证、调用请求与用量口径。
//
// 本包只放纯类型与纯函数（不依赖数据库与网络），便于对计量口径做精确单元测试。
// 所有规则编号 R1..R8 对应题面「需要完成的业务范围与规则」第 1–8 条。
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// TenantStatus 表示租户的服务状态。
type TenantStatus string

const (
	// TenantPending 表示租户已由运营建立但尚未开通服务，需运营执行「开通」。
	TenantPending TenantStatus = "pending"
	// TenantActive 表示租户服务已开通，可以受理调用。
	TenantActive TenantStatus = "active"
	// TenantDisabled 表示租户已被运营停用，不再受理新调用（题面 R6）。
	TenantDisabled TenantStatus = "disabled"
)

// CredentialStatus 表示应用凭证的状态。
type CredentialStatus string

const (
	// CredentialActive 表示凭证可用。
	CredentialActive CredentialStatus = "active"
	// CredentialDisabled 表示凭证已被管理员停用（题面 R6）。
	CredentialDisabled CredentialStatus = "disabled"
)

// RequestStatus 表示一次调用请求的终态或中间态。
type RequestStatus string

const (
	// RequestProcessing 表示已受理并占用额度，正在调用上游。
	RequestProcessing RequestStatus = "processing"
	// RequestSuccess 表示上游成功返回，消耗额度一次并计入成功量。
	RequestSuccess RequestStatus = "success"
	// RequestFailed 表示上游明确失败，释放额度占用且不计成功量（题面 R4）。
	RequestFailed RequestStatus = "failed"
	// RequestTimeout 表示超过 2 秒未返回，释放额度占用且不计成功量（题面 R4）。
	RequestTimeout RequestStatus = "timeout"
	// RequestRejected 表示受理前被拒绝，未调用上游、未占用额度（题面 R8）。
	RequestRejected RequestStatus = "rejected"
)

// SubjectType 标识会话主体类型。
type SubjectType string

const (
	// SubjectAdmin 表示平台运营或客户管理员。
	SubjectAdmin SubjectType = "admin"
	// SubjectCredential 表示客户应用凭证。
	SubjectCredential SubjectType = "credential"
)

// AdminRole 区分平台运营与客户管理员。
type AdminRole string

const (
	// AdminOps 是平台运营，可跨租户核查（题面 R7）。
	AdminOps AdminRole = "ops"
	// AdminTenant 是客户管理员，仅限本租户（题面 R7）。
	AdminTenant AdminRole = "tenant_admin"
)

// MaxQuota 是日额度的硬上界，防止误输入把额度改成天文数字。
const MaxQuota = 100000

// UpstreamTimeout 是题面 R4 规定的上游超时阈值。
const UpstreamTimeout = 2 * time.Second

// ErrQuotaExceeded 表示当日额度已用尽，请求被拒绝。
var ErrQuotaExceeded = errors.New("当日额度已用尽")

// ErrQuotaFloor 表示降额后的额度低于当日已成功数加处理中数（题面 R5）。
var ErrQuotaFloor = errors.New("降低额度不得低于当日已成功数量加处理中数量")

// ErrQuotaRange 表示额度取值超出允许范围。
var ErrQuotaRange = errors.New("额度取值超出允许范围")

// ErrReasonRequired 表示配额变更必须填写原因（题面 R5）。
var ErrReasonRequired = errors.New("配额变更原因不能为空")

// ErrRequestNoRequired 表示请求必须携带本租户全局唯一编号（题面 R3）。
var ErrRequestNoRequired = errors.New("请求编号不能为空")

// ErrRequestNoFormat 表示请求编号格式非法。
var ErrRequestNoFormat = errors.New("请求编号格式非法")

// ErrRequestNoConflict 表示同一编号被用于不同参数（题面 R3）。
var ErrRequestNoConflict = errors.New("请求编号已被占用且参数不一致")

// ErrTenantDisabled 表示租户已停用，不再受理新调用（题面 R6）。
var ErrTenantDisabled = errors.New("租户已停用，不再受理新调用")

// ErrCredentialDisabled 表示凭证已停用，不再受理新调用（题面 R6）。
var ErrCredentialDisabled = errors.New("凭证已停用，不再受理新调用")

// ErrCredentialLimit 表示凭证数量已达租户上限。
var ErrCredentialLimit = errors.New("凭证数量已达上限")

// ErrTenantMismatch 表示请求试图把资源绑定到其他租户（题面 R1）。
var ErrTenantMismatch = errors.New("凭证所属租户由系统确认，不允许指定其他租户")

// ErrForbidden 表示角色越权（题面 R7）。
var ErrForbidden = errors.New("无权访问该资源")

// ErrNotFound 表示目标对象不存在。
var ErrNotFound = errors.New("对象不存在")

// ErrUnauthorized 表示凭据缺失或无效。
var ErrUnauthorized = errors.New("身份认证失败")

// ErrUpstreamFailure 表示上游明确失败（题面 R4）。
var ErrUpstreamFailure = errors.New("上游明确失败")

// requestNoPattern 约束请求编号为 1–64 位的字母、数字、连字符与下划线。
var requestNoPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Tenant 是被服务的企业租户，配额挂在租户而非凭证上（题面 R2）。
type Tenant struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Status     TenantStatus `json:"status"`
	DailyQuota int          `json:"daily_quota"`
	CreatedAt  time.Time    `json:"created_at"`
}

// Admin 是平台运营或客户管理员账号。
type Admin struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Username  string    `json:"username"`
	Role      AdminRole `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	// PasswordHash 只落库、只用于校验，绝不进入任何响应。
	PasswordHash string `json:"-"`
}

// Credential 是客户应用使用的访问凭证，归属租户由服务端确定（题面 R1）。
type Credential struct {
	ID          string           `json:"id"`
	TenantID    string           `json:"tenant_id"`
	Name        string           `json:"name"`
	Status      CredentialStatus `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	DisabledAt  *time.Time       `json:"disabled_at,omitempty"`
	CreatedBy   string           `json:"created_by"`
	DisabledBy  string           `json:"disabled_by,omitempty"`
	DisableWhy  string           `json:"disable_reason,omitempty"`
	ReplayCount int              `json:"replay_count"`
	// KeyHash 是密钥摘要，绝不进入任何响应。
	KeyHash string `json:"-"`
}

// ModelRequest 是一次调用请求，是计量与核查的基本单元。
type ModelRequest struct {
	ID             string        `json:"id"`
	TenantID       string        `json:"tenant_id"`
	CredentialID   string        `json:"credential_id"`
	RequestNo      string        `json:"request_no"`
	ParamsHash     string        `json:"params_hash"`
	Day            string        `json:"day"`
	Status         RequestStatus `json:"status"`
	Model          string        `json:"model"`
	Prompt         string        `json:"prompt"`
	UpstreamResult string        `json:"upstream_result,omitempty"`
	ErrorReason    string        `json:"error_reason,omitempty"`
	ReceivedAt     time.Time     `json:"received_at"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	ReplayCount    int           `json:"replay_count"`
}

// RejectedAttempt 记录一次被拒绝的调用尝试（题面 R8）。
// 独立于 model_requests 存储，因此不占用 request_no 的唯一性，
// 增额后应用仍可用同一编号重试。
type RejectedAttempt struct {
	ID           string        `json:"id"`
	TenantID     string        `json:"tenant_id"`
	CredentialID string        `json:"credential_id"`
	RequestNo    string        `json:"request_no"`
	Day          string        `json:"day"`
	Status       RequestStatus `json:"status"`
	Reason       string        `json:"reason"`
	Actor        string        `json:"actor"`
	OccurredAt   time.Time     `json:"occurred_at"`
}

// QuotaChange 记录一次配额变更（题面 R5 要求操作人、原因、生效时间）。
type QuotaChange struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	OldQuota    int       `json:"old_quota"`
	NewQuota    int       `json:"new_quota"`
	Operator    string    `json:"operator"`
	Reason      string    `json:"reason"`
	EffectiveAt time.Time `json:"effective_at"`
	Day         string    `json:"day"`
}

// TenantEvent 记录租户/凭证停用等影响用量核查的业务事件（题面 R8）。
type TenantEvent struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	CredentialID string    `json:"credential_id"`
	Kind         string    `json:"kind"`
	Actor        string    `json:"actor"`
	Reason       string    `json:"reason"`
	Detail       string    `json:"detail"`
	Day          string    `json:"day"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// Diagnostic 保存迟到结果等只作诊断、不改变计量的记录（题面 R4）。
type Diagnostic struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id"`
	TenantID   string    `json:"tenant_id"`
	Kind       string    `json:"kind"`
	Detail     string    `json:"detail"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Usage 是某租户某 UTC 自然日的用量口径汇总（题面 R2）。
type Usage struct {
	TenantID   string `json:"tenant_id"`
	Day        string `json:"day"`
	QuotaLimit int    `json:"quota_limit"`
	Success    int    `json:"success"`
	InFlight   int    `json:"in_flight"`
}

// DailyReport 是日终核查报表（题面 R8）。
type DailyReport struct {
	TenantID string `json:"tenant_id"`
	Day      string `json:"day"`
	// Counts 按终态分类计数，键为 success/failed/timeout/processing/rejected。
	Counts map[RequestStatus]int `json:"counts"`
	// UsageSuccess 是用量口径中的成功计数。
	UsageSuccess int `json:"usage_success"`
	// UsageInFlight 是用量口径中的处理中计数。
	UsageInFlight int `json:"usage_in_flight"`
	// QuotaLimit 是当日生效额度。
	QuotaLimit int `json:"quota_limit"`
	// Remaining 是剩余额度。
	Remaining int `json:"remaining"`
	// QuotaChanges 是当日配额变更。
	QuotaChanges []QuotaChange `json:"quota_changes"`
	// Events 是当日停用等事件。
	Events []TenantEvent `json:"events"`
	// Consistent 表示成功记录数与计入用量次数是否一致。
	Consistent bool `json:"consistent"`
	// MismatchNote 在不一致时说明差异。
	MismatchNote string `json:"mismatch_note,omitempty"`
}

// Remaining 返回剩余额度：额度 − (成功数 + 处理中数)。处理中按题面 R2 计入占用。
func (u Usage) Remaining() int {
	r := u.QuotaLimit - (u.Success + u.InFlight)
	if r < 0 {
		return 0
	}
	return r
}

// Occupied 返回已占用额度 = 成功数 + 处理中数（题面 R2/R5 降额下限口径）。
func (u Usage) Occupied() int { return u.Success + u.InFlight }

// DayOf 返回时刻所在 UTC 自然日，格式 2006-01-02（题面 R2）。
func DayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

// ValidateRequestNo 校验请求编号非空且格式合法（题面 R3）。
func ValidateRequestNo(no string) error {
	if strings.TrimSpace(no) == "" {
		return ErrRequestNoRequired
	}
	if !requestNoPattern.MatchString(no) {
		return ErrRequestNoFormat
	}
	return nil
}

// ParamsFingerprint 计算参与上游调用的业务参数的规范化摘要（题面 R3 的「参数」）。
// 同编号同摘要视为同一次调用（幂等命中）；同编号异摘要必须拒绝。
func ParamsFingerprint(model, prompt string, params map[string]any) (string, error) {
	canonical := struct {
		Model  string         `json:"model"`
		Prompt string         `json:"prompt"`
		Params map[string]any `json:"params"`
	}{Model: model, Prompt: prompt, Params: params}

	// 归一化：剔除 nil 参数并按键排序，确保同一组参数得到稳定摘要。
	if canonical.Params != nil {
		cleaned := make(map[string]any, len(canonical.Params))
		for k, v := range canonical.Params {
			if v == nil {
				continue
			}
			cleaned[k] = v
		}
		canonical.Params = cleaned
	}

	buf, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("规范化调用参数失败: %w", err)
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// HashSecret 计算凭证与口令的存储摘要。
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte("modelquota/v1|" + secret))
	return hex.EncodeToString(sum[:])
}

// ValidateQuota 校验额度取值范围（题面 R5，配额硬上界 MaxQuota）。
func ValidateQuota(quota int) error {
	if quota < 0 || quota > MaxQuota {
		return fmt.Errorf("%w: 允许范围 0..%d", ErrQuotaRange, MaxQuota)
	}
	return nil
}

// ValidateQuotaChange 校验降额下限：不得低于当日已成功数量加处理中数量（题面 R5）。
func ValidateQuotaChange(newQuota int, current Usage) error {
	if err := ValidateQuota(newQuota); err != nil {
		return err
	}
	if newQuota < current.Occupied() {
		return fmt.Errorf("%w: 当日已成功 %d + 处理中 %d = %d",
			ErrQuotaFloor, current.Success, current.InFlight, current.Occupied())
	}
	return nil
}

// BuildDailyReport 汇总终态计数并核对「成功记录数 == 计入用量次数」（题面 R8）。
func BuildDailyReport(tenantID, day string, counts map[RequestStatus]int, usage Usage, changes []QuotaChange, events []TenantEvent) DailyReport {
	success := counts[RequestSuccess]
	report := DailyReport{
		TenantID:      tenantID,
		Day:           day,
		Counts:        counts,
		UsageSuccess:  usage.Success,
		UsageInFlight: usage.InFlight,
		QuotaLimit:    usage.QuotaLimit,
		Remaining:     usage.Remaining(),
		QuotaChanges:  changes,
		Events:        events,
	}
	report.Consistent = success == usage.Success
	if !report.Consistent {
		report.MismatchNote = fmt.Sprintf("成功记录数 %d 与计入用量次数 %d 不一致", success, usage.Success)
	}
	return report
}

// CountOf 返回某终态的计数。
//
// 模板里无法直接用字符串下标访问 map[RequestStatus]int（键类型不匹配），
// 因此页面统一通过本方法取值。
func (r DailyReport) CountOf(status RequestStatus) int {
	return r.Counts[status]
}

// StatusOrder 定义终态的稳定展示顺序，便于报表与页面输出确定性。
var StatusOrder = []RequestStatus{
	RequestSuccess,
	RequestFailed,
	RequestTimeout,
	RequestProcessing,
	RequestRejected,
}

// SortStatuses 按稳定顺序返回给定终态集合。
func SortStatuses(all []RequestStatus) []RequestStatus {
	seen := make(map[RequestStatus]bool, len(all))
	out := make([]RequestStatus, 0, len(all))
	for _, s := range StatusOrder {
		if seen[s] {
			out = append(out, s)
		}
	}
	for _, s := range all {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return statusRank(out[i]) < statusRank(out[j])
	})
	return out
}

func statusRank(s RequestStatus) int {
	for i, v := range StatusOrder {
		if v == s {
			return i
		}
	}
	return len(StatusOrder)
}
