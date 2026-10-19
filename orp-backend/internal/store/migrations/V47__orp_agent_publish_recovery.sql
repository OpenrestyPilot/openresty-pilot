-- Durable link from an Agent publish item to the exact task that may activate it.
-- These nullable columns preserve existing Agent task and publish history.
ALTER TABLE orp_agent_task
  ADD COLUMN publish_batch_id BIGINT NULL,
  ADD COLUMN publish_item_id BIGINT NULL,
  ADD KEY idx_orp_agent_task_publish_item (publish_item_id, operation, status);

ALTER TABLE orp_publish_item
  ADD COLUMN agent_task_id CHAR(36) NULL,
  ADD UNIQUE KEY uk_orp_publish_item_agent_task (agent_task_id);
