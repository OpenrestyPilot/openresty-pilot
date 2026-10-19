CREATE TABLE IF NOT EXISTS orp_api_consumer (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  description VARCHAR(500) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'enabled',
  created_by VARCHAR(128) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  UNIQUE KEY uk_orp_api_consumer_name (name),
  KEY idx_orp_api_consumer_status (status, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS orp_api_key (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  consumer_id BIGINT NOT NULL,
  key_prefix VARCHAR(24) NOT NULL,
  secret_hash BINARY(32) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  expires_at DATETIME(6) NULL,
  last_used_at DATETIME(6) NULL,
  created_by VARCHAR(128) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  revoked_by VARCHAR(128) NULL,
  revoked_at DATETIME(6) NULL,
  rotated_from_id BIGINT NULL,
  CONSTRAINT fk_orp_api_key_consumer FOREIGN KEY (consumer_id) REFERENCES orp_api_consumer(id),
  CONSTRAINT fk_orp_api_key_rotated_from FOREIGN KEY (rotated_from_id) REFERENCES orp_api_key(id),
  UNIQUE KEY uk_orp_api_key_secret_hash (secret_hash),
  KEY idx_orp_api_key_consumer (consumer_id, status, id),
  KEY idx_orp_api_key_prefix (key_prefix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
