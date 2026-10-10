-- One-time upgrade from original Agently MySQL schema version 40 to 42.
-- Select the existing database before executing; no application-data backfill.
-- Derived from script/mysql/schema_versioned.ddl, schema_upgrade_40.

DELIMITER $$
-- Discover installed CHECK names; they need not match the bootstrap or Skeema.
DROP PROCEDURE IF EXISTS agently_drop_check_constraints $$
CREATE PROCEDURE agently_drop_check_constraints()
BEGIN
    DECLARE check_table VARCHAR(64);
    DECLARE check_name VARCHAR(64);
    WHILE EXISTS (
        SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
        WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_TYPE = 'CHECK'
    ) DO
        SELECT TABLE_NAME, CONSTRAINT_NAME INTO check_table, check_name
        FROM information_schema.TABLE_CONSTRAINTS
        WHERE TABLE_SCHEMA = DATABASE() AND CONSTRAINT_TYPE = 'CHECK'
        ORDER BY TABLE_NAME, CONSTRAINT_NAME LIMIT 1;
        SET @agently_drop_check_sql = CONCAT(
            'ALTER TABLE `', REPLACE(check_table, '`', '``'),
            '` DROP CHECK `', REPLACE(check_name, '`', '``'), '`');
        PREPARE agently_drop_check_stmt FROM @agently_drop_check_sql;
        EXECUTE agently_drop_check_stmt;
        DEALLOCATE PREPARE agently_drop_check_stmt;
    END WHILE;
    SET @agently_drop_check_sql = NULL;
END $$

CALL agently_drop_check_constraints() $$
DROP PROCEDURE agently_drop_check_constraints $$
DELIMITER ;

ALTER TABLE conversation
  ADD COLUMN protocol_only TINYINT NOT NULL DEFAULT 0,
  ADD COLUMN protocol_thread_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN protocol_thread_id LONGBLOB NULL,
  ADD COLUMN protocol_principal LONGBLOB NULL,
  ADD COLUMN protocol_revision BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN protocol_state_json LONGBLOB NULL,
  ADD COLUMN protocol_messages_json LONGBLOB NULL;

ALTER TABLE run
  ADD COLUMN run_kind VARCHAR(16) NOT NULL DEFAULT 'execution',
  ADD COLUMN protocol_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN protocol_status VARCHAR(32) NULL,
  ADD COLUMN protocol_turn_id VARBINARY(255) NULL,
  ADD COLUMN protocol_run_id LONGBLOB NULL,
  ADD COLUMN protocol_parent_run_id LONGBLOB NULL,
  ADD COLUMN protocol_prior_run_id LONGBLOB NULL,
  ADD COLUMN protocol_client_message_id LONGBLOB NULL,
  ADD COLUMN protocol_resumed_by_run_id LONGBLOB NULL,
  ADD COLUMN protocol_source_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN protocol_initial_turn_key CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN protocol_input_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN protocol_input_json LONGBLOB NULL,
  ADD COLUMN protocol_pending_json LONGBLOB NULL,
  ADD COLUMN protocol_revision BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN protocol_last_sequence BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN protocol_lease_owner LONGBLOB NULL,
  ADD COLUMN protocol_lease_until DATETIME(6) NULL,
  ADD COLUMN protocol_lease_revision BIGINT NOT NULL DEFAULT 0;

ALTER TABLE call_payload
  ADD COLUMN run_id VARCHAR(255) NULL,
  ADD COLUMN sequence BIGINT NULL;

CREATE UNIQUE INDEX ux_conversation_protocol_thread ON conversation (protocol_thread_key);
CREATE UNIQUE INDEX ux_run_protocol_key ON run (protocol_key);
CREATE UNIQUE INDEX ux_run_protocol_source ON run (protocol_source_key);
CREATE UNIQUE INDEX ux_run_protocol_initial_turn ON run (protocol_initial_turn_key);
CREATE INDEX idx_run_protocol_scope ON run (run_kind, conversation_id, effective_user_id, protocol_status);
CREATE INDEX idx_run_protocol_turn ON run (run_kind, effective_user_id, protocol_turn_id, conversation_id, protocol_key);
CREATE INDEX idx_run_protocol_recovery ON run (run_kind, protocol_key, protocol_status);
CREATE UNIQUE INDEX ux_payload_run_sequence ON call_payload (run_id, sequence);
ALTER TABLE call_payload ADD CONSTRAINT fk_payload_protocol_run
  FOREIGN KEY (run_id) REFERENCES run(id) ON DELETE CASCADE;
-- Schema bookkeeping only; original application rows remain unchanged.
UPDATE schema_version SET version_number=42 WHERE version_number=40;
