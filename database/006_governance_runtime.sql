BEGIN;
SET search_path TO teaching, public;

-- These APIs did not exist before 006. If unsupported direct database writes
-- created rows, stop instead of inventing a deletion tombstone or export
-- digest that cannot be reconciled with an independent ledger/object.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM data_deletion_requests WHERE tombstone_id IS NULL) THEN
        RAISE EXCEPTION '006 blocked: reconcile legacy deletion requests with the independent ledger before migration';
    END IF;
    IF EXISTS (SELECT 1 FROM export_jobs WHERE status='succeeded') THEN
        RAISE EXCEPTION '006 blocked: reconcile legacy successful exports and their object metadata before migration';
    END IF;
END $$;

-- Governance jobs pin the classroom generation that was current when they
-- started. A late executor can therefore never publish an object after a
-- deletion request has revoked the classroom.
ALTER TABLE export_jobs
    ADD COLUMN storage_generation bigint NOT NULL DEFAULT 0 CHECK (storage_generation >= 0),
    ADD COLUMN mime_type text,
    ADD COLUMN byte_size bigint CHECK (byte_size IS NULL OR byte_size > 0),
    ADD COLUMN sha256 text CHECK (sha256 IS NULL OR sha256 ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT ck_export_result_metadata CHECK (
        (status = 'succeeded' AND mime_type IS NOT NULL AND byte_size IS NOT NULL AND sha256 IS NOT NULL)
        OR status <> 'succeeded');

CREATE TABLE governance_workers (
    worker_id text PRIMARY KEY CHECK (worker_id <> ''),
    capabilities text[] NOT NULL CHECK (cardinality(capabilities) > 0),
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ix_governance_workers_seen ON governance_workers(last_seen_at DESC);

-- A tombstone is written to the independent ledger before this row is made.
-- Keeping it mandatory prevents new requests from silently bypassing replay.
ALTER TABLE data_deletion_requests
    ALTER COLUMN tombstone_id SET NOT NULL;

COMMIT;
