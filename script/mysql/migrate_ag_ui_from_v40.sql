-- One-time additive upgrade from original Agently MySQL schema version 40.
-- Select the existing database before executing; no application-data backfill.
-- Derived from script/mysql/schema_versioned.ddl, schema_upgrade_40.

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

DELIMITER $$
CREATE PROCEDURE agently_agui_payload_kind_upgrade()
BEGIN
  DECLARE old_check VARCHAR(255) DEFAULT NULL;
  SELECT tc.CONSTRAINT_NAME INTO old_check
  FROM information_schema.TABLE_CONSTRAINTS tc
  JOIN information_schema.CHECK_CONSTRAINTS cc
    ON cc.CONSTRAINT_SCHEMA=tc.CONSTRAINT_SCHEMA
   AND cc.CONSTRAINT_NAME=tc.CONSTRAINT_NAME
  WHERE tc.TABLE_SCHEMA=DATABASE() AND tc.TABLE_NAME='call_payload'
    AND cc.CHECK_CLAUSE LIKE '%model_request%'
    AND cc.CHECK_CLAUSE NOT LIKE '%agui.event%'
  LIMIT 1;
  IF old_check IS NOT NULL THEN
    SET @agui_kind_sql=CONCAT('ALTER TABLE call_payload DROP CHECK `',REPLACE(old_check,'`','``'),'`');
    PREPARE agui_kind_stmt FROM @agui_kind_sql;
    EXECUTE agui_kind_stmt;
    DEALLOCATE PREPARE agui_kind_stmt;
    ALTER TABLE call_payload ADD CONSTRAINT ck_call_payload_kind
      CHECK (kind IN ('model_request','model_response','provider_request',
        'provider_response','model_stream','tool_request','tool_response',
        'elicitation_request','elicitation_response','attachment','agui.event'));
  END IF;
END $$
CALL agently_agui_payload_kind_upgrade() $$
DROP PROCEDURE agently_agui_payload_kind_upgrade $$
DELIMITER ;

CREATE UNIQUE INDEX ux_conversation_protocol_thread ON conversation (protocol_thread_key);
CREATE UNIQUE INDEX ux_run_protocol_key ON run (protocol_key);
CREATE UNIQUE INDEX ux_run_protocol_source ON run (protocol_source_key);
CREATE UNIQUE INDEX ux_run_protocol_initial_turn ON run (protocol_initial_turn_key);
CREATE INDEX ix_run_protocol_scope ON run (run_kind, conversation_id, effective_user_id, protocol_status);
CREATE INDEX ix_run_protocol_turn ON run (run_kind, effective_user_id, protocol_turn_id, conversation_id, protocol_key);
CREATE INDEX ix_run_protocol_recovery ON run (run_kind, protocol_key, protocol_status);
CREATE UNIQUE INDEX ux_payload_run_sequence ON call_payload (run_id, sequence);
ALTER TABLE call_payload ADD CONSTRAINT fk_payload_protocol_run
  FOREIGN KEY (run_id) REFERENCES run(id) ON DELETE CASCADE;
-- Schema bookkeeping only; original application rows remain unchanged.
UPDATE schema_version SET version_number=41 WHERE version_number=40;
