-- Apply after 006 with writers stopped. Do not manufacture historical leases
-- or zero-cost settlements: inconsistent rows must be reconciled by their owner.
BEGIN;
SET search_path TO teaching, public;
ALTER TABLE analysis_jobs VALIDATE CONSTRAINT ck_job_execution;
ALTER TABLE model_calls VALIDATE CONSTRAINT ck_call_settlement;
ALTER TABLE export_jobs VALIDATE CONSTRAINT ck_export_lease;
ALTER TABLE data_deletion_requests VALIDATE CONSTRAINT ck_deletion_lease;
COMMIT;
