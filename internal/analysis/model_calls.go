package analysis

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vvvv11112222/cangjie/internal/apperror"
	"github.com/vvvv11112222/cangjie/internal/identity"
)

var moneyPattern = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

type ModelCall struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	ReservedCost      string     `json:"reserved_cost"`
	ActualCost        *string    `json:"actual_cost"`
	Currency          string     `json:"currency"`
	BillingPeriod     string     `json:"billing_period"`
	PriceVersion      *string    `json:"price_version"`
	LockVersion       int        `json:"lock_version"`
	DispatchStartedAt *time.Time `json:"dispatch_started_at"`
	SettledAt         *time.Time `json:"settled_at"`
}

type ReconcileCall struct {
	LockVersion       int     `json:"lock_version"`
	Status            string  `json:"status"`
	ActualCost        string  `json:"actual_cost"`
	ProviderRequestID *string `json:"provider_request_id"`
	Reason            string  `json:"reason"`
}

const modelCallColumns = `id::text,status,to_char(reserved_cost,'FM999999999990.000000'),
	CASE WHEN actual_cost IS NULL THEN NULL ELSE to_char(actual_cost,'FM999999999990.000000') END,
	currency,billing_period::text,price_version,lock_version,dispatch_started_at,settled_at`

func scanModelCall(row pgx.Row) (ModelCall, error) {
	var value ModelCall
	err := row.Scan(&value.ID, &value.Status, &value.ReservedCost, &value.ActualCost, &value.Currency, &value.BillingPeriod,
		&value.PriceVersion, &value.LockVersion, &value.DispatchStartedAt, &value.SettledAt)
	return value, err
}

func (s *Service) ListModelCalls(ctx context.Context, p identity.Principal, after string, limit int) ([]ModelCall, string, error) {
	if !p.Has("sys_admin") {
		return nil, "", forbidden()
	}
	rows, err := s.pool.Query(ctx, `SELECT `+modelCallColumns+` FROM teaching.model_calls
		WHERE ($1='' OR (created_at,id)>(SELECT created_at,id FROM teaching.model_calls WHERE id=$1::uuid))
		ORDER BY created_at,id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []ModelCall{}
	for rows.Next() {
		value, scanErr := scanModelCall(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, value)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (s *Service) ReconcileModelCall(ctx context.Context, p identity.Principal, id string, in ReconcileCall) (ModelCall, error) {
	if !p.Has("sys_admin") {
		return ModelCall{}, forbidden()
	}
	if in.LockVersion < 1 || (in.Status != "succeeded" && in.Status != "failed") || !moneyPattern.MatchString(in.ActualCost) || strings.TrimSpace(in.Reason) == "" {
		return ModelCall{}, invalid("invalid model call reconciliation")
	}
	if in.ProviderRequestID != nil {
		trimmed := strings.TrimSpace(*in.ProviderRequestID)
		if trimmed == "" {
			return ModelCall{}, invalid("provider_request_id must be non-empty or null")
		}
		in.ProviderRequestID = &trimmed
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ModelCall{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var status string
	var lockVersion int
	if err = tx.QueryRow(ctx, `SELECT status,lock_version FROM teaching.model_calls WHERE id=$1 FOR UPDATE`, id).Scan(&status, &lockVersion); errors.Is(err, pgx.ErrNoRows) {
		return ModelCall{}, notFound()
	} else if err != nil {
		return ModelCall{}, err
	}
	if lockVersion != in.LockVersion {
		return ModelCall{}, revisionConflict()
	}
	if status != "unknown" {
		return ModelCall{}, apperror.New(http.StatusConflict, "INVALID_STATE", "only unknown model calls can be reconciled")
	}
	value, err := scanModelCall(tx.QueryRow(ctx, `UPDATE teaching.model_calls SET status=$2,actual_cost=$3::numeric,
		provider_request_id=$4,reconciled_by=$5,reconciliation_reason=$6,settled_at=now(),lock_version=lock_version+1
		WHERE id=$1 RETURNING `+modelCallColumns, id, in.Status, in.ActualCost, in.ProviderRequestID, p.UserID, strings.TrimSpace(in.Reason)))
	if err != nil {
		return ModelCall{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO teaching.audit_logs(actor_user_id,action,resource_type,resource_id,result,request_id,metadata)
		VALUES($1,'model_call.reconcile','model_call',$2,'success','service',jsonb_build_object('status',$3::text,'reason',$4::text))`,
		p.UserID, id, in.Status, strings.TrimSpace(in.Reason)); err != nil {
		return ModelCall{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ModelCall{}, err
	}
	return value, nil
}
