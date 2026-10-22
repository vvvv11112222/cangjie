package governance

import (
	"context"
	"net/http"
	"time"

	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

// AuditRecord deliberately excludes metadata and content. IDs remain strings:
// PostgreSQL bigint IDs need not fit in a JavaScript number.
type AuditRecord struct {
	ID           string    `json:"id"`
	ActorUserID  *string   `json:"actor_user_id"`
	ActorName    *string   `json:"actor_name"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   *string   `json:"resource_id"`
	Result       string    `json:"result"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuditQuery struct {
	Before         int64
	Limit          int
	Action, Result string
}

func (s *Service) ListAudit(ctx context.Context, p identity.Principal, q AuditQuery) ([]AuditRecord, string, error) {
	if !p.Has("sys_admin") && !p.Has("academic_admin") && !p.Has("supervisor") {
		return nil, "", apperror.New(http.StatusForbidden, "FORBIDDEN", "audit permission required")
	}
	if q.Limit < 1 || q.Limit > 100 || q.Before < 0 || len(q.Action) > 100 || (q.Result != "" && q.Result != "success" && q.Result != "denied" && q.Result != "failure") {
		return nil, "", apperror.New(http.StatusBadRequest, "INVALID_ARGUMENT", "invalid audit filter")
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,a.actor_user_id::text,u.display_name,a.action,a.resource_type,a.resource_id::text,a.result,a.created_at
	 FROM teaching.audit_logs a LEFT JOIN teaching.user_accounts u ON u.id=a.actor_user_id
	 WHERE ($1::boolean OR a.actor_user_id=$2::uuid) AND ($3::bigint=0 OR a.id<$3)
	 AND ($4::text='' OR a.action=$4) AND ($5::text='' OR a.result=$5)
	 ORDER BY a.id DESC LIMIT $6`, p.Has("sys_admin"), p.UserID, q.Before, q.Action, q.Result, q.Limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []AuditRecord{}
	for rows.Next() {
		var v AuditRecord
		if err = rows.Scan(&v.ID, &v.ActorUserID, &v.ActorName, &v.Action, &v.ResourceType, &v.ResourceID, &v.Result, &v.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > q.Limit {
		items = items[:q.Limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}
