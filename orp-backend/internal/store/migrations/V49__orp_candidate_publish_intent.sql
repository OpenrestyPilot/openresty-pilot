ALTER TABLE orp_configuration_candidate
  ADD COLUMN publish_on_validation_success BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN publish_strategy JSON NULL,
  ADD COLUMN publish_state VARCHAR(24) NOT NULL DEFAULT 'not_requested',
  ADD COLUMN publish_batch_id BIGINT NULL,
  ADD COLUMN publish_error TEXT NULL,
  ADD COLUMN publish_claim_until DATETIME(6) NULL,
  ADD UNIQUE KEY uk_orp_configuration_candidate_publish_batch (publish_batch_id);

ALTER TABLE orp_publish_candidate
  ADD COLUMN configuration_candidate_id BIGINT NULL,
  ADD UNIQUE KEY uk_orp_publish_candidate_configuration (configuration_candidate_id),
  ADD CONSTRAINT fk_orp_publish_candidate_configuration FOREIGN KEY (configuration_candidate_id) REFERENCES orp_configuration_candidate(id);
