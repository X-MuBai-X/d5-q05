// Package httpapi 提供题目 05 的 HTTP 接口与可操作页面。
//
// 鉴权模型（确认结论 Q12 + 题面 R1/R7）：
//   - 管理员（运营/租户管理员）用预置账号口令登录，服务端签发会话令牌；
//   - 客户应用用凭证密钥登录；
//   - 身份与租户归属完全由服务端从令牌推导，请求参数中的租户编号一律不被采信。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"modelquota/internal/domain"
	"modelquota/internal/service"
)

const sessionCookie = "mq_session"

// Server 汇集 HTTP 处理器。
type Server struct {
	svc *service.Service
	mux *http.ServeMux
	// templates 是页面模板集合。
	templates *templates
}

// New 构造 HTTP 服务。
func New(svc *service.Service, tmpl *templates) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux(), templates: tmpl}
	s.routes()
	return s
}

// Handler 返回根处理器。
func (s *Server) Handler() http.Handler { return s.mux }

// routes 注册全部 HTTP 路由。
//
// 路由按角色分组：运营接口套 opsOnly、租户管理员接口套 adminOnly / tenantAdminOnly、
// 应用接口套 appOnly —— 权限判定统一放在中间件里做，各 handler 只管业务（题面 R7）。
func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.HandleFunc("/readyz", s.handleHealth)

	// 认证
	s.mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/v1/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/v1/auth/whoami", s.handleWhoami)

	// 运营（R1/R5/R6/R7）
	s.mux.HandleFunc("/api/v1/ops/tenants", s.opsOnly(s.handleListTenants))
	s.mux.HandleFunc("/api/v1/ops/tenants/open", s.opsOnly(s.handleOpenTenant))
	s.mux.HandleFunc("/api/v1/ops/tenants/quota", s.opsOnly(s.handleAdjustQuota))
	s.mux.HandleFunc("/api/v1/ops/tenants/disable", s.opsOnly(s.handleDisableTenant))
	s.mux.HandleFunc("/api/v1/ops/report/daily", s.opsOnly(s.handleDailyReport))
	s.mux.HandleFunc("/api/v1/ops/diagnostics", s.opsOnly(s.handleDiagnostics))
	s.mux.HandleFunc("/api/v1/ops/clock", s.opsOnly(s.handleClock))

	// 租户管理员（R1/R6/R7）
	s.mux.HandleFunc("/api/v1/admin/credentials", s.adminOnly(s.handleListCredentials))
	s.mux.HandleFunc("/api/v1/admin/credentials/create", s.tenantAdminOnly(s.handleCreateCredential))
	s.mux.HandleFunc("/api/v1/admin/credentials/disable", s.tenantAdminOnly(s.handleDisableCredential))
	s.mux.HandleFunc("/api/v1/admin/usage", s.adminOnly(s.handleTenantUsage))
	s.mux.HandleFunc("/api/v1/admin/requests", s.adminOnly(s.handleTenantRequests))

	// 客户应用（R3/R4/R7）
	s.mux.HandleFunc("/api/v1/app/invoke", s.appOnly(s.handleInvoke))
	s.mux.HandleFunc("/api/v1/app/requests", s.appOnly(s.handleCredentialRequests))
	s.mux.HandleFunc("/api/v1/app/request", s.appOnly(s.handleGetRequest))

	// 页面
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/login", s.handleLoginPage)
	s.mux.HandleFunc("/ops", s.handleOpsPage)
	s.mux.HandleFunc("/admin", s.handleAdminPage)
	s.mux.HandleFunc("/app", s.handleAppPage)
}

// ---------- 通用辅助 ----------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("写 JSON 响应失败: %v", err)
	}
}

// writeError 输出错误响应。
//
// 500 类内部错误只回通用提示，具体原因写入服务端日志，
// 避免把数据库细节、路径等内部信息暴露给调用方。
func writeError(w http.ResponseWriter, status int, err error) {
	if status >= http.StatusInternalServerError {
		log.Printf("服务端内部错误: %v", err)
		writeJSON(w, status, map[string]any{"error": "服务内部错误，请稍后重试"})
		return
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

// statusFor 把领域错误映射为 HTTP 状态码。
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrQuotaExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, domain.ErrRequestNoConflict):
		return http.StatusConflict
	case errors.Is(err, domain.ErrRequestNoRequired),
		errors.Is(err, domain.ErrRequestNoFormat),
		errors.Is(err, domain.ErrQuotaRange),
		errors.Is(err, domain.ErrQuotaFloor),
		errors.Is(err, domain.ErrReasonRequired),
		errors.Is(err, domain.ErrCredentialLimit),
		errors.Is(err, domain.ErrTenantMismatch),
		errors.Is(err, domain.ErrTenantNameRequired),
		errors.Is(err, domain.ErrTenantScopeRequired):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrTenantDisabled),
		errors.Is(err, domain.ErrCredentialDisabled):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSON 严格解析 JSON 请求体。
//
// 两个要点：请求体上限 1MB；拒绝未知字段，避免拼错的字段名被静默忽略。
func decodeJSON(r *http.Request, target any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("请求体解析失败: %w", err)
	}
	return nil
}

// identityFrom 从请求中解析身份。
//
// 只接受 Cookie 或 Authorization: Bearer 两种来源；不接受请求参数里的用户/租户编号，
// 以保证「角色身份可信、不能通过修改请求中的用户编号越权操作」。
func (s *Server) identityFrom(r *http.Request) (service.Identity, error) {
	token := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		token = cookie.Value
	}
	if token == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	return s.svc.Authenticate(r.Context(), token)
}

// withIdentity 包装处理器：解析身份并注入上下文。
func (s *Server) withIdentity(next func(http.ResponseWriter, *http.Request, service.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.identityFrom(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}
		next(w, r, identity)
	}
}

// opsOnly 限制平台运营访问。
func (s *Server) opsOnly(next func(http.ResponseWriter, *http.Request, service.Identity)) http.HandlerFunc {
	return s.withIdentity(func(w http.ResponseWriter, r *http.Request, identity service.Identity) {
		if err := service.AuthorizeOps(identity); err != nil {
			writeError(w, http.StatusForbidden, err)
			return
		}
		next(w, r, identity)
	})
}

// adminOnly 限制租户管理员访问（运营亦可，用于跨租户核查）。
func (s *Server) adminOnly(next func(http.ResponseWriter, *http.Request, service.Identity)) http.HandlerFunc {
	return s.withIdentity(func(w http.ResponseWriter, r *http.Request, identity service.Identity) {
		if !identity.IsTenantAdmin() && !identity.IsOps() {
			writeError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}
		next(w, r, identity)
	})
}

// appOnly 限制客户应用凭证访问。
func (s *Server) appOnly(next func(http.ResponseWriter, *http.Request, service.Identity)) http.HandlerFunc {
	return s.withIdentity(func(w http.ResponseWriter, r *http.Request, identity service.Identity) {
		if !identity.IsCredential() {
			writeError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}
		next(w, r, identity)
	})
}

// tenantAdminOnly 限制客户管理员访问。
//
// 凭证的创建与停用属于本租户内部操作（R1/R6）：凭证所属租户由服务端从
// 调用者身份推导，运营账号没有租户归属，因此不能代客户创建凭证。
func (s *Server) tenantAdminOnly(next func(http.ResponseWriter, *http.Request, service.Identity)) http.HandlerFunc {
	return s.withIdentity(func(w http.ResponseWriter, r *http.Request, identity service.Identity) {
		if !identity.IsTenantAdmin() || identity.TenantID == "" {
			writeError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}
		next(w, r, identity)
	})
}

// tenantScope 解析请求中的租户范围。
//
// 运营可跨租户（必须显式给出 tenant_id）；租户管理员与应用凭证一律使用自身租户，
// 即使请求参数里带了其他租户编号也不会被采信（R1/R7）。
func (s *Server) tenantScope(r *http.Request, identity service.Identity) (string, error) {
	requested := r.URL.Query().Get("tenant_id")
	if identity.IsOps() {
		if requested == "" {
			return "", domain.ErrTenantScopeRequired
		}
		return requested, nil
	}
	if requested != "" && requested != identity.TenantID {
		return "", domain.ErrTenantMismatch
	}
	if identity.TenantID == "" {
		return "", domain.ErrForbidden
	}
	return identity.TenantID, nil
}

// dayParam 解析日期参数，缺省为 UTC 今日。
func dayParam(r *http.Request, clk service.Clock) string {
	day := r.URL.Query().Get("day")
	if day == "" {
		return domain.DayOf(clk.Now())
	}
	return day
}

// ---------- 健康检查 ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   s.svc.Clock().Now().Format(time.RFC3339),
	})
}

// ---------- 认证 ----------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Secret 用于客户应用以凭证密钥登录。
	Secret string `json:"secret"`
}

// handleLogin 处理登录请求，返回会话令牌并写入 Cookie。
//
// 两种登录方式：管理员用账号口令，客户应用用凭证密钥。
// Cookie 设置 HttpOnly 与 SameSite=Lax，令牌不出现在页面脚本可读的位置。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in loginRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var token string
	var identity service.Identity
	var err error
	if in.Secret != "" {
		token, identity, err = s.svc.LoginCredential(r.Context(), in.Secret)
	} else {
		token, identity, err = s.svc.LoginAdmin(r.Context(), in.Username, in.Password)
	}
	if err != nil {
		s.respond(w, r, http.StatusUnauthorized, map[string]any{"error": err.Error()}, "/login", err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  s.svc.Clock().Now().Add(s.svc.SessionTTL),
	})
	s.respond(w, r, http.StatusOK, map[string]any{
		"token": token,
		"identity": map[string]any{
			"subject_type": identity.SubjectType,
			"subject_id":   identity.SubjectID,
			"tenant_id":    identity.TenantID,
			"role":         identity.Role,
			"username":     identity.Username,
		},
	}, "/", "登录成功")
}

// handleLogout 注销当前会话并清除 Cookie。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token, err := s.identityToken(r); err == nil {
		_ = s.svc.Logout(r.Context(), token)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	s.respond(w, r, http.StatusOK, map[string]any{"status": "logged_out"}, "/login", "已登出")
}

// identityToken 从 Cookie 或 Authorization 头取出会话令牌。
//
// 与 identityFrom 的区别：这里只取令牌本身（用于登出），不做身份还原。
func (s *Server) identityToken(r *http.Request) (string, error) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		return cookie.Value, nil
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer "), nil
	}
	return "", domain.ErrUnauthorized
}

// handleWhoami 返回当前登录身份，用于前端或脚本自查身份与租户归属。
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	s.withIdentity(func(w http.ResponseWriter, r *http.Request, identity service.Identity) {
		writeJSON(w, http.StatusOK, map[string]any{
			"subject_type": identity.SubjectType,
			"subject_id":   identity.SubjectID,
			"tenant_id":    identity.TenantID,
			"role":         identity.Role,
			"username":     identity.Username,
		})
	})(w, r)
}

// ---------- 运营接口 ----------

func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request, _ service.Identity) {
	tenants, err := s.svc.DB().ListTenants(r.Context())
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

type openTenantRequest struct {
	TenantID   string `json:"tenant_id"`
	Name       string `json:"name"`
	DailyQuota int    `json:"daily_quota"`
	Reason     string `json:"reason"`
}

// handleOpenTenant 为租户开通服务并设置初始日额度（R1）。
//
// 操作人与开通原因会留痕（配额变更记录），便于事后核查。
func (s *Server) handleOpenTenant(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in openTenantRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tenant, err := s.svc.OpenTenant(r.Context(), identity.Username, service.TenantInput{
		TenantID:   in.TenantID,
		Name:       in.Name,
		DailyQuota: in.DailyQuota,
		Reason:     in.Reason,
	})
	if err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/ops", err.Error())
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{"tenant": tenant}, "/ops",
		"租户 "+tenant.Name+" 已开通，日额度 "+strconv.Itoa(tenant.DailyQuota))
}

type quotaRequest struct {
	TenantID   string `json:"tenant_id"`
	DailyQuota int    `json:"daily_quota"`
	Reason     string `json:"reason"`
}

// handleAdjustQuota 调整租户的日额度（R5）。
//
// 降额下限由 service 层按「当日成功数 + 处理中数」判定，
// 且判定与写入在同一条 SQL 里完成，避免并发降额把占用数顶到额度之上。
func (s *Server) handleAdjustQuota(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in quotaRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	change, err := s.svc.AdjustQuota(r.Context(), identity.Username, in.TenantID, in.DailyQuota, in.Reason)
	if err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/ops", err.Error())
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{"change": change}, "/ops",
		"租户 "+in.TenantID+" 的日额度已由 "+strconv.Itoa(change.OldQuota)+" 调整为 "+strconv.Itoa(change.NewQuota))
}

type disableTenantRequest struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason"`
}

// handleDisableTenant 停用租户（R6）。
//
// 停用后不再受理新调用；在途请求仍按原规则完成结算。
func (s *Server) handleDisableTenant(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in disableTenantRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.DisableTenant(r.Context(), identity.Username, in.TenantID, in.Reason); err != nil {
		s.respond(w, r, statusFor(err), map[string]any{"error": err.Error()}, "/ops", err.Error())
		return
	}
	s.respond(w, r, http.StatusOK, map[string]any{"status": "disabled", "tenant_id": in.TenantID},
		"/ops", "租户 "+in.TenantID+" 已停用，不再受理新调用")
}

// handleDailyReport 输出日终核查报表（R8）。
//
// 按租户与 UTC 自然日分列成功、失败、超时、处理中、被拒绝五类计数，
// 并核对「成功记录数 == 计入用量次数」以及占用是否超过额度。
func (s *Server) handleDailyReport(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	tenantID, err := s.tenantScope(r, identity)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	day := dayParam(r, s.svc.Clock())
	report, err := s.svc.DailyReport(r.Context(), tenantID, day)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": report})
}

// handleDiagnostics 输出迟到结果等诊断记录（R4）。
//
// 诊断只用于事后核查：迟到结果不改终态、不追加扣额、不计入成功量。
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request, identity service.Identity) {
	tenantID, err := s.tenantScope(r, identity)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	diags, err := s.svc.Diagnostics(r.Context(), tenantID)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diagnostics": diags})
}

type clockRequest struct {
	OffsetSeconds int `json:"offset_seconds"`
}

// handleClock 调整教学时钟的偏移秒数。
//
// 教学时钟只偏移「当前时间」，配额与日期归属规则不变（题面共同要求允许用可配置教学时钟
// 演示时间边界，但必须注明）。时钟前移后旧会话令牌随即失效。
func (s *Server) handleClock(w http.ResponseWriter, r *http.Request, _ service.Identity) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 POST"))
		return
	}
	var in clockRequest
	if err := decodeInput(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	offset := time.Duration(in.OffsetSeconds) * time.Second
	// 教学时钟只偏移「当前时间」，配额与归属规则不变。
	if err := s.svc.DB().SetSetting(r.Context(), "clock_offset_seconds", strconv.Itoa(in.OffsetSeconds)); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	s.svc.SetClockOffset(offset)
	s.respond(w, r, http.StatusOK, map[string]any{
		"offset_seconds": in.OffsetSeconds,
		"now":            s.svc.Clock().Now().Format(time.RFC3339),
	}, "/ops", "教学时钟已调整 "+strconv.Itoa(in.OffsetSeconds)+" 秒")
}
