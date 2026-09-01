CREATE TABLE workspaces (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    owner_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workspace_members (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'Viewer'
);
