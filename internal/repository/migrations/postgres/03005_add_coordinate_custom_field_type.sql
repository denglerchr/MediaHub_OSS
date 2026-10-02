-- v3.2 changes
-- Migration: Add COORDINATE custom field type & queued_count
-- Description: Updates CHECK constraint on database_custom_fields.type to include 'COORDINATE' and adds queued_count to databases

-- +goose Up
ALTER TABLE database_custom_fields DROP CONSTRAINT IF EXISTS database_custom_fields_type_check;
ALTER TABLE database_custom_fields ADD CONSTRAINT database_custom_fields_type_check CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN', 'COORDINATE'));

ALTER TABLE databases ADD COLUMN IF NOT EXISTS queued_count BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_databases_queued_count ON databases(queued_count) WHERE queued_count > 0;

-- +goose Down
DROP INDEX IF EXISTS idx_databases_queued_count;
ALTER TABLE databases DROP COLUMN IF EXISTS queued_count;

ALTER TABLE database_custom_fields DROP CONSTRAINT IF EXISTS database_custom_fields_type_check;
ALTER TABLE database_custom_fields ADD CONSTRAINT database_custom_fields_type_check CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN'));

