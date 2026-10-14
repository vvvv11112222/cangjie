BEGIN;
SET search_path TO teaching, public;

ALTER TABLE data_deletion_requests
    ADD COLUMN required_worker_ids text[] NOT NULL DEFAULT '{}'::text[];

CREATE TABLE worker_cleanup_receipts (
    deletion_request_id uuid NOT NULL REFERENCES data_deletion_requests(id) ON DELETE CASCADE,
    worker_id text NOT NULL CHECK (worker_id <> ''),
    storage_generation bigint NOT NULL CHECK (storage_generation > 0),
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (deletion_request_id, worker_id)
);

COMMIT;
