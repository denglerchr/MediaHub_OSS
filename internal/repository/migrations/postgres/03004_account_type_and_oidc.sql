-- Migration: Add account_type and user_oidc_identities, drop is_service_account
-- Description: Replaces is_service_account boolean with integer account_type (0=local, 1=service_account, 2=oidc)
--              and creates user_oidc_identities table for OIDC SSO provider identity matching.

-- +goose Up
ALTER TABLE users ADD COLUMN account_type SMALLINT NOT NULL DEFAULT 0;

UPDATE users SET account_type = 1 WHERE is_service_account = TRUE;

ALTER TABLE users DROP COLUMN is_service_account;

CREATE INDEX IF NOT EXISTS idx_users_account_type ON users(account_type);

CREATE TABLE IF NOT EXISTS user_oidc_identities (
    issuer VARCHAR(255) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    user_id VARCHAR(26) NOT NULL,
    created_at BIGINT NOT NULL DEFAULT CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) * 1000 AS BIGINT),
    PRIMARY KEY (issuer, subject),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_oidc_identities_user_id ON user_oidc_identities(user_id);

-- +goose Down
DROP TABLE IF EXISTS user_oidc_identities;

ALTER TABLE users ADD COLUMN is_service_account BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE users SET is_service_account = TRUE WHERE account_type = 1;

DROP INDEX IF EXISTS idx_users_account_type;

ALTER TABLE users DROP COLUMN account_type;
