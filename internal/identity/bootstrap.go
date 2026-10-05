package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type BootstrapRequest struct {
	SchoolCode  string
	SchoolName  string
	Username    string
	DisplayName string
	Password    string
}

func BootstrapRequestFromEnvironment() (BootstrapRequest, error) {
	request := BootstrapRequest{
		SchoolCode:  environment("BOOTSTRAP_SCHOOL_CODE", "SCHOOL"),
		SchoolName:  environment("BOOTSTRAP_SCHOOL_NAME", "示例学校"),
		Username:    environment("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		DisplayName: environment("BOOTSTRAP_ADMIN_DISPLAY_NAME", "系统管理员"),
		Password:    strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")),
	}
	if request.Password == "" {
		return BootstrapRequest{}, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD is required")
	}
	if len(request.Password) < 12 {
		return BootstrapRequest{}, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must contain at least 12 characters")
	}
	return request, nil
}

func BootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, request BootstrapRequest) (bool, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin bootstrap transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('bootstrap_system_administrator'))`); err != nil {
		return false, fmt.Errorf("lock administrator bootstrap: %w", err)
	}

	var schoolID, organizationKind string
	err = tx.QueryRow(ctx, `SELECT id::text, kind FROM teaching.org_units WHERE code=$1`, request.SchoolCode).Scan(&schoolID, &organizationKind)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO teaching.org_units (code,name,kind) VALUES ($1,$2,'school') RETURNING id::text, kind`, request.SchoolCode, request.SchoolName).Scan(&schoolID, &organizationKind)
	} else if err == nil && organizationKind != "school" {
		return false, fmt.Errorf("organization code %q already belongs to kind %q", request.SchoolCode, organizationKind)
	}
	if err != nil {
		return false, fmt.Errorf("ensure school organization: %w", err)
	}

	var userID, existingOrgID string
	var existingAdmin bool
	err = tx.QueryRow(ctx, `
		SELECT u.id::text, u.org_unit_id::text,
		       EXISTS (SELECT 1 FROM teaching.role_bindings r WHERE r.user_id=u.id AND r.role_code='sys_admin')
		FROM teaching.user_accounts u WHERE u.username=$1`, request.Username).Scan(&userID, &existingOrgID, &existingAdmin)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			return false, fmt.Errorf("hash administrator password: %w", hashErr)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO teaching.user_accounts (org_unit_id,username,password_hash,display_name,status)
			VALUES ($1,$2,$3,$4,'active') RETURNING id::text`,
			schoolID, request.Username, string(hash), request.DisplayName,
		).Scan(&userID)
		created = err == nil
	} else if err == nil && (!existingAdmin || existingOrgID != schoolID) {
		return false, fmt.Errorf("username %q already belongs to a different or non-administrator account", request.Username)
	}
	if err != nil {
		return false, fmt.Errorf("ensure administrator account: %w", err)
	}
	if created {
		if _, err := tx.Exec(ctx, `
			INSERT INTO teaching.role_bindings (user_id,role_code,scope_org_id)
			VALUES ($1,'sys_admin',NULL)`, userID); err != nil {
			return false, fmt.Errorf("ensure administrator role: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit administrator bootstrap: %w", err)
	}
	return created, nil
}

func environment(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
