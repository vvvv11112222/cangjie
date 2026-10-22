-- Keep original upload names as display metadata, never as storage paths.
ALTER TABLE teaching.media_assets ADD COLUMN original_filename text;
ALTER TABLE teaching.media_assets ADD CONSTRAINT media_original_filename_length
    CHECK (original_filename IS NULL OR char_length(original_filename) BETWEEN 1 AND 255);
