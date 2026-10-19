CREATE TABLE orp_configuration_candidate (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  candidate_uuid CHAR(36) NOT NULL,
  center_id BIGINT NOT NULL,
  actor VARCHAR(128) NOT NULL,
  target_ids JSON NOT NULL,
  snapshot JSON NOT NULL,
  config_text LONGTEXT NOT NULL,
  digest CHAR(64) NOT NULL,
  baseline_version VARCHAR(128) NULL,
  baseline_digest CHAR(64) NULL,
  archived_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uk_orp_configuration_candidate_uuid (candidate_uuid),
  KEY idx_orp_configuration_candidate_center (center_id, archived_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE orp_validation_attempt (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  candidate_id BIGINT NOT NULL,
  attempt_no INT NOT NULL,
  state VARCHAR(24) NOT NULL DEFAULT 'queued',
  concurrency_limit INT NOT NULL DEFAULT 1,
  last_candidate_id BIGINT NULL,
  failure_node_id BIGINT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  finished_at DATETIME(6) NULL,
  UNIQUE KEY uk_orp_validation_attempt (candidate_id, attempt_no),
  CONSTRAINT fk_orp_validation_attempt_candidate FOREIGN KEY (candidate_id) REFERENCES orp_configuration_candidate(id),
  KEY idx_orp_validation_attempt_state (state, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE orp_validation_job (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  attempt_id BIGINT NOT NULL,
  node_id BIGINT NOT NULL,
  state VARCHAR(24) NOT NULL DEFAULT 'queued',
  lease_owner VARCHAR(128) NULL,
  lease_until DATETIME(6) NULL,
  command_text VARCHAR(512) NULL,
  output_text MEDIUMTEXT NULL,
  started_at DATETIME(6) NULL,
  finished_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uk_orp_validation_job_target (attempt_id, node_id),
  CONSTRAINT fk_orp_validation_job_attempt FOREIGN KEY (attempt_id) REFERENCES orp_validation_attempt(id),
  KEY idx_orp_validation_job_lease (state, lease_until, id),
  KEY idx_orp_validation_job_attempt (attempt_id, state, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE orp_validation_center_policy (
  center_id BIGINT NOT NULL PRIMARY KEY,
  max_inflight INT NOT NULL DEFAULT 4,
  last_candidate_id BIGINT NOT NULL DEFAULT 0,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
