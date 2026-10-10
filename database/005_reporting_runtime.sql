BEGIN;
SET search_path TO teaching, public;

ALTER TABLE reports
    ADD COLUMN provenance jsonb CHECK (provenance IS NULL OR jsonb_typeof(provenance)='object');

CREATE INDEX ix_evidence_run_available ON evidence_items(run_id, start_ms, id)
    WHERE availability='available';

COMMIT;
