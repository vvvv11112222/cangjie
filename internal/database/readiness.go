package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CheckSchema checks deployment compatibility, not just network reachability.
// Calling it in readiness also detects an accidental migration-file downgrade.
func (p *Pool) CheckSchema(ctx context.Context, directory string) error {
	migrations, err := loadMigrations(directory)
	if err != nil {
		return err
	}
	conn, err := p.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire schema check connection: %w", err)
	}
	defer conn.Release()
	rows, err := conn.Query(ctx, `SELECT name, checksum FROM public.schema_migrations`)
	if err != nil {
		return fmt.Errorf("migration history is unavailable: %w", err)
	}
	applied := make(map[string]string)
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			rows.Close()
			return err
		}
		applied[name] = checksum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := validateMigrationHistory(migrations, applied); err != nil {
		return err
	}
	if len(applied) != len(migrations) {
		return fmt.Errorf("database has pending migrations")
	}
	var valid bool
	err = conn.QueryRow(ctx, `WITH required(table_name, constraint_name) AS (VALUES
		('analysis_jobs','ck_job_execution'), ('model_calls','ck_call_settlement'),
		('export_jobs','ck_export_lease'), ('data_deletion_requests','ck_deletion_lease'))
		SELECT bool_and(COALESCE(c.convalidated,false)) FROM required r
		LEFT JOIN pg_namespace n ON n.nspname='teaching'
		LEFT JOIN pg_class t ON t.relnamespace=n.oid AND t.relname=r.table_name
		LEFT JOIN pg_constraint c ON c.conrelid=t.oid AND c.conname=r.constraint_name`).Scan(&valid)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if !valid {
		return fmt.Errorf("runtime constraints are missing or not validated")
	}
	return nil
}
