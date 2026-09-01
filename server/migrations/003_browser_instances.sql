CREATE TABLE browser_instances (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    profile_id VARCHAR(255) NOT NULL,
    device_id VARCHAR(255),
    status VARCHAR(32) DEFAULT 'offline',
    last_online TIMESTAMP
);
