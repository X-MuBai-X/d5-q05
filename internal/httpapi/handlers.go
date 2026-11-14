package httpapi

import (
	"fmt"
	"net/http"

	"modelquota/internal/domain"
	"modelquota/internal/service"
)

// ---------- 租户管理员接口（R1/R6/R7） ----------

func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	tenantID, err := s.tenantScope(r, identity)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	creds, err := s.svc.DB().ListCredentialsByTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

type createCredentialRequest struct {
	Name string `json:"name"`
	// TenantID 故意不作为参数使用：凭证所属租户由系统确认（R1）。
	// 保留字段仅为让越权尝试能被显式拒绝，而不是静默忽略。
	TenantID string `json:"tenant_id"`
}

// handleCreateCredential 为客户管理员创建应用凭证（R1）。
//
// 关键点：
//   - 请求体里即使带了 tenant_id，只要与当前身份不符就**显式拒绝**，
//     而不是静默忽略（共同要求：不能通过修改请求中的编号越权操作）；
//   - 凭证所属租户取自登录身份，不取自请求参数；
//   - 密钥只在创建时返回一次：表单提交直接渲染含密钥的页面，不走重定向。
func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in createCredentialRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// 越权尝试：请求体指定了其他租户 —— 直接拒绝（R1）。
	if in.TenantID != "" && in.TenantID != identity.TenantID {
		writeError(w, statusFor(domain.ErrTenantMismatch), domain.ErrTenantMismatch)
		return
	}
	cred, secret, err := s.svc.CreateCredential(r.Context(), identity.Username, identity.TenantID,
		service.CredentialInput{Name: in.Name})
	if err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/admin", err.Error())
		return
	}
	// 表单提交：密钥只返回一次，不能重定向丢失，直接渲染一次含密钥的页面。
	if isFormRequest(r) {
		s.renderAdminWithSecret(w, r, identity, cred, secret)
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{"credential": cred, "secret": secret}, "/admin",
		"凭证 "+cred.Name+" 已创建，密钥仅显示一次，请立即保存")
}

type disableCredentialRequest struct {
	CredentialID string `json:"credential_id"`
	Reason       string `json:"reason"`
}

// handleDisableCredential 停用指定凭证（R6）。
//
// 停用只影响后续新调用：在途请求仍按原规则完成结算。
func (s *Server) handleDisableCredential(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in disableCredentialRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DisableCredential(r.Context(), identity.Username, identity.TenantID, in.CredentialID, in.Reason); err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/admin", err.Error())
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{"status": "disabled", "credential_id": in.CredentialID},
		"/admin", "凭证 "+in.CredentialID+" 已停用")
}

// handleTenantUsage 查询租户的用量与剩余额度（R2/R7）。
//
// 返回口径与题面一致：occupied = 成功数 + 处理中数，remaining = 日额度 − occupied。
// 管理员查本租户；运营跨租户核查时需显式指定 tenant_id，否则返回 400。
func (s *Server) handleTenantUsage(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	tenantID, err := s.tenantScope(r, identity)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	day := dayParam(r, s.svc.Clock())
	usage, err := s.svc.TenantUsage(r.Context(), tenantID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"usage": map[string]any{
			"tenant_id":   usage.TenantID,
			"day":         usage.Day,
			"quota_limit": usage.QuotaLimit,
			"success":     usage.Success,
			"in_flight":   usage.InFlight,
			"occupied":    usage.Occupied(),
			"remaining":   usage.Remaining(),
		},
	})
}

// handleTenantRequests 查询租户当日受理的请求，以及被拒绝的尝试（R7/R8）。
//
// 「被拒绝的尝试」独立成表（R8 要求五类计数分列），不占用请求编号。
func (s *Server) handleTenantRequests(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	tenantID, err := s.tenantScope(r, identity)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	day := dayParam(r, s.svc.Clock())
	reqs, err := s.svc.RequestsByTenant(r.Context(), tenantID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	rejected, err := s.svc.RejectedAttempts(r.Context(), tenantID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs, "rejected": rejected})
}

// ---------- 客户应用接口（R3/R4/R7） ----------

type invokeRequest struct {
	RequestNo string         `json:"request_no"`
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	Params    map[string]any `json:"params"`
	// Scenario 是页面演示用的便捷字段，等价于 params.scenario。
	// 当 params 中已存在 scenario 时以 params 为准。
	Scenario string `json:"scenario"`
}

// effectiveParams 合并场景参数，得到真正传给上游的参数。
func (in invokeRequest) effectiveParams() map[string]any {
	if in.Scenario == "" {
		return in.Params
	}
	if in.Params == nil {
		return map[string]any{"scenario": in.Scenario}
	}
	if _, ok := in.Params["scenario"]; ok {
		return in.Params
	}
	merged := make(map[string]any, len(in.Params)+1)
	for k, v := range in.Params {
		merged[k] = v
	}
	merged["scenario"] = in.Scenario
	return merged
}

// handleInvoke 受理客户应用的模型调用请求（R3/R4）。
//
// 调用链：service.Invoke 依次做「编号校验 → 幂等命中 → 状态校验 → 原子占额度 →
// 调上游 → 计量结算」，这里只负责取参、传参、返回结果与提示文案。
func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in invokeRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.svc.Invoke(r.Context(), "credential:"+identity.SubjectID, identity.TenantID, identity.SubjectID,
		service.InvokeInput{
			RequestNo: in.RequestNo,
			Model:     in.Model,
			Prompt:    in.Prompt,
			Params:    in.effectiveParams(),
		})
	if err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/app", err.Error())
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{
		"request":     result.Request,
		"replayed":    result.Replayed,
		"usage":       result.Usage,
		"diagnostics": result.Diagnostics,
	}, "/app", "调用 "+in.RequestNo+" 终态："+string(result.Request.Status))
}

// handleCredentialRequests 查询本凭证当日创建的请求（R7）。
//
// 范围限定在当前凭证：应用看不到同租户其它凭证的请求。
func (s *Server) handleCredentialRequests(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	day := dayParam(r, s.svc.Clock())
	reqs, err := s.svc.RequestsByCredential(r.Context(), identity.SubjectID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

// handleGetRequest 按请求编号查询单条请求及其诊断记录（R7）。
//
// 应用仅可查询由**其凭证**创建的请求，查别人的返回 403；
// 诊断记录一并返回，便于观察迟到结果（只诊断、不改终态）。
func (s *Server) handleGetRequest(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	requestNo := r.URL.Query().Get("request_no")
	if requestNo == "" {
		writeError(w, http.StatusBadRequest, domain.ErrRequestNoRequired)
		return
	}
	req, err := s.svc.DB().GetRequestByNo(r.Context(), identity.TenantID, requestNo)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	// 应用仅可查询由其凭证创建的请求（R7）。
	if req.CredentialID != identity.SubjectID {
		writeError(w, statusFor(domain.ErrForbidden), domain.ErrForbidden)
		return
	}
	diags, err := s.svc.DB().ListDiagnosticsByRequest(r.Context(), req.ID)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": req, "diagnostics": diags})
}
