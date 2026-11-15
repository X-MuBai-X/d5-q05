// 本文件定义对外的错误码契约（对应需求文档 §10「异常与错误码处置矩阵」）。
//
// 设计要点：
//   - 错误码是**对外契约**，客户端据此判断原因，必须稳定、不随文案变化；
//   - 文案（message）面向人，可以调整；错误码（code）面向程序，不能随意改；
//   - 领域层只定义「是什么错」，HTTP 状态码与错误码的映射集中在本文件，
//     避免同一个错在不同接口上返回不同的码。

package domain

import "errors"

// 对外错误码。取值与需求文档 §10 的矩阵一一对应。
const (
	// CodeSuccess 表示受理成功。
	CodeSuccess = "SUCCESS"
	// CodeIdempotentHit 表示同编号同参数重复请求，命中历史结果（200，不重复计量）。
	CodeIdempotentHit = "IDEMPOTENT_HIT"
	// CodeInvalidParams 表示缺少或非法参数（400）。
	CodeInvalidParams = "INVALID_PARAMS"
	// CodeKeyNotFound 表示凭证无效或未提供（401）。
	CodeKeyNotFound = "KEY_NOT_FOUND"
	// CodeKeySuspended 表示凭证已被停用（403）。
	CodeKeySuspended = "KEY_SUSPENDED"
	// CodeTenantSuspended 表示租户已被停用（403）。
	CodeTenantSuspended = "TENANT_SUSPENDED"
	// CodeScopeDenied 表示越权访问（403）。
	CodeScopeDenied = "SCOPE_DENIED"
	// CodeIdempotencyConflict 表示同编号改参数复用（409）。
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
	// CodeQuotaExceeded 表示当日共享额度耗尽（429）。
	CodeQuotaExceeded = "QUOTA_EXCEEDED"
	// CodeUpstreamError 表示上游明确失败（502）。
	CodeUpstreamError = "UPSTREAM_ERROR"
	// CodeGatewayTimeout 表示上游超时熔断（504）。
	CodeGatewayTimeout = "GATEWAY_TIMEOUT"

	// 以下两个码用于**管理类接口**（凭证创建、租户开通等），不在受理路径的矩阵内，
	// 单独列出以免与受理路径的码混用。
	// CodeCredentialLimit 表示该租户凭证数已达上限（400）。
	CodeCredentialLimit = "CREDENTIAL_LIMIT_REACHED"
	// CodeTenantNameRequired 表示新建租户缺少名称（400）。
	CodeTenantNameRequired = "TENANT_NAME_REQUIRED"
	// CodeQuotaFloorViolation 表示降额低于当日已成功数加处理中数，被安全底线拦截（400）。
	CodeQuotaFloorViolation = "QUOTA_FLOOR_VIOLATION"
	// CodeReasonRequired 表示缺少操作原因（400）。
	CodeReasonRequired = "REASON_REQUIRED"
	// CodeQuotaRangeInvalid 表示额度取值超出允许范围（400）。
	CodeQuotaRangeInvalid = "QUOTA_RANGE_INVALID"
	// CodeNotFound 表示目标对象不存在（404）。
	CodeNotFound = "NOT_FOUND"
	// CodeTenantScopeRequired 表示运营跨租户核查时必须显式指定租户（400）。
	CodeTenantScopeRequired = "TENANT_SCOPE_REQUIRED"
	// CodeInternal 表示服务端内部错误（500）。
	CodeInternal = "INTERNAL_ERROR"
)

// CodeOf 把领域错误映射为对外错误码。
//
// 映射表与需求文档 §10 的矩阵一致；命中矩阵外的错误一律回 CodeInternal，
// 避免把「说不清的原因」伪装成某个具体业务码。
func CodeOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrQuotaExceeded):
		return CodeQuotaExceeded
	case errors.Is(err, ErrRequestNoConflict):
		return CodeIdempotencyConflict
	case errors.Is(err, ErrUnauthorized):
		return CodeKeyNotFound
	case errors.Is(err, ErrCredentialDisabled):
		return CodeKeySuspended
	case errors.Is(err, ErrTenantDisabled):
		return CodeTenantSuspended
	case errors.Is(err, ErrForbidden):
		return CodeScopeDenied
	case errors.Is(err, ErrCredentialLimit):
		return CodeCredentialLimit
	case errors.Is(err, ErrTenantNameRequired):
		return CodeTenantNameRequired
	case errors.Is(err, ErrQuotaFloor):
		return CodeQuotaFloorViolation
	case errors.Is(err, ErrReasonRequired):
		return CodeReasonRequired
	case errors.Is(err, ErrQuotaRange):
		return CodeQuotaRangeInvalid
	case errors.Is(err, ErrRequestNoRequired), errors.Is(err, ErrRequestNoFormat),
		errors.Is(err, ErrTenantMismatch):
		// 凭证所属租户由系统确认：请求参数里指定别的租户属于参数问题，
		// 必须落到明确业务码，不能掉进 INTERNAL_ERROR。
		return CodeInvalidParams
	case errors.Is(err, ErrTenantScopeRequired):
		return CodeTenantScopeRequired
	case errors.Is(err, ErrNotFound):
		return CodeNotFound
	case errors.Is(err, ErrUpstreamFailure):
		return CodeUpstreamError
	default:
		return CodeInternal
	}
}
