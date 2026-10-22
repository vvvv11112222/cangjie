package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"golang.org/x/crypto/bcrypt"
)

type ChangePassword struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}
type ResetPassword struct {
	NewPassword string `json:"new_password"`
}

// Password updates and session revocation are atomic. Neither passwords nor
// hashes enter audit metadata. Self-service always requires the old password.
func (s *Service) ChangePassword(ctx context.Context, actor Principal, input ChangePassword) error {
	return s.updatePassword(ctx, actor, actor.UserID, input.CurrentPassword, input.NewPassword, false)
}
func (s *Service) ResetPassword(ctx context.Context, actor Principal, id string, input ResetPassword) error {
	if strings.EqualFold(actor.UserID, id) {
		return apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "use self-service password change")
	}
	if !actor.Has("sys_admin") && !actor.Has("academic_admin") {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "account management permission required")
	}
	return s.updatePassword(ctx, actor, id, "", input.NewPassword, true)
}
func (s *Service) updatePassword(ctx context.Context, actor Principal, id, current, next string, reset bool) error {
	if len(next) < 12 || len(next) > 72 {
		return apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "invalid password length", map[string]any{"reason": "新密码须为12至72字节；中文等字符会占多个字节。"})
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var stored string
	var org *string
	err = tx.QueryRow(ctx, `SELECT password_hash,org_unit_id::text FROM teaching.user_accounts WHERE id=$1 FOR UPDATE`, id).Scan(&stored, &org)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.New(http.StatusNotFound, "NOT_FOUND", "user not found")
	}
	if err != nil {
		return err
	}
	if reset && !actor.Has("sys_admin") {
		if err = requireManageableUser(ctx, tx, actor, id, org); err != nil {
			return err
		}
	}
	if !reset && bcrypt.CompareHashAndPassword([]byte(stored), []byte(current)) != nil {
		return apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "current password is incorrect", map[string]any{"reason": "当前密码不正确。"})
	}
	if bcrypt.CompareHashAndPassword([]byte(stored), []byte(next)) == nil {
		return apperror.WithDetails(http.StatusBadRequest, "INVALID_ARGUMENT", "password is unchanged", map[string]any{"reason": "新密码不能与当前密码相同。"})
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.user_accounts SET password_hash=$2 WHERE id=$1`, id, string(hash)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE teaching.auth_sessions SET revoked_at=COALESCE(revoked_at,$2) WHERE user_id=$1`, id, s.now()); err != nil {
		return err
	}
	action := "password.change"
	if reset {
		action = "password.reset"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id) VALUES($1,$2,'user',$3,'success','service')`, actor.UserID, action, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
