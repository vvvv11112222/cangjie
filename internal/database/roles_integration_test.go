package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Role changes and schema resets are confined to the explicitly opted-in fixture.
func TestRoleSeparationGuards(t *testing.T) {
	url := os.Getenv("DATABASE_ACCEPTANCE_URL")
	if url == "" {
		t.Skip("DATABASE_ACCEPTANCE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil || cfg.Database != "cangjie_database_acceptance" || os.Getenv("DATABASE_ACCEPTANCE_ALLOW_RESET") != "1" {
		t.Fatal("requires cangjie_database_acceptance and DATABASE_ACCEPTANCE_ALLOW_RESET=1")
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('database_acceptance'))`); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('database_acceptance'))`)
	root := filepath.Join("..", "..", "database")
	body, err := os.ReadFile(filepath.Join(root, "configure_roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, setup, want string
	}{
		{"clean", "", ""},
		{"inherited_insert", "GRANT INSERT ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"inherited_update", "GRANT UPDATE ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"inherited_delete", "GRANT DELETE ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"inherited_truncate", "GRANT TRUNCATE ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"inherited_column_insert", "GRANT INSERT(name) ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"inherited_column_update", "GRANT UPDATE(checksum) ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT TRUE, SET FALSE;", "history-write"},
		{"public_delete", "GRANT DELETE ON public.schema_migrations TO PUBLIC;", "history-write"},
		{"set_schema", "GRANT USAGE,CREATE ON SCHEMA teaching TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", "elevated/DDL"},
		{"indirect_set_schema", "GRANT USAGE,CREATE ON SCHEMA teaching TO %[3]s; GRANT %[3]s TO %[4]s WITH INHERIT FALSE, SET TRUE; GRANT %[4]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", "elevated/DDL"},
		{"set_history", "GRANT INSERT ON public.schema_migrations TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", "history-write"},
		{"set_create_role", "ALTER ROLE %[3]s CREATEROLE; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", "elevated/DDL"},
		{"admin_can_enable_set", "GRANT CREATE ON SCHEMA teaching TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET FALSE, ADMIN TRUE;", "elevated/DDL"},
		{"set_view_owner", "CREATE VIEW teaching.role_owned AS SELECT 1 AS id; ALTER VIEW teaching.role_owned OWNER TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", "elevated/DDL"},
		{"harmless_set_role", "GRANT USAGE ON SCHEMA teaching TO %[3]s; GRANT SELECT ON teaching.org_units TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET TRUE;", ""},
		{"inaccessible_ddl_role", "GRANT CREATE ON SCHEMA teaching TO %[3]s; GRANT %[3]s TO %[2]s WITH INHERIT FALSE, SET FALSE, ADMIN FALSE;", ""},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must := func(sql string) {
				t.Helper()
				if _, err := conn.Exec(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
			must(`DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations`)
			if err := RunMigrations(ctx, url, root); err != nil {
				t.Fatal(err)
			}
			prefix := fmt.Sprintf("role_guard_%d_%d", time.Now().UnixNano(), i)
			names := []string{prefix + "_migration", prefix + "_app", prefix + "_helper", prefix + "_middle"}
			quoted := make([]string, len(names))
			for j, name := range names {
				quoted[j] = pgx.Identifier{name}.Sanitize()
				must("CREATE ROLE " + quoted[j] + " NOLOGIN")
			}
			t.Cleanup(func() {
				cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanCancel()
				_, err := conn.Exec(cleanCtx, `ROLLBACK; RESET SESSION AUTHORIZATION; DROP SCHEMA IF EXISTS teaching CASCADE; DROP TABLE IF EXISTS public.schema_migrations`)
				if err == nil {
					_, err = conn.Exec(cleanCtx, "DROP OWNED BY "+strings.Join(quoted, ",")+" CASCADE; DROP ROLE "+strings.Join(quoted, ","))
				}
				if err != nil {
					t.Errorf("role fixture cleanup: %v", err)
				}
			})
			if tc.setup != "" {
				must(strings.NewReplacer("%[1]s", quoted[0], "%[2]s", quoted[1], "%[3]s", quoted[2], "%[4]s", quoted[3]).Replace(tc.setup))
			}
			if _, err := conn.Exec(ctx, `SELECT set_config('cangjie.migration_role',$1,false),set_config('cangjie.app_role',$2,false)`, names[0], names[1]); err != nil {
				t.Fatal(err)
			}
			var ownerBefore string
			if err := conn.QueryRow(ctx, `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.schema_migrations'::regclass`).Scan(&ownerBefore); err != nil {
				t.Fatal(err)
			}
			_, err := conn.Exec(ctx, string(body))
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected %q rejection, got %v", tc.want, err)
				}
				must("ROLLBACK")
				var ownerAfter string
				if err := conn.QueryRow(ctx, `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.schema_migrations'::regclass`).Scan(&ownerAfter); err != nil || ownerAfter != ownerBefore {
					t.Fatalf("failed role setup changed owner: %q -> %q err=%v", ownerBefore, ownerAfter, err)
				}
				var granted bool
				if err := conn.QueryRow(ctx, `SELECT has_database_privilege($1,current_database(),'CREATE')`, names[0]).Scan(&granted); err != nil || granted {
					t.Fatalf("failed role setup retained migration CREATE grant: %v err=%v", granted, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Successful setup remains idempotent and permits legitimate business writes.
			must(string(body))
			must("SET SESSION AUTHORIZATION " + quoted[1])
			must(`INSERT INTO teaching.org_units(code,name,kind) VALUES('ROLE-CHECK','Synthetic','college'); INSERT INTO teaching.audit_logs(action,resource_type,result,request_id) VALUES('role.check','synthetic','success','synthetic')`)
			var canWrite bool
			if err := conn.QueryRow(ctx, `SELECT has_table_privilege(current_user,'public.schema_migrations','INSERT,UPDATE,DELETE,TRUNCATE')`).Scan(&canWrite); err != nil || canWrite {
				t.Fatalf("business role can write migration history: %v err=%v", canWrite, err)
			}
			if tc.name == "harmless_set_role" {
				must("SET ROLE " + quoted[2])
				must("SELECT * FROM teaching.org_units")
				must("RESET ROLE")
			}
			must("RESET SESSION AUTHORIZATION")
		})
	}
}
