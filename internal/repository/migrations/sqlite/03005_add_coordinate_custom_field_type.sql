-- Migration: Add COORDINATE custom field type
-- Description: Updates CHECK constraint on database_custom_fields.type to include 'COORDINATE'

-- +goose Up
CREATE TABLE database_custom_fields_new (
    database_id VARCHAR(26) NOT NULL,
    field_id INTEGER NOT NULL CHECK(field_id >= 0 AND field_id <= 254),
    name VARCHAR(64) NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN', 'COORDINATE')),
    is_indexed BOOLEAN NOT NULL DEFAULT 1,
    PRIMARY KEY (database_id, field_id),
    FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE,
    UNIQUE (database_id, name)
);

INSERT INTO database_custom_fields_new (database_id, field_id, name, type, is_indexed)
SELECT database_id, field_id, name, type, is_indexed FROM database_custom_fields;

DROP TABLE database_custom_fields;

ALTER TABLE database_custom_fields_new RENAME TO database_custom_fields;

-- +goose Down
CREATE TABLE database_custom_fields_old (
    database_id VARCHAR(26) NOT NULL,
    field_id INTEGER NOT NULL CHECK(field_id >= 0 AND field_id <= 254),
    name VARCHAR(64) NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('TEXT', 'INTEGER', 'REAL', 'BOOLEAN')),
    is_indexed BOOLEAN NOT NULL DEFAULT 1,
    PRIMARY KEY (database_id, field_id),
    FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE,
    UNIQUE (database_id, name)
);

INSERT INTO database_custom_fields_old (database_id, field_id, name, type, is_indexed)
SELECT database_id, field_id, name, type, is_indexed FROM database_custom_fields WHERE type != 'COORDINATE';

DROP TABLE database_custom_fields;

ALTER TABLE database_custom_fields_old RENAME TO database_custom_fields;
