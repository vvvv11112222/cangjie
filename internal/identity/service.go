package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/optional"
	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName = "teaching_session"
	PreAuthCookieName = "teaching_csrf"
	dummyPasswordHash = "$2a$10$MFe2u296BdtnOyr.fRgKKeS0rN4zZft/Ma0tXjAQ8cE81xIcRaxrC"
)

type RoleBinding struct {
	ID         string  `json:"id"`
	RoleCode   string  `json:"role_code"`
	ScopeOrgID *string `json:"scope_org_id"`
}

type CreateRoleBinding struct {
	RoleCode   string                 `json:"role_code"`
	ScopeOrgID optional.Value[string] `json:"scope_org_id"`
}

type Principal struct {
	UserID      string
	DisplayName string
	Roles       []RoleBinding
	SessionID   string
	Token       string
}

type User struct {
	ID          string  `json:"id"`
	OrgUnitID   *string `json:"org_unit_id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	Status      string  `json:"status"`
}

type Service struct {
	pool       *pgxpool.Pool
	sessionTTL time.Duration
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, sessionTTL time.Duration) *Service {
	return &Service{pool: pool, sessionTTL: sessionTTL, now: time.Now}
}

func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func CSRFToken(sessionToken string) string { return tokenHash("csrf:" + sessionToken) }

func SecureEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if rawToken == "" {
		return Principal{}, apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
	}
	var p Principal
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text,u.display_name,a.id::text
		FROM teaching.auth_sessions a
		JOIN teaching.user_accounts u ON u.id=a.user_id
		WHERE a.token_hash=$1 AND a.revoked_at IS NULL AND a.expires_at>$2 AND u.status='active'`,
		tokenHash(rawToken), s.now()).Scan(&p.UserID, &p.DisplayName, &p.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "session is invalid or expired")
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate session: %w", err)
	}
	p.Token = rawToken
	p.Roles, err = s.roles(ctx, s.pool, p.UserID)
	if err != nil {
		return Principal{}, err
	}
	return p, nil
}

func (s *Service) Login(ctx context.Context, username, password, previousToken string) (Principal, string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || len(password) > 72 {
		return Principal{}, "", apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "username and password are required")
	}
	var userID, displayName, passwordHash, status string
	err := s.pool.QueryRow(ctx, `SELECT id::text,display_name,password_hash,status FROM teaching.user_accounts WHERE username=$1`, username).
		Scan(&userID, &displayName, &passwordHash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
		return Principal{}, "", apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "invalid username or password")
	}
	if err != nil {
		return Principal{}, "", fmt.Errorf("load login account: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return Principal{}, "", apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "invalid username or password")
	}
	if status != "active" {
		return Principal{}, "", apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "account is disabled")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Principal{}, "", fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := tx.QueryRow(ctx, `SELECT display_name,status FROM teaching.user_accounts WHERE id=$1 FOR UPDATE`, userID).Scan(&displayName, &status); err != nil {
		return Principal{}, "", fmt.Errorf("lock login account: %w", err)
	}
	if status != "active" {
		return Principal{}, "", apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "account is disabled")
	}
	if previousToken != "" {
		if _, err := tx.Exec(ctx, `UPDATE teaching.auth_sessions SET revoked_at=COALESCE(revoked_at,$1) WHERE token_hash=$2`, s.now(), tokenHash(previousToken)); err != nil {
			return Principal{}, "", fmt.Errorf("revoke previous session: %w", err)
		}
	}
	rawToken, err := RandomToken()
	if err != nil {
		return Principal{}, "", err
	}
	var sessionID string
	err = tx.QueryRow(ctx, `INSERT INTO teaching.auth_sessions(user_id,token_hash,expires_at) VALUES($1,$2,$3) RETURNING id::text`,
		userID, tokenHash(rawToken), s.now().Add(s.sessionTTL)).Scan(&sessionID)
	if err != nil {
		return Principal{}, "", fmt.Errorf("create session: %w", err)
	}
	roles, err := s.roles(ctx, tx, userID)
	if err != nil {
		return Principal{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, "", fmt.Errorf("commit login: %w", err)
	}
	return Principal{UserID: userID, DisplayName: displayName, Roles: roles, SessionID: sessionID, Token: rawToken}, rawToken, nil
}

func (s *Service) Logout(ctx context.Context, p Principal) error {
	command, err := s.pool.Exec(ctx, `UPDATE teaching.auth_sessions SET revoked_at=$1 WHERE id=$2 AND revoked_at IS NULL`, s.now(), p.SessionID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return apperror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "session is invalid")
	}
	return nil
}

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Service) roles(ctx context.Context, q rowQuerier, userID string) ([]RoleBinding, error) {
	rows, err := q.Query(ctx, `SELECT id::text,role_code,scope_org_id::text FROM teaching.role_bindings WHERE user_id=$1 ORDER BY role_code,scope_org_id NULLS FIRST`, userID)
	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()
	roles := []RoleBinding{}
	for rows.Next() {
		var role RoleBinding
		if err := rows.Scan(&role.ID, &role.RoleCode, &role.ScopeOrgID); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (p Principal) Has(role string) bool {
	for _, binding := range p.Roles {
		if binding.RoleCode == role {
			return true
		}
	}
	return false
}

func (p Principal) Scoped(role, orgID string) bool {
	for _, binding := range p.Roles {
		if binding.RoleCode == role && binding.ScopeOrgID != nil && *binding.ScopeOrgID == orgID {
			return true
		}
	}
	return false
}

func (p Principal) CollegeScopes() []string {
	set := map[string]bool{}
	for _, r := range p.Roles {
		if (r.RoleCode == "academic_admin" || r.RoleCode == "supervisor") && r.ScopeOrgID != nil {
			set[*r.ScopeOrgID] = true
		}
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func (p Principal) AllowedActions() []string {
	set := map[string]bool{}
	for _, r := range p.Roles {
		switch r.RoleCode {
		case "sys_admin":
			for _, a := range []string{"manage_academic", "manage_users", "upload", "analyze", "review", "publish", "operate", "delete"} {
				set[a] = true
			}
		case "academic_admin":
			set["manage_academic"], set["manage_users"] = true, true
		case "supervisor":
			set["review"], set["publish"] = true, true
		case "teacher":
			set["upload"], set["analyze"] = true, true
		}
	}
	order := []string{"manage_academic", "manage_users", "upload", "analyze", "review", "publish", "operate", "delete"}
	result := []string{}
	for _, a := range order {
		if set[a] {
			result = append(result, a)
		}
	}
	return result
}

type CreateUser struct {
	OrgUnitID       *string `json:"org_unit_id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Status          string  `json:"status"`
	InitialPassword string  `json:"initial_password"`
}
type PatchUser struct {
	OrgUnitID   optional.Value[string] `json:"org_unit_id"`
	Username    *string                `json:"username"`
	DisplayName *string                `json:"display_name"`
	Status      *string                `json:"status"`
}

func (s *Service) ListUsers(ctx context.Context, actor Principal, after string, limit int) ([]User, string, error) {
	args := []any{after, limit + 1}
	predicate := "($1='' OR u.id>$1::uuid)"
	if !actor.Has("sys_admin") {
		scopes := academicScopes(actor)
		if len(scopes) == 0 {
			return nil, "", apperror.New(http.StatusForbidden, "FORBIDDEN", "user management is not allowed")
		}
		args = append(args, scopes)
		predicate += " AND teaching.college_of(u.org_unit_id)=ANY($3::uuid[]) AND NOT EXISTS (SELECT 1 FROM teaching.role_bindings rb WHERE rb.user_id=u.id AND (rb.role_code IN ('sys_admin','academic_admin') OR (rb.scope_org_id IS NOT NULL AND NOT rb.scope_org_id=ANY($3::uuid[]))))"
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id::text,u.org_unit_id::text,u.username,u.display_name,u.status FROM teaching.user_accounts u WHERE `+predicate+` ORDER BY u.id LIMIT $2`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var v User
		if err := rows.Scan(&v.ID, &v.OrgUnitID, &v.Username, &v.DisplayName, &v.Status); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return trimUsers(items, limit)
}

func trimUsers(items []User, limit int) ([]User, string, error) {
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (s *Service) GetUser(ctx context.Context, actor Principal, id string) (User, error) {
	var v User
	err := s.pool.QueryRow(ctx, `SELECT id::text,org_unit_id::text,username,display_name,status FROM teaching.user_accounts WHERE id=$1`, id).Scan(&v.ID, &v.OrgUnitID, &v.Username, &v.DisplayName, &v.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, apperror.New(http.StatusNotFound, "NOT_FOUND", "user not found")
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	if !actor.Has("sys_admin") {
		if err := s.requireManageableUser(ctx, actor, id, v.OrgUnitID); err != nil {
			return User{}, err
		}
	}
	return v, nil
}

func (s *Service) CreateUser(ctx context.Context, actor Principal, input CreateUser) (User, error) {
	if strings.TrimSpace(input.Username) == "" || strings.TrimSpace(input.DisplayName) == "" || len(input.InitialPassword) < 12 || len(input.InitialPassword) > 72 || (input.Status != "active" && input.Status != "disabled") {
		return User{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "invalid user fields or password shorter than 12 characters")
	}
	if input.OrgUnitID == nil {
		return User{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "org_unit_id is required")
	}
	if !actor.Has("sys_admin") {
		allowed, err := s.inAcademicScope(ctx, actor, *input.OrgUnitID)
		if err != nil {
			return User{}, err
		}
		if !allowed {
			return User{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.InitialPassword), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var v User
	err = s.pool.QueryRow(ctx, `INSERT INTO teaching.user_accounts(org_unit_id,username,password_hash,display_name,status) VALUES($1,$2,$3,$4,$5) RETURNING id::text,org_unit_id::text,username,display_name,status`, input.OrgUnitID, strings.TrimSpace(input.Username), string(hash), strings.TrimSpace(input.DisplayName), input.Status).Scan(&v.ID, &v.OrgUnitID, &v.Username, &v.DisplayName, &v.Status)
	if err != nil {
		return User{}, dbInputError(err, "create user")
	}
	return v, nil
}

func (s *Service) PatchUser(ctx context.Context, actor Principal, id string, input PatchUser) (User, error) {
	current, err := s.GetUser(ctx, actor, id)
	if err != nil {
		return User{}, err
	}
	if id == actor.UserID && !actor.Has("sys_admin") {
		return User{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "academic administrators cannot modify their own account")
	}
	org := current.OrgUnitID
	if input.OrgUnitID.Set {
		org = input.OrgUnitID.Value
	}
	if org == nil {
		return User{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "org_unit_id is required")
	}
	if !actor.Has("sys_admin") {
		allowed, scopeErr := s.inAcademicScope(ctx, actor, *org)
		if scopeErr != nil {
			return User{}, scopeErr
		}
		if !allowed {
			return User{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "organization is outside your scope")
		}
	}
	username := current.Username
	if input.Username != nil {
		username = strings.TrimSpace(*input.Username)
	}
	name := current.DisplayName
	if input.DisplayName != nil {
		name = strings.TrimSpace(*input.DisplayName)
	}
	status := current.Status
	if input.Status != nil {
		status = *input.Status
	}
	if username == "" || name == "" || (status != "active" && status != "disabled") {
		return User{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "invalid user fields")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin user update: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if status == "disabled" {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('active_system_administrators'))`); err != nil {
			return User{}, fmt.Errorf("lock administrator continuity: %w", err)
		}
		if err := s.requireAnotherActiveAdministrator(ctx, tx, id); err != nil {
			return User{}, err
		}
	}
	var v User
	err = tx.QueryRow(ctx, `UPDATE teaching.user_accounts SET org_unit_id=$2,username=$3,display_name=$4,status=$5 WHERE id=$1 RETURNING id::text,org_unit_id::text,username,display_name,status`, id, org, username, name, status).Scan(&v.ID, &v.OrgUnitID, &v.Username, &v.DisplayName, &v.Status)
	if err != nil {
		return User{}, dbInputError(err, "update user")
	}
	if status == "disabled" {
		_, err = tx.Exec(ctx, `UPDATE teaching.auth_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE user_id=$1`, id, s.now())
		if err != nil {
			return User{}, fmt.Errorf("revoke disabled user sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit user update: %w", err)
	}
	return v, nil
}

func (s *Service) CreateRole(ctx context.Context, actor Principal, userID string, request CreateRoleBinding) (RoleBinding, error) {
	if !request.ScopeOrgID.Set {
		return RoleBinding{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "scope_org_id is required")
	}
	input := RoleBinding{RoleCode: request.RoleCode, ScopeOrgID: request.ScopeOrgID.Value}
	if !validRole(input.RoleCode, input.ScopeOrgID) {
		return RoleBinding{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "invalid role scope")
	}
	if input.ScopeOrgID != nil {
		var isCollege bool
		err := s.pool.QueryRow(ctx, `SELECT kind='college' FROM teaching.org_units WHERE id=$1`, *input.ScopeOrgID).Scan(&isCollege)
		if errors.Is(err, pgx.ErrNoRows) || !isCollege {
			return RoleBinding{}, apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "scoped roles require a college")
		}
		if err != nil {
			return RoleBinding{}, dbInputError(err, "inspect role scope")
		}
	}
	user, err := s.GetUser(ctx, actor, userID)
	if err != nil {
		return RoleBinding{}, err
	}
	if !actor.Has("sys_admin") {
		if userID == actor.UserID || !allowedAcademicGrant(actor, input) {
			return RoleBinding{}, apperror.New(http.StatusForbidden, "FORBIDDEN", "role grant is not allowed")
		}
		if err := s.requireManageableUser(ctx, actor, userID, user.OrgUnitID); err != nil {
			return RoleBinding{}, err
		}
	}
	var v RoleBinding
	err = s.pool.QueryRow(ctx, `INSERT INTO teaching.role_bindings(user_id,role_code,scope_org_id) VALUES($1,$2,$3) RETURNING id::text,role_code,scope_org_id::text`, userID, input.RoleCode, input.ScopeOrgID).Scan(&v.ID, &v.RoleCode, &v.ScopeOrgID)
	if err != nil {
		return RoleBinding{}, dbInputError(err, "create role binding")
	}
	return v, nil
}

func (s *Service) DeleteRole(ctx context.Context, actor Principal, userID, bindingID string) error {
	user, err := s.GetUser(ctx, actor, userID)
	if err != nil {
		return err
	}
	var role RoleBinding
	err = s.pool.QueryRow(ctx, `SELECT id::text,role_code,scope_org_id::text FROM teaching.role_bindings WHERE id=$1 AND user_id=$2`, bindingID, userID).Scan(&role.ID, &role.RoleCode, &role.ScopeOrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(http.StatusNotFound, "NOT_FOUND", "role binding not found")
	}
	if err != nil {
		return err
	}
	if !actor.Has("sys_admin") {
		if userID == actor.UserID || !allowedAcademicGrant(actor, role) {
			return apperror.New(http.StatusForbidden, "FORBIDDEN", "role removal is not allowed")
		}
		if err := s.requireManageableUser(ctx, actor, userID, user.OrgUnitID); err != nil {
			return err
		}
	}
	if role.RoleCode == "sys_admin" {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin role removal: %w", err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('active_system_administrators'))`); err != nil {
			return fmt.Errorf("lock administrator continuity: %w", err)
		}
		if err := s.requireAnotherActiveAdministrator(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM teaching.role_bindings WHERE id=$1 AND user_id=$2`, bindingID, userID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM teaching.role_bindings WHERE id=$1 AND user_id=$2`, bindingID, userID)
	return err
}

type rowGetter interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) requireAnotherActiveAdministrator(ctx context.Context, q rowGetter, userID string) error {
	var targetIsAdmin, anotherExists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM teaching.role_bindings WHERE user_id=$1 AND role_code='sys_admin'),
		       EXISTS(SELECT 1 FROM teaching.user_accounts u JOIN teaching.role_bindings r ON r.user_id=u.id
		              WHERE r.role_code='sys_admin' AND u.status='active' AND u.id<>$1)`, userID).Scan(&targetIsAdmin, &anotherExists)
	if err != nil {
		return fmt.Errorf("check administrator continuity: %w", err)
	}
	if targetIsAdmin && !anotherExists {
		return apperror.New(http.StatusConflict, "INVALID_STATE", "the last active system administrator cannot be disabled or unassigned")
	}
	return nil
}

func (s *Service) requireManageableUser(ctx context.Context, actor Principal, userID string, orgID *string) error {
	if orgID == nil {
		return apperror.New(http.StatusNotFound, "NOT_FOUND", "user not found")
	}
	allowed, err := s.inAcademicScope(ctx, actor, *orgID)
	if err != nil {
		return err
	}
	if !allowed {
		return apperror.New(http.StatusNotFound, "NOT_FOUND", "user not found")
	}
	var unsafe bool
	scopes := academicScopes(actor)
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teaching.role_bindings WHERE user_id=$1 AND (role_code IN ('sys_admin','academic_admin') OR (scope_org_id IS NOT NULL AND NOT scope_org_id=ANY($2::uuid[]))))`, userID, scopes).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "account has protected or cross-college roles")
	}
	return nil
}

func (s *Service) inAcademicScope(ctx context.Context, actor Principal, orgID string) (bool, error) {
	var college *string
	if err := s.pool.QueryRow(ctx, `SELECT teaching.college_of($1)`, orgID).Scan(&college); err != nil {
		return false, err
	}
	return college != nil && actor.Scoped("academic_admin", *college), nil
}

func academicScopes(p Principal) []string {
	r := []string{}
	for _, b := range p.Roles {
		if b.RoleCode == "academic_admin" && b.ScopeOrgID != nil {
			r = append(r, *b.ScopeOrgID)
		}
	}
	return r
}
func allowedAcademicGrant(p Principal, r RoleBinding) bool {
	return r.RoleCode == "teacher" && r.ScopeOrgID == nil || r.RoleCode == "supervisor" && r.ScopeOrgID != nil && p.Scoped("academic_admin", *r.ScopeOrgID)
}
func validRole(role string, scope *string) bool {
	return (role == "sys_admin" || role == "teacher") && scope == nil || (role == "academic_admin" || role == "supervisor") && scope != nil
}

func dbInputError(err error, operation string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "a unique field already exists", map[string]any{"constraint": pgErr.ConstraintName})
		case "23503", "23514", "22P02":
			return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "related resource or field is invalid")
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
