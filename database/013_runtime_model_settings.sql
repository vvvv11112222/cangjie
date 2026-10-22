-- Public settings and an opaque reference only. API keys stay in private files.
CREATE TABLE teaching.runtime_model_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision uuid NOT NULL DEFAULT gen_random_uuid(),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(settings) = 'object' AND NOT (settings ?| ARRAY['asr_api_key','report_api_key','ASR_API_KEY','REPORT_API_KEY'])),
    secret_revision uuid CHECK (secret_revision IS NULL OR secret_revision = revision),
    -- Historical actor UUID: global configuration survives user/org removal.
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO teaching.runtime_model_settings(singleton) VALUES(true);
