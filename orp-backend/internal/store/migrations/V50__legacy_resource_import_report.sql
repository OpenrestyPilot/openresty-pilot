CREATE TABLE legacy_resource_import_report (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  run_id BINARY(16) NOT NULL,
  resource_class VARCHAR(100) NOT NULL,
  status VARCHAR(16) NOT NULL,
  imported_count INT UNSIGNED NOT NULL DEFAULT 0,
  blocked_reason TEXT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  CONSTRAINT ck_legacy_import_report_status CHECK (status IN ('imported', 'blocked')),
  CONSTRAINT uk_legacy_import_report_run_class UNIQUE (run_id, resource_class),
  KEY idx_legacy_import_report_created (created_at),
  KEY idx_legacy_import_report_class_created (resource_class, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
