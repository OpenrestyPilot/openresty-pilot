CREATE TABLE orp_configuration_candidate_diff (
  candidate_id BIGINT NOT NULL,
  node_id BIGINT NOT NULL,
  baseline_version CHAR(36) NULL,
  baseline_digest CHAR(64) NULL,
  candidate_digest CHAR(64) NOT NULL,
  semantic_diff JSON NOT NULL,
  text_diff JSON NOT NULL,
  added_lines INT NOT NULL DEFAULT 0,
  deleted_lines INT NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (candidate_id, node_id),
  CONSTRAINT fk_orp_candidate_diff_candidate FOREIGN KEY (candidate_id) REFERENCES orp_configuration_candidate(id),
  KEY idx_orp_candidate_diff_node (node_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
