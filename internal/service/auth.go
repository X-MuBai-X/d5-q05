package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"modelquota/internal/domain"
)

// Identity 是服务端从会话令牌推导出的调用者身份。
//
// 关键设计（题面 R1/R7 + 共同要求「角色身份必须可信，不能通过随意修改请求中的用户编号越权操作」）：
// 身份只来自登录时签发的令牌，租户归属同样来自服务端，
// 请求参数中的任何租户/用户编号都不会被采信。
type Identity struct {
	// SubjectType 区分管理员与凭证。
	SubjectType domain.SubjectType
	// SubjectID 是管理员 ID 或凭证 ID。
	SubjectID string
	// TenantID 是该身份所属租户（运营为空）。
	TenantID string
	// Role 是运营或租户管理员。
	Role domain.AdminRole
	// Username 仅用于页面展示。
	Username string
	// DisplayName 仅用于页面展示。
	DisplayName string
}

// IsOps 判断是否为平台运营。
func (i Identity) IsOps() bool {
	return i.SubjectType == domain.SubjectAdmin && i.Role == domain.AdminOps
}

// IsTenantAdmin 判断是否为客户管理员。
func (i Identity) IsTenantAdmin() bool {
	return i.SubjectType == domain.SubjectAdmin && i.Role == domain.AdminTenant
}

// IsCredential 判断是否为客户应用凭证。
func (i Identity) IsCredential() bool {
	return i.SubjectType == domain.SubjectCredential
}

// LoginAdmin 用预置账号口令登录并签发会话令牌。
func (s *Service) LoginAdmin(ctx context.Context, username, password string) (string, Identity, error) {
	admin, err := s.db.GetAdminByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return "", Identity{}, domain.ErrUnauthorized
	}
	if domain.HashSecret(password) != admin.PasswordHash {
		return "", Identity{}, domain.ErrUnauthorized
	}
	identity := Identity{
		SubjectType: domain.SubjectAdmin,
		SubjectID:   admin.ID,
		TenantID:    admin.TenantID,
		Role:        admin.Role,
		Username:    admin.Username,
	}
	token, err := s.issueSession(ctx, identity)
	if err != nil {
		return "", Identity{}, err
	}
	return token, identity, nil
}

// LoginCredential 用凭证密钥登录并签发会话令牌。
func (s *Service) LoginCredential(ctx context.Context, secret string) (string, Identity, error) {
	cred, err := s.db.GetCredentialByKeyHash(ctx, domain.HashSecret(secret))
	if err != nil {
		return "", Identity{}, domain.ErrUnauthorized
	}
	identity := Identity{
		SubjectType: domain.SubjectCredential,
		SubjectID:   cred.ID,
		TenantID:    cred.TenantID,
		Username:    cred.Name,
		DisplayName: cred.Name,
	}
	token, err := s.issueSession(ctx, identity)
	if err != nil {
		return "", Identity{}, err
	}
	return token, identity, nil
}

func (s *Service) issueSession(ctx context.Context, identity Identity) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成会话令牌失败: %w", err)
	}
	token := hex.EncodeToString(buf)
	now := s.clk.Now()
	role := string(identity.Role)
	if identity.SubjectType == domain.SubjectCredential {
		role = ""
	}
	if err := s.db.CreateSession(ctx, token, string(identity.SubjectType), identity.SubjectID,
		identity.TenantID, role, now, now.Add(s.SessionTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate 按令牌解析身份。
func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if strings.TrimSpace(token) == "" {
		return Identity{}, domain.ErrUnauthorized
	}
	session, err := s.db.GetSession(ctx, token)
	if err != nil {
		return Identity{}, domain.ErrUnauthorized
	}
	if s.clk.Now().After(session.ExpiresAt) {
		return Identity{}, domain.ErrUnauthorized
	}

	identity := Identity{
		SubjectType: session.SubjectType,
		SubjectID:   session.SubjectID,
		TenantID:    session.TenantID,
		Role:        session.Role,
	}
	switch session.SubjectType {
	case domain.SubjectAdmin:
		admin, err := s.db.GetAdminByID(ctx, session.SubjectID)
		if err != nil {
			return Identity{}, domain.ErrUnauthorized
		}
		identity.Username = admin.Username
		identity.DisplayName = admin.Username
	case domain.SubjectCredential:
		cred, err := s.db.GetCredential(ctx, session.SubjectID)
		if err != nil {
			return Identity{}, domain.ErrUnauthorized
		}
		identity.Username = cred.Name
		identity.DisplayName = cred.Name
	}
	return identity, nil
}

// Logout 删除会话令牌。
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.db.DeleteSession(ctx, token)
}

// AuthorizeTenantAccess 校验身份是否可访问指定租户（R7）。
// 运营可跨租户核查；租户管理员与应用凭证仅限本租户。
func AuthorizeTenantAccess(identity Identity, tenantID string) error {
	if identity.IsOps() {
		return nil
	}
	if identity.TenantID == "" || identity.TenantID != tenantID {
		return domain.ErrForbidden
	}
	return nil
}

// AuthorizeOps 校验身份是平台运营。
func AuthorizeOps(identity Identity) error {
	if !identity.IsOps() {
		return domain.ErrForbidden
	}
	return nil
}
