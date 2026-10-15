package identity

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/apperror"
)

// PrincipalByID rechecks account status and roles for a long-lived delegated grant.
func (s *Service) PrincipalByID(ctx context.Context, id string) (Principal, error) {
	p := Principal{UserID: id}
	if err := s.pool.QueryRow(ctx, `SELECT display_name FROM teaching.user_accounts WHERE id=$1 AND status='active'`, id).Scan(&p.DisplayName); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return p, err
		}
		return p, apperror.New(403, "FORBIDDEN", "grant owner is not active")
	}
	var err error
	p.Roles, err = s.roles(ctx, s.pool, id)
	return p, err
}
