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

func (s *Server) handleCredentialRequests(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	day := dayParam(r, s.svc.Clock())
	reqs, err := s.svc.RequestsByCredential(r.Context(), identity.SubjectID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

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
