CREATE TABLE IF NOT EXISTS orp_log_rejection (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  kafka_topic VARCHAR(249) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  kafka_partition INT NOT NULL,
  kafka_offset BIGINT NOT NULL,
  payload_bytes INT UNSIGNED NOT NULL,
  payload_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  reason VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  recorded_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_orp_log_rejection_offset (kafka_topic, kafka_partition, kafka_offset),
  KEY idx_orp_log_rejection_time (recorded_at),
  KEY idx_orp_log_rejection_reason_time (reason, recorded_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
