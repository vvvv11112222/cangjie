-- Administrative deployment step, not a numbered migration. Run after migration.
-- Supply existing dedicated LOGIN roles via cangjie.migration_role and cangjie.app_role.
-- Passwords/authentication belong to the deployment secret store, not this script.
BEGIN;
DO $$
DECLARE
    migration_role text := current_setting('cangjie.migration_role');
    app_role text := current_setting('cangjie.app_role');
    item record;
BEGIN
    IF migration_role = app_role OR migration_role = '' OR app_role = '' THEN
        RAISE EXCEPTION 'Dedicated and distinct migration/application roles are required';
    END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=migration_role AND NOT rolsuper)
        OR NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=app_role AND NOT rolsuper
            AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls) THEN
        RAISE EXCEPTION 'Roles must exist; application role must not have elevated attributes';
    END IF;
    IF pg_has_role(app_role,migration_role,'MEMBER') THEN
        RAISE EXCEPTION 'Application role must not inherit or assume the migration role';
    END IF;
    EXECUTE format('GRANT CONNECT, CREATE ON DATABASE %I TO %I',current_database(),migration_role);
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO %I',current_database(),app_role);
    EXECUTE format('GRANT USAGE, CREATE ON SCHEMA public TO %I',migration_role);
    EXECUTE format('ALTER SCHEMA teaching OWNER TO %I',migration_role);
    FOR item IN SELECT c.relname,c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='teaching' AND c.relkind IN('r','p','S') ORDER BY c.relkind DESC LOOP
        EXECUTE format('ALTER %s teaching.%I OWNER TO %I',
            CASE WHEN item.relkind='S' THEN 'SEQUENCE' ELSE 'TABLE' END,item.relname,migration_role);
    END LOOP;
    FOR item IN SELECT p.oid::regprocedure AS signature FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
        WHERE n.nspname='teaching' LOOP
        EXECUTE format('ALTER FUNCTION %s OWNER TO %I',item.signature,migration_role);
    END LOOP;
    EXECUTE format('ALTER TABLE public.schema_migrations OWNER TO %I',migration_role);
    REVOKE ALL ON SCHEMA teaching FROM PUBLIC;
    REVOKE ALL ON ALL TABLES IN SCHEMA teaching FROM PUBLIC;
    REVOKE ALL ON ALL SEQUENCES IN SCHEMA teaching FROM PUBLIC;
    REVOKE ALL ON ALL FUNCTIONS IN SCHEMA teaching FROM PUBLIC;
    EXECUTE format('REVOKE ALL ON SCHEMA public FROM %I',app_role);
    EXECUTE format('GRANT USAGE ON SCHEMA public,teaching TO %I',app_role);
    EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA teaching TO %I',app_role);
    EXECUTE format('GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA teaching TO %I',app_role);
    EXECUTE format('GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA teaching TO %I',app_role);
    EXECUTE format('REVOKE ALL ON public.schema_migrations FROM %I',app_role);
    EXECUTE format('GRANT SELECT ON public.schema_migrations TO %I',app_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA teaching GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO %I',migration_role,app_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA teaching GRANT USAGE,SELECT ON SEQUENCES TO %I',migration_role,app_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC',migration_role);
    EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA teaching GRANT EXECUTE ON FUNCTIONS TO %I',migration_role,app_role);
    IF has_schema_privilege(app_role,'teaching','CREATE') OR has_schema_privilege(app_role,'public','CREATE')
        OR has_table_privilege(app_role,'public.schema_migrations','UPDATE') THEN
        RAISE EXCEPTION 'Application role still inherits DDL/history-write privileges; fix existing grants before proceeding';
    END IF;
END $$;
COMMIT;
