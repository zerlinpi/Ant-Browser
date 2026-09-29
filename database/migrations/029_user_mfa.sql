-- v029: TOTP two-factor authentication for user accounts.
--
-- The rows belong to a user, like sessions and refresh_tokens, so they carry
-- no tenant RLS policy and only the control plane role may use them. TOTP
-- secrets are sealed by the application's envelope encryption
-- (secret_envelope is opaque, self-describing text bound to the user);
-- recovery codes and challenge tokens are stored only as SHA-256 digests.

CREATE TABLE user_mfa_factors (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_envelope TEXT NOT NULL CHECK (length(secret_envelope) BETWEEN 1 AND 8192),
    status TEXT NOT NULL CHECK (status IN ('pending', 'active')),
    -- Latest accepted TOTP time step; a code is valid only for a later step.
    last_used_step BIGINT NOT NULL DEFAULT 0 CHECK (last_used_step >= 0),
    -- Code checks since the last success. Reaching the limit sets
    -- locked_until; the count restarts once the lockout has passed.
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_mfa_factors_confirmed_check
        CHECK ((status = 'active') = (confirmed_at IS NOT NULL))
);

-- Recovery codes exist only with a factor and disappear with it.
CREATE TABLE user_mfa_recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES user_mfa_factors(user_id) ON DELETE CASCADE,
    code_hash BYTEA NOT NULL CHECK (octet_length(code_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at TIMESTAMPTZ,
    CONSTRAINT user_mfa_recovery_codes_hash_uq UNIQUE (user_id, code_hash)
);

-- The second step of a password login for an account with an active factor.
CREATE TABLE mfa_login_challenges (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    device_id TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CONSTRAINT mfa_login_challenges_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX mfa_login_challenges_user_idx ON mfa_login_challenges (user_id, expires_at);

-- 018's default privileges cover tables created by the role that ran it;
-- grant explicitly so the result does not depend on the migration role.
GRANT SELECT, INSERT, UPDATE, DELETE
    ON user_mfa_factors, user_mfa_recovery_codes, mfa_login_challenges
    TO ant_control_plane;
