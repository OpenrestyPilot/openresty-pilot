CREATE TABLE orp_agent_token (
  node_id BIGINT NOT NULL PRIMARY KEY,
  token_hash CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_orp_agent_token_hash (token_hash)
);
