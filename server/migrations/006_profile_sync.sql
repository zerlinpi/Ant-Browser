CREATE TABLE browser_profiles (
 id SERIAL PRIMARY KEY,
 workspace_id BIGINT NOT NULL,
 name VARCHAR(255) NOT NULL,
 fingerprint_id VARCHAR(255),
 owner_id BIGINT,
 created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE profile_versions (
 id SERIAL PRIMARY KEY,
 profile_id BIGINT NOT NULL,
 version INT NOT NULL,
 hash VARCHAR(255),
 created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE profile_files (
 id SERIAL PRIMARY KEY,
 profile_id BIGINT NOT NULL,
 file_type VARCHAR(64) NOT NULL,
 storage_path TEXT,
 hash VARCHAR(255)
);

CREATE TABLE sync_records (
 id SERIAL PRIMARY KEY,
 profile_id BIGINT NOT NULL,
 action VARCHAR(32),
 status VARCHAR(32),
 created_at TIMESTAMP DEFAULT NOW()
);
