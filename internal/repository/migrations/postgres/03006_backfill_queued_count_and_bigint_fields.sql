-- Migration: Backfill databases.queued_count and widen INTEGER custom fields to BIGINT
-- Description:
--   1. Recomputes databases.queued_count from each existing dynamic entries_<id> table (WHERE status = 4).
--   2. Widens existing 32-bit INTEGER custom field columns (cf_<field_id>) to 64-bit BIGINT.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    db_rec RECORD;
    cf_rec RECORD;
    entries_table TEXT;
    actual_queued BIGINT;
BEGIN
    FOR db_rec IN SELECT id, queued_count FROM databases LOOP
        entries_table := 'entries_' || db_rec.id;
        IF to_regclass(format('%I', entries_table)) IS NOT NULL THEN
            EXECUTE format('SELECT COUNT(*) FROM %I WHERE status = 4', entries_table) INTO actual_queued;
            IF db_rec.queued_count IS DISTINCT FROM actual_queued THEN
                UPDATE databases SET queued_count = actual_queued WHERE id = db_rec.id;
            END IF;
        END IF;
    END LOOP;

    FOR cf_rec IN
        SELECT cols.table_schema, cols.table_name, cols.column_name
        FROM database_custom_fields cf
        JOIN information_schema.columns cols
          ON cols.table_schema = ANY(current_schemas(false))
         AND cols.table_name = 'entries_' || cf.database_id
         AND cols.column_name = 'cf_' || cf.field_id
        WHERE cf.type = 'INTEGER'
          AND cols.data_type = 'integer'
    LOOP
        EXECUTE format(
            'ALTER TABLE %I.%I ALTER COLUMN %I TYPE BIGINT',
            cf_rec.table_schema,
            cf_rec.table_name,
            cf_rec.column_name
        );
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
