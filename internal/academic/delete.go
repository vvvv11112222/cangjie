package academic

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

// DeleteUnused removes only one unreferenced resource. Foreign keys remain the
// final concurrent-write guard: this never cascades through teaching history.
func (s *Service) DeleteUnused(ctx context.Context, p identity.Principal, resource, id string) error {
	tables := map[string]string{"org-units": "org_units", "terms": "academic_terms", "courses": "courses", "class-groups": "class_groups", "classrooms": "classrooms", "offerings": "course_offerings", "schedules": "schedule_entries"}
	table, ok := tables[resource]
	if !ok {
		return invalid("unsupported resource")
	}
	global := resource == "org-units" || resource == "terms" || resource == "classrooms"
	if global {
		if err := requireSystem(p); err != nil {
			return err
		}
	}
	if !global && !p.Has("sys_admin") && !p.Has("academic_admin") {
		return apperror.New(http.StatusForbidden, "FORBIDDEN", "academic management permission required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var org string
	if global {
		var locked string
		err = tx.QueryRow(ctx, "SELECT id::text FROM teaching."+table+" WHERE id=$1 FOR UPDATE", id).Scan(&locked)
	} else if resource == "schedules" {
		var offering string
		err = tx.QueryRow(ctx, "SELECT offering_id::text FROM teaching.schedule_entries WHERE id=$1 FOR UPDATE", id).Scan(&offering)
		if err == nil {
			err = tx.QueryRow(ctx, "SELECT org_unit_id::text FROM teaching.course_offerings WHERE id=$1", offering).Scan(&org)
		}
	} else {
		err = tx.QueryRow(ctx, "SELECT org_unit_id::text FROM teaching."+table+" WHERE id=$1 FOR UPDATE", id).Scan(&org)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(resource)
	}
	if err != nil {
		return err
	}
	if !global {
		if err = requireManageOrg(ctx, tx, p, org); err != nil {
			return err
		}
	}
	// Never remove the school root, even in an otherwise empty installation.
	if resource == "org-units" {
		var kind string
		if err = tx.QueryRow(ctx, "SELECT kind FROM teaching.org_units WHERE id=$1", id).Scan(&kind); err != nil {
			return err
		}
		if kind == "school" {
			return apperror.WithDetails(http.StatusConflict, "INVALID_STATE", "school root is protected", map[string]any{"reason": "学校根组织不能删除；可以编辑名称。"})
		}
	}
	_, err = tx.Exec(ctx, "DELETE FROM teaching."+table+" WHERE id=$1", id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return apperror.WithDetails(http.StatusConflict, "INVALID_STATE", "resource is referenced", map[string]any{"reason": "该资料已被下级组织、账号、课程、开课、排课或课堂引用，不能直接删除。请先处理关联资料；有授课历史的开课应归档，排课应取消。"})
	}
	if err != nil {
		return fmt.Errorf("delete unused academic resource: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id) VALUES($1,'academic.delete',$2,$3,'success','service')`, p.UserID, resource, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return apperror.WithDetails(http.StatusConflict, "INVALID_STATE", "resource is referenced", map[string]any{"reason": "该资料已被历史记录引用，不能删除；请使用归档或取消。"})
		}
		return err
	}
	return nil
}
