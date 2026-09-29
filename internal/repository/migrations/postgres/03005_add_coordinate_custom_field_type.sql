-- Migration: Add COORDINATE custom field type
-- Description: Updates CHECK constraint on database_custom_fields.type to include 'COORDINATE'

-- +goose Up
ALTER TABLE database_custom_fields DROP CONSTRAINT IF EXISTS database_custom_fields_type_check;
ALTER TABLE database_custom_fields ADD CONSTRAINT database_custom_fields_type_check CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN', 'COORDINATE'));

-- +goose Down
ALTER TABLE database_custom_fields DROP CONSTRAINT IF EXISTS database_custom_fields_type_check;
ALTER TABLE database_custom_fields ADD CONSTRAINT database_custom_fields_type_check CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN'));
