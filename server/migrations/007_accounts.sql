CREATE TABLE accounts (
 id BIGSERIAL PRIMARY KEY,
 workspace_id BIGINT NOT NULL,
 platform VARCHAR(50) NOT NULL,
 username VARCHAR(255),
 password TEXT,
 email VARCHAR(255),
 proxy_id BIGINT,
 browser_instance_id BIGINT,
 status VARCHAR(50),
 risk_level VARCHAR(50),
 notes TEXT,
 created_at TIMESTAMP DEFAULT NOW()
);
