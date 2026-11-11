package httpapi

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"modelquota/internal/domain"
	"modelquota/internal/service"
)

//go:embed web/templates/*.html
var templateFS embed.FS

// templates 持有页面模板集合。
type templates struct {
	login    *template.Template
	ops      *template.Template
	admin    *template.Template
	app      *template.Template
	notFound *template.Template
}

// LoadTemplates 加载页面模板。
// 每个页面模板与 layout.html 一起解析，页面文件通过 define "content" 注入内容区。
func LoadTemplates() (*templates, error) {
	funcs := template.FuncMap{
		"statusLabel": func(s domain.RequestStatus) string {
			switch s {
			case domain.RequestSuccess:
				return "成功"
			case domain.RequestFailed:
				return "失败"
			case domain.RequestTimeout:
				return "超时"
			case domain.RequestProcessing:
				return "处理中"
			case domain.RequestRejected:
				return "被拒绝"
			default:
				return string(s)
			}
		},
		"tenantStatusLabel": func(s domain.TenantStatus) string {
			switch s {
			case domain.TenantActive:
				return "已开通"
			case domain.TenantPending:
				return "待开通"
			case domain.TenantDisabled:
				return "已停用"
			default:
				return string(s)
			}
		},
		"credentialStatusLabel": func(s domain.CredentialStatus) string {
			switch s {
			case domain.CredentialActive:
				return "可用"
			case domain.CredentialDisabled:
				return "已停用"
			default:
				return string(s)
			}
		},
		"statusClass": func(s domain.RequestStatus) string {
			switch s {
			case domain.RequestSuccess:
				return "ok"
			case domain.RequestFailed, domain.RequestTimeout, domain.RequestRejected:
				return "bad"
			default:
				return "mut"
			}
		},
	}

	parse := func(page string) (*template.Template, error) {
		return template.New("layout.html").Funcs(funcs).ParseFS(templateFS,
			"web/templates/layout.html", "web/templates/"+page)
	}

	login, err := parse("login.html")
	if err != nil {
		return nil, fmt.Errorf("加载登录模板失败: %w", err)
	}
	ops, err := parse("ops.html")
	if err != nil {
		return nil, fmt.Errorf("加载运营模板失败: %w", err)
	}
	admin, err := parse("admin.html")
	if err != nil {
		return nil, fmt.Errorf("加载管理员模板失败: %w", err)
	}
	app, err := parse("app.html")
	if err != nil {
		return nil, fmt.Errorf("加载应用模板失败: %w", err)
	}
	notFound, err := parse("notfound.html")
	if err != nil {
		return nil, fmt.Errorf("加载未找到模板失败: %w", err)
	}
	return &templates{login: login, ops: ops, admin: admin, app: app, notFound: notFound}, nil
}

// pageData 是页面统一数据。
type pageData struct {
	Title    string
	Identity service.Identity
	Today    string
	Now      string
	Error    string
	Message  string
	Data     any
}

// render 渲染页面（先执行布局模板再执行内容模板）。
func (s *Server) render(w http.ResponseWriter, status int, tmpl *template.Template, data pageData) {
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		http.Error(w, "页面渲染失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

// requireIdentity 解析身份，失败时跳转到登录页。
func (s *Server) requireIdentity(w http.ResponseWriter, r *http.Request) (service.Identity, bool) {
	identity, err := s.identityFrom(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return service.Identity{}, false
	}
	return identity, true
}

func (s *Server) baseData(r *http.Request, identity service.Identity, title string) pageData {
	return pageData{
		Title:    title,
		Identity: identity,
		Today:    s.svc.CurrentDay(),
		Now:      s.svc.Clock().Now().Format("2006-01-02 15:04:05 UTC"),
		Error:    r.URL.Query().Get("error"),
		Message:  r.URL.Query().Get("message"),
	}
}

// handleIndex 按身份跳转到对应工作台；未知路径返回 404 页面。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		data := s.baseData(r, service.Identity{}, "页面不存在")
		s.render(w, http.StatusNotFound, s.templates.notFound, data)
		return
	}
	identity, err := s.identityFrom(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	switch {
	case identity.IsOps():
		http.Redirect(w, r, "/ops", http.StatusSeeOther)
	case identity.IsTenantAdmin():
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/app", http.StatusSeeOther)
	}
}

// handleLoginPage 渲染登录页。
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Title: "登录", Now: s.svc.Clock().Now().Format("2006-01-02 15:04:05 UTC")}
	s.render(w, http.StatusOK, s.templates.login, data)
}

// opsView 是运营工作台的数据。
type opsView struct {
	Tenants     []domain.Tenant
	Reports     []opsReportRow
	Diagnostics []opsDiagnosticRow
	Day         string
}

// opsReportRow 是某租户当日的核查行。
type opsReportRow struct {
	TenantID string
	Report   *domain.DailyReport
}

// opsDiagnosticRow 是某租户的一条诊断记录（含迟到结果）。
type opsDiagnosticRow struct {
	TenantID   string
	Diagnostic domain.Diagnostic
}

func (s *Server) handleOpsPage(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	if !identity.IsOps() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	day := dayParam(r, s.svc.Clock())

	tenants, err := s.svc.DB().ListTenants(r.Context())
	if err != nil {
		s.renderOpsError(w, r, identity, err)
		return
	}
	view := opsView{Tenants: tenants, Day: day}
	for _, t := range tenants {
		report, err := s.svc.DailyReport(r.Context(), t.ID, day)
		if err != nil {
			continue
		}
		view.Reports = append(view.Reports, opsReportRow{TenantID: t.ID, Report: report})
		// 迟到结果等诊断记录（R4）：运营跨租户核查时一并展示。
		diags, err := s.svc.Diagnostics(r.Context(), t.ID)
		if err != nil {
			continue
		}
		for _, d := range diags {
			view.Diagnostics = append(view.Diagnostics, opsDiagnosticRow{TenantID: t.ID, Diagnostic: d})
		}
	}

	data := s.baseData(r, identity, "平台运营工作台")
	data.Data = view
	s.render(w, http.StatusOK, s.templates.ops, data)
}

func (s *Server) renderOpsError(w http.ResponseWriter, r *http.Request, identity service.Identity, err error) {
	data := s.baseData(r, identity, "平台运营工作台")
	data.Error = err.Error()
	s.render(w, http.StatusInternalServerError, s.templates.ops, data)
}

// adminView 是租户管理员工作台的数据。
type adminView struct {
	TenantID    string
	Tenant      *domain.Tenant
	Credentials []domain.Credential
	Usage       domain.Usage
	Requests    []domain.ModelRequest
	Rejected    []domain.RejectedAttempt
	Day         string
	// Secret 是刚创建的凭证密钥（只在创建后的一次渲染中出现）。
	Secret string
	// SecretCredential 是该密钥对应的凭证名称。
	SecretCredential string
}

// adminViewFor 装配租户管理员工作台的数据。
func (s *Server) adminViewFor(r *http.Request, identity service.Identity, day string) (adminView, error) {
	view := adminView{TenantID: identity.TenantID, Day: day}

	tenant, err := s.svc.DB().GetTenant(r.Context(), identity.TenantID)
	if err != nil {
		return view, err
	}
	view.Tenant = tenant

	creds, err := s.svc.DB().ListCredentialsByTenant(r.Context(), identity.TenantID)
	if err != nil {
		return view, err
	}
	view.Credentials = creds

	usage, err := s.svc.TenantUsage(r.Context(), identity.TenantID, day)
	if err != nil {
		return view, err
	}
	view.Usage = usage

	reqs, err := s.svc.RequestsByTenant(r.Context(), identity.TenantID, day)
	if err != nil {
		return view, err
	}
	view.Requests = reqs

	rejected, err := s.svc.RejectedAttempts(r.Context(), identity.TenantID, day)
	if err != nil {
		return view, err
	}
	view.Rejected = rejected

	return view, nil
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	if !identity.IsTenantAdmin() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	day := dayParam(r, s.svc.Clock())

	view, err := s.adminViewFor(r, identity, day)
	if err != nil {
		s.renderAdminError(w, r, identity, err)
		return
	}

	data := s.baseData(r, identity, "客户管理员工作台")
	data.Data = view
	s.render(w, http.StatusOK, s.templates.admin, data)
}

// renderAdminWithSecret 渲染管理台并显示刚创建的一次性密钥。
//
// 密钥只在创建时返回一次，因此表单提交后不能像其他操作那样重定向（重定向会丢失密钥），
// 而是直接渲染一次含密钥的页面。
func (s *Server) renderAdminWithSecret(w http.ResponseWriter, r *http.Request, identity service.Identity, cred *domain.Credential, secret string) {
	day := dayParam(r, s.svc.Clock())
	view, err := s.adminViewFor(r, identity, day)
	if err != nil {
		s.renderAdminError(w, r, identity, err)
		return
	}
	view.Secret = secret
	view.SecretCredential = cred.Name

	data := s.baseData(r, identity, "客户管理员工作台")
	data.Data = view
	data.Message = "凭证 " + cred.Name + " 已创建，密钥仅显示一次，请立即保存并交给应用"
	s.render(w, http.StatusOK, s.templates.admin, data)
}

func (s *Server) renderAdminError(w http.ResponseWriter, r *http.Request, identity service.Identity, err error) {
	data := s.baseData(r, identity, "客户管理员工作台")
	data.Error = err.Error()
	s.render(w, http.StatusInternalServerError, s.templates.admin, data)
}

// appView 是客户应用工作台的数据。
type appView struct {
	CredentialID string
	Day          string
	Requests     []domain.ModelRequest
}

func (s *Server) handleAppPage(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	if !identity.IsCredential() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	day := dayParam(r, s.svc.Clock())
	reqs, err := s.svc.RequestsByCredential(r.Context(), identity.SubjectID, day)
	if err != nil {
		data := s.baseData(r, identity, "客户应用工作台")
		data.Error = err.Error()
		s.render(w, http.StatusInternalServerError, s.templates.app, data)
		return
	}

	data := s.baseData(r, identity, "客户应用工作台")
	data.Data = appView{CredentialID: identity.SubjectID, Day: day, Requests: reqs}
	s.render(w, http.StatusOK, s.templates.app, data)
}
