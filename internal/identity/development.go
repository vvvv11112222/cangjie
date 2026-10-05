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

type DevelopmentUsersRequest struct {
	SchoolCode            string
	SchoolName            string
	CollegeCode           string
	CollegeName           string
	AdminUsername         string
	AdminDisplayName      string
	AcademicUsername      string
	AcademicDisplayName   string
	SupervisorUsername    string
	SupervisorDisplayName string
	TeacherUsername       string
	TeacherDisplayName    string
	Password              string
}

func DevelopmentUsersRequestFromEnvironment() (DevelopmentUsersRequest, error) {
	request := DevelopmentUsersRequest{
		SchoolCode:            environment("BOOTSTRAP_SCHOOL_CODE", "SCHOOL"),
		SchoolName:            environment("BOOTSTRAP_SCHOOL_NAME", "示例学校"),
		CollegeCode:           environment("BOOTSTRAP_COLLEGE_CODE", "SOFTWARE"),
		CollegeName:           environment("BOOTSTRAP_COLLEGE_NAME", "软件学院"),
		AdminUsername:         environment("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		AdminDisplayName:      environment("BOOTSTRAP_ADMIN_DISPLAY_NAME", "系统管理员"),
		AcademicUsername:      environment("BOOTSTRAP_ACADEMIC_USERNAME", "academic_demo"),
		AcademicDisplayName:   environment("BOOTSTRAP_ACADEMIC_DISPLAY_NAME", "教务管理员示例"),
		SupervisorUsername:    environment("BOOTSTRAP_SUPERVISOR_USERNAME", "supervisor_demo"),
		SupervisorDisplayName: environment("BOOTSTRAP_SUPERVISOR_DISPLAY_NAME", "教学督导示例"),
		TeacherUsername:       environment("BOOTSTRAP_TEACHER_USERNAME", "teacher_demo"),
		TeacherDisplayName:    environment("BOOTSTRAP_TEACHER_DISPLAY_NAME", "任课教师示例"),
		Password:              strings.TrimSpace(os.Getenv("BOOTSTRAP_TEST_PASSWORD")),
	}
	if request.Password == "" {
		return DevelopmentUsersRequest{}, fmt.Errorf("BOOTSTRAP_TEST_PASSWORD is required")
	}
	if len(request.Password) < 12 {
		return DevelopmentUsersRequest{}, fmt.Errorf("BOOTSTRAP_TEST_PASSWORD must contain at least 12 characters")
	}
	return request, nil
}

func BootstrapDevelopmentUsers(ctx context.Context, pool *pgxpool.Pool, request DevelopmentUsersRequest) (int, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin development seed transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('bootstrap_development_users'))`); err != nil {
		return 0, fmt.Errorf("lock development seed: %w", err)
	}

	schoolID, err := ensureOrganization(ctx, tx, request.SchoolCode, request.SchoolName, "school", nil)
	if err != nil {
		return 0, err
	}
	collegeID, err := ensureOrganization(ctx, tx, request.CollegeCode, request.CollegeName, "college", &schoolID)
	if err != nil {
		return 0, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("hash development password: %w", err)
	}
	users := []struct {
		username, displayName, role string
		scope                       *string
	}{
		{request.AcademicUsername, request.AcademicDisplayName, "academic_admin", &collegeID},
		{request.SupervisorUsername, request.SupervisorDisplayName, "supervisor", &collegeID},
		{request.TeacherUsername, request.TeacherDisplayName, "teacher", nil},
	}
	created := 0
	for _, user := range users {
		wasCreated, err := ensureDevelopmentUser(ctx, tx, collegeID, user.username, user.displayName, string(passwordHash), user.role, user.scope)
		if err != nil {
			return 0, err
		}
		if wasCreated {
			created++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit development seed: %w", err)
	}
	return created, nil
}

func ensureOrganization(ctx context.Context, tx pgx.Tx, code, name, kind string, parentID *string) (string, error) {
	var id, existingKind string
	var parentMatches bool
	err := tx.QueryRow(ctx, `
		SELECT id::text, kind, parent_id IS NOT DISTINCT FROM $2::uuid
		FROM teaching.org_units WHERE code=$1`, code, parentID).Scan(&id, &existingKind, &parentMatches)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO teaching.org_units (parent_id,code,name,kind)
			VALUES ($1,$2,$3,$4) RETURNING id::text`, parentID, code, name, kind).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("ensure %s organization %q: %w", kind, code, err)
	}
	if existingKind != "" && (existingKind != kind || !parentMatches) {
		return "", fmt.Errorf("organization code %q has incompatible kind or parent", code)
	}
	return id, nil
}

func ensureDevelopmentUser(ctx context.Context, tx pgx.Tx, orgID, username, displayName, passwordHash, role string, scopeID *string) (bool, error) {
	var userID string
	var organizationMatches, roleMatches bool
	err := tx.QueryRow(ctx, `
		SELECT u.id::text, u.org_unit_id=$2::uuid,
		       EXISTS (
		           SELECT 1 FROM teaching.role_bindings r
		           WHERE r.user_id=u.id AND r.role_code=$3
		             AND r.scope_org_id IS NOT DISTINCT FROM $4::uuid
		       )
		FROM teaching.user_accounts u WHERE u.username=$1`, username, orgID, role, scopeID).Scan(&userID, &organizationMatches, &roleMatches)
	if err == nil {
		if !organizationMatches || !roleMatches {
			return false, fmt.Errorf("username %q already belongs to an incompatible account", username)
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("inspect development user %q: %w", username, err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO teaching.user_accounts (org_unit_id,username,password_hash,display_name,status)
		VALUES ($1,$2,$3,$4,'active') RETURNING id::text`, orgID, username, passwordHash, displayName).Scan(&userID); err != nil {
		return false, fmt.Errorf("create development user %q: %w", username, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO teaching.role_bindings (user_id,role_code,scope_org_id)
		VALUES ($1,$2,$3)`, userID, role, scopeID); err != nil {
		return false, fmt.Errorf("grant development role %q: %w", role, err)
	}
	return true, nil
}
