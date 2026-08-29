CREATE DATABASE IF NOT EXISTS `agently`
    CHARACTER SET utf8mb4
    COLLATE utf8mb4_0900_ai_ci;
USE `agently`;


DELIMITER $$
-- Schema versioning helpers (added)
-- Ensure schema_version table exists and initialize to version 0 if empty
CREATE TABLE IF NOT EXISTS schema_version
(
    version_number int UNSIGNED NOT NULL
) $$


DROP FUNCTION IF EXISTS get_schema_version $$

CREATE FUNCTION get_schema_version()
    RETURNS int
    READS SQL DATA
BEGIN
    DECLARE result int DEFAULT 1;
SELECT COALESCE(MAX(version_number), 1) INTO result FROM schema_version;
RETURN result;
END $$

DROP PROCEDURE IF EXISTS set_schema_version $$

CREATE PROCEDURE set_schema_version(version int)
BEGIN
DELETE FROM schema_version;
INSERT INTO schema_version VALUES (version);
END $$


DROP PROCEDURE IF EXISTS schema_upgrade_1 $$
CREATE PROCEDURE schema_upgrade_1()
BEGIN
    IF get_schema_version() = 1
    THEN

-- MySQL 8 schema (no JSON; types close to original SQLite)
-- Engine/charset
SET NAMES utf8mb4;

SET FOREIGN_KEY_CHECKS = 0;

DROP TABLE IF EXISTS model_call;
DROP TABLE IF EXISTS tool_call;
DROP TABLE IF EXISTS generated_file;
DROP TABLE IF EXISTS call_payload;
DROP TABLE IF EXISTS `message`;
DROP TABLE IF EXISTS turn;
DROP TABLE IF EXISTS conversation;

SET FOREIGN_KEY_CHECKS = 1;

-- =========================
-- conversation
-- =========================
CREATE TABLE conversation
(
    id                     VARCHAR(255) PRIMARY KEY,
    -- legacy-friendly columns
    summary                TEXT,
    last_activity          TIMESTAMP    NULL     DEFAULT NULL,
    usage_input_tokens     BIGINT                DEFAULT 0,
    usage_output_tokens    BIGINT                DEFAULT 0,
    usage_embedding_tokens BIGINT                DEFAULT 0,

    -- v2 columns
    created_at             TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP    NULL     DEFAULT NULL,
    created_by_user_id     VARCHAR(255),
    agent_id               VARCHAR(255),
    default_model_provider TEXT,
    default_model          TEXT,
    default_model_params   TEXT,
    title                  TEXT,
    conversation_parent_id       VARCHAR(255),
    conversation_parent_turn_id  VARCHAR(255),
    metadata               TEXT,
    visibility             VARCHAR(255) NOT NULL DEFAULT 'private',
    status                 VARCHAR(255),

    -- scheduling annotations
    scheduled              TINYINT      NULL CHECK (scheduled IN (0,1)),
    schedule_id            VARCHAR(255) NULL,
    schedule_run_id        VARCHAR(255) NULL,
    schedule_kind          VARCHAR(32)  NULL,
    schedule_timezone      VARCHAR(64)  NULL,
    schedule_cron_expr     VARCHAR(255) NULL,
    -- external task reference for A2A exposure
    external_task_ref      TEXT
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

-- Optional usage breakdown table (kept for compatibility)
CREATE TABLE turn
(
    id                      VARCHAR(255) PRIMARY KEY,
    conversation_id         VARCHAR(255) NOT NULL,
    created_at              TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status                  VARCHAR(255) NOT NULL CHECK (status IN
                                                         ('pending', 'running', 'waiting_for_user', 'succeeded',
                                                          'failed', 'canceled')),
    error_message           TEXT,
    started_by_message_id   VARCHAR(255),
    retry_of                VARCHAR(255),
    agent_id_used           VARCHAR(255),
    agent_config_used_id    VARCHAR(255),
    model_override_provider TEXT,
    model_override          TEXT,
    model_params_override   TEXT,

    CONSTRAINT fk_turn_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE
);

CREATE INDEX idx_turn_conversation ON turn (conversation_id);



CREATE TABLE call_payload
(
    id                       VARCHAR(255) PRIMARY KEY,
    tenant_id                VARCHAR(255),
    kind                     VARCHAR(255) NOT NULL CHECK (kind IN
                                                          ('model_request', 'model_response', 'provider_request',
                                                           'provider_response', 'model_stream', 'tool_request',
                                                           'tool_response', 'elicitation_request',
                                                           'elicitation_response', 'attachment')),
    subtype                  TEXT,
    mime_type                TEXT         NOT NULL,
    size_bytes               BIGINT       NOT NULL,
    digest                   VARCHAR(255),
    storage                  VARCHAR(255) NOT NULL CHECK (storage IN ('inline', 'object')),
    inline_body              LONGBLOB,
    uri                      TEXT,
    compression              VARCHAR(255) NOT NULL DEFAULT 'none' CHECK (compression IN ('none', 'gzip', 'zstd')),
    encryption_kms_key_id    TEXT,
    redaction_policy_version TEXT,
    redacted                 BIGINT       NOT NULL DEFAULT 0 CHECK (redacted IN (0, 1)),
    created_at               TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    schema_ref               TEXT,

    CHECK (
        (storage = 'inline' AND inline_body IS NOT NULL) OR
        (storage = 'object' AND inline_body IS NULL)
        )
);

CREATE INDEX idx_payload_tenant_kind ON call_payload (tenant_id, kind, created_at);
CREATE INDEX idx_payload_digest ON call_payload (digest);
CREATE INDEX idx_call_payload_created_at ON call_payload (created_at);


CREATE TABLE `message`
(
    id                     VARCHAR(255) PRIMARY KEY,
    conversation_id        VARCHAR(255) NOT NULL,
    turn_id                VARCHAR(255),
    archived               TINYINT      NULL,
    sequence               BIGINT,
    created_at             TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP,
    created_by_user_id     VARCHAR(255),
    status                 VARCHAR(255) CHECK (status IS NULL OR status IN
                                                                 ('', 'pending', 'accepted', 'rejected', 'cancel',
                                                                  'open', 'summary', 'summarized','completed','error',
                                                                  'running', 'failed', 'canceled', 'in_progress')),
    mode                   VARCHAR(255),
    role                   VARCHAR(255) NOT NULL CHECK (role IN ('system', 'user', 'assistant', 'tool', 'chain')),
    `type`                 VARCHAR(255) NOT NULL DEFAULT 'text' CHECK (`type` IN ('text', 'tool_op', 'control', 'elicitation_request', 'elicitation_response')),
    content                MEDIUMTEXT,
    summary                TEXT,
    context_summary        TEXT,
    tags                   TEXT,
    interim                BIGINT       NOT NULL DEFAULT 0 CHECK (interim IN (0, 1)),
    elicitation_id         VARCHAR(255),
    parent_message_id      VARCHAR(255),
    superseded_by          VARCHAR(255),
    linked_conversation_id VARCHAR(255),
    attachment_payload_id  VARCHAR(255),
    elicitation_payload_id VARCHAR(255),
    -- legacy column to remain compatible with older readers
    tool_name              TEXT,
    embedding_index        LONGBLOB,

    CONSTRAINT fk_message_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_message_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_message_attachment_payload
        FOREIGN KEY (attachment_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_message_elicitation_payload
        FOREIGN KEY (elicitation_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX idx_message_turn_seq ON `message` (turn_id, sequence);
CREATE INDEX idx_msg_conv_created ON `message` (conversation_id, created_at DESC);
CREATE INDEX idx_message_parent_seq_created ON `message` (parent_message_id, sequence, created_at);

CREATE TABLE generated_file
(
    id               VARCHAR(255) PRIMARY KEY,
    conversation_id  VARCHAR(255) NOT NULL,
    turn_id          VARCHAR(255) NULL,
    message_id       VARCHAR(255) NULL,
    provider         VARCHAR(255) NOT NULL,
    mode             VARCHAR(32)  NOT NULL CHECK (mode IN ('interpreter', 'inline', 'tool')),
    copy_mode        VARCHAR(32)  NOT NULL CHECK (copy_mode IN ('eager', 'lazy', 'lazy_cache')),
    status           VARCHAR(32)  NOT NULL DEFAULT 'ready' CHECK (status IN ('pending', 'ready', 'materializing', 'expired', 'failed')),
    payload_id       VARCHAR(255) NULL,
    container_id     VARCHAR(255) NULL,
    provider_file_id VARCHAR(255) NULL,
    filename         TEXT,
    mime_type        VARCHAR(255) NULL,
    size_bytes       BIGINT       NULL,
    checksum         VARCHAR(255) NULL,
    error_message    TEXT,
    expires_at       TIMESTAMP    NULL DEFAULT NULL,
    created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP    NULL DEFAULT NULL,

    CONSTRAINT fk_generated_file_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_generated_file_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_generated_file_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE SET NULL,
    CONSTRAINT fk_generated_file_payload
        FOREIGN KEY (payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
CREATE INDEX idx_generated_file_conversation_created ON generated_file (conversation_id, created_at);
CREATE INDEX idx_generated_file_message ON generated_file (message_id);
CREATE INDEX idx_generated_file_provider_ref ON generated_file (provider, container_id, provider_file_id);

-- Users table for identity and schedule UX state
CREATE TABLE users (
                       id                                   VARCHAR(255) PRIMARY KEY,
                       username                             VARCHAR(255) NOT NULL UNIQUE,
                       display_name                         VARCHAR(255),
                       email                                VARCHAR(255),
                       provider                             VARCHAR(255) NOT NULL DEFAULT 'local',
                       subject                              VARCHAR(255),
                       hash_ip                              VARCHAR(255),
                       timezone                             VARCHAR(64)  NOT NULL DEFAULT 'UTC',
                       default_agent_ref                    VARCHAR(255),
                       default_model_ref                    VARCHAR(255),
                       default_embedder_ref                 VARCHAR(255),
                       settings                             TEXT,
                       disabled                             BIGINT       NOT NULL DEFAULT 0,
                       created_at                           TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                       updated_at                           TIMESTAMP    NULL DEFAULT NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Unique subject per provider (NULL subject allowed for local)
CREATE UNIQUE INDEX ux_users_provider_subject ON users(provider, subject);
CREATE INDEX ix_users_hash_ip ON users(hash_ip);

CREATE TABLE model_call
(
    message_id                            VARCHAR(255) PRIMARY KEY,
    turn_id                               VARCHAR(255),
    provider                              TEXT         NOT NULL,
    model                                 VARCHAR(255) NOT NULL,
    model_kind                            VARCHAR(255) NOT NULL CHECK (model_kind IN
                                                                       ('chat', 'completion', 'vision', 'reranker',
                                                                        'embedding', 'other')),


    error_code                            TEXT,
    error_message                         TEXT,
    finish_reason                         TEXT,
    prompt_tokens                         BIGINT,
    prompt_cached_tokens                  BIGINT,
    completion_tokens                     BIGINT,
    total_tokens                          BIGINT,
    prompt_audio_tokens                   BIGINT,
    completion_reasoning_tokens           BIGINT,
    completion_audio_tokens               BIGINT,
    completion_accepted_prediction_tokens BIGINT,
    completion_rejected_prediction_tokens BIGINT,
    status                                VARCHAR(255) NOT NULL CHECK (status IN ('thinking', 'streaming','running', 'completed', 'failed', 'canceled')),
    started_at                            TIMESTAMP    NULL     DEFAULT NULL,
    completed_at                          TIMESTAMP    NULL     DEFAULT NULL,
    latency_ms                            BIGINT,
    cost                                  DOUBLE,

    trace_id                              TEXT,
    span_id                               TEXT,

    request_payload_id                    VARCHAR(255),
    response_payload_id                   VARCHAR(255),
    provider_request_payload_id           VARCHAR(255),
    provider_response_payload_id          VARCHAR(255),
    stream_payload_id                     VARCHAR(255),

    CONSTRAINT fk_model_calls_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE CASCADE,
    CONSTRAINT fk_model_call_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_model_call_req_payload
        FOREIGN KEY (request_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_model_call_res_payload
        FOREIGN KEY (response_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_model_call_provider_req_payload
        FOREIGN KEY (provider_request_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_model_call_provider_res_payload
        FOREIGN KEY (provider_response_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_model_call_stream_payload
        FOREIGN KEY (stream_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
);

CREATE INDEX idx_model_call_model ON model_call (model);
CREATE INDEX idx_model_call_started_at ON model_call (started_at);

CREATE TABLE tool_call
(
    message_id          VARCHAR(255) PRIMARY KEY,
    turn_id             VARCHAR(255),
    op_id               VARCHAR(255) NOT NULL,
    attempt             BIGINT       NOT NULL DEFAULT 1,
    tool_name           VARCHAR(255) NOT NULL,
    tool_kind           VARCHAR(255) NOT NULL CHECK (tool_kind IN ('general', 'resource')),
    status              VARCHAR(255) NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'skipped',
                                                                'canceled')),
    -- request_ref removed
    request_hash        TEXT,
    error_code          TEXT,
    error_message       TEXT,
    retriable           BIGINT CHECK (retriable IN (0, 1)),
    started_at          TIMESTAMP    NULL     DEFAULT NULL,
    completed_at        TIMESTAMP    NULL     DEFAULT NULL,
    latency_ms          BIGINT,
    cost                DOUBLE,
    trace_id            TEXT,
    span_id             TEXT,
    request_payload_id  VARCHAR(255),
    response_payload_id VARCHAR(255),


    CONSTRAINT fk_tool_call_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE CASCADE,
    CONSTRAINT fk_tool_call_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_tool_call_req_payload
        FOREIGN KEY (request_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_tool_call_res_payload
        FOREIGN KEY (response_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX idx_tool_op_attempt ON tool_call (turn_id, op_id, attempt);
CREATE INDEX idx_tool_call_status ON tool_call (status);
CREATE INDEX idx_tool_call_name ON tool_call (tool_name);
CREATE INDEX idx_tool_call_op ON tool_call (turn_id, op_id);
CREATE INDEX idx_tool_call_trace ON tool_call (trace_id(191));


	CREATE TABLE IF NOT EXISTS schedule (
	                                        id                    VARCHAR(255) PRIMARY KEY,
	    name                  VARCHAR(255) NOT NULL UNIQUE,
	    description           TEXT,
	    created_by_user_id    VARCHAR(255),
	    visibility            VARCHAR(255) NOT NULL DEFAULT 'private',
	    internal              TINYINT      NOT NULL DEFAULT 0 CHECK (internal IN (0,1)),
	    conversation_id       VARCHAR(255),
	    goal_id               VARCHAR(255),

	    -- Target agent / model
	    agent_ref             VARCHAR(255) NOT NULL,
	    model_override        VARCHAR(255),
	    user_cred_url         TEXT,

    -- Enable/disable + time window
    enabled               TINYINT      NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    start_at              TIMESTAMP    NULL DEFAULT NULL,
    end_at                TIMESTAMP    NULL DEFAULT NULL,

    -- Frequency
    schedule_type         VARCHAR(32)  NOT NULL DEFAULT 'cron' CHECK (schedule_type IN ('adhoc','cron','interval')),
    cron_expr             VARCHAR(255),
    interval_seconds      BIGINT,
    timezone              VARCHAR(64)  NOT NULL DEFAULT 'UTC',

    -- Task payload (predefined user task)
    task_prompt_uri       TEXT,
    task_prompt           MEDIUMTEXT,

    -- Optional orchestration workflow (reserved)

    -- Bookkeeping
    next_run_at           TIMESTAMP    NULL DEFAULT NULL,
    last_run_at           TIMESTAMP    NULL DEFAULT NULL,
    last_status           VARCHAR(32),
    last_error            TEXT,
    created_at            TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TIMESTAMP    NULL DEFAULT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX idx_schedule_enabled_next ON schedule(enabled, next_run_at);

-- Per-run audit trail
CREATE TABLE IF NOT EXISTS schedule_run (
                                            id                     VARCHAR(255) PRIMARY KEY,
    schedule_id            VARCHAR(255) NOT NULL,
    created_at             TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP    NULL DEFAULT NULL,
    status                 VARCHAR(32)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','prechecking','skipped','running','succeeded','failed')),
    error_message          TEXT,

    precondition_ran_at    TIMESTAMP    NULL DEFAULT NULL,
    precondition_passed    TINYINT      NULL CHECK (precondition_passed IN (0,1)),
    precondition_result    MEDIUMTEXT,

    conversation_id        VARCHAR(255) NULL,
    conversation_kind      VARCHAR(32)  NOT NULL DEFAULT 'scheduled' CHECK (conversation_kind IN ('scheduled','precondition')),
    started_at             TIMESTAMP    NULL DEFAULT NULL,
    completed_at           TIMESTAMP    NULL DEFAULT NULL,

    CONSTRAINT fk_run_schedule FOREIGN KEY (schedule_id) REFERENCES schedule(id) ON DELETE CASCADE,
    CONSTRAINT fk_run_conversation FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE SET NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX idx_run_schedule_status ON schedule_run(schedule_id, status);

-- OAuth tokens per user (server-side, encrypted). Stores serialized scy/auth.Token as enc_token.
CREATE TABLE IF NOT EXISTS user_oauth_token (
                                                user_id     VARCHAR(255) NOT NULL,
    provider    VARCHAR(128) NOT NULL,
    enc_token   TEXT         NOT NULL,
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP    NULL DEFAULT NULL,
    PRIMARY KEY (user_id, provider),
    CONSTRAINT fk_uot_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Sessions (server-side). Each session is tied to a user + auth provider.
CREATE TABLE IF NOT EXISTS session (
    id          VARCHAR(64)  PRIMARY KEY,
    user_id     VARCHAR(255) NOT NULL,
    provider    VARCHAR(128) NOT NULL,
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP    NULL DEFAULT NULL,
    expires_at  TIMESTAMP    NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX idx_session_user_id ON session(user_id);
CREATE INDEX idx_session_provider ON session(provider);
CREATE INDEX idx_session_expires_at ON session(expires_at);

-- Embedius upstream MySQL schema (source of truth + SCN log)
CREATE TABLE IF NOT EXISTS vec_dataset (
  dataset_id   VARCHAR(255) PRIMARY KEY,
  description  TEXT,
  source_uri   TEXT,
  last_scn     BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS vec_dataset_scn (
  dataset_id VARCHAR(255) PRIMARY KEY,
  next_scn   BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS vec_shadow_log (
  dataset_id   VARCHAR(255) NOT NULL,
  shadow_table VARCHAR(255) NOT NULL,
  scn          BIGINT NOT NULL,
  op           VARCHAR(16) NOT NULL,
  document_id  VARCHAR(512) NOT NULL,
  payload      LONGBLOB NOT NULL,
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(dataset_id, shadow_table, scn)
);

CREATE TABLE IF NOT EXISTS shadow_vec_docs (
  dataset_id       VARCHAR(255) NOT NULL,
  id               VARCHAR(512) NOT NULL,
  content          MEDIUMTEXT,
  meta             MEDIUMTEXT,
  embedding        LONGBLOB,
  embedding_model  VARCHAR(255),
  scn              BIGINT NOT NULL DEFAULT 0,
  archived         TINYINT NOT NULL DEFAULT 0,
  PRIMARY KEY(dataset_id, id)
);

CREATE TABLE IF NOT EXISTS emb_root (
  dataset_id      VARCHAR(255) PRIMARY KEY,
  source_uri      TEXT,
  description     TEXT,
  last_indexed_at TIMESTAMP NULL,
  last_scn        BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS emb_root_config (
  dataset_id     VARCHAR(255) PRIMARY KEY,
  include_globs  TEXT,
  exclude_globs  TEXT,
  max_size_bytes BIGINT NOT NULL DEFAULT 0,
  updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS emb_asset (
  dataset_id VARCHAR(255) NOT NULL,
  asset_id   VARCHAR(512) NOT NULL,
  path       TEXT NOT NULL,
  md5        VARCHAR(64) NOT NULL,
  size       BIGINT NOT NULL,
  mod_time   TIMESTAMP NOT NULL,
  scn        BIGINT NOT NULL,
  archived   TINYINT NOT NULL DEFAULT 0,
  PRIMARY KEY (dataset_id, asset_id)
);

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'emb_asset'
              AND INDEX_NAME = 'idx_emb_asset_path'
        ) THEN
            CREATE INDEX idx_emb_asset_path ON emb_asset(dataset_id, path(255));
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'emb_asset'
              AND INDEX_NAME = 'idx_emb_asset_mod'
        ) THEN
            CREATE INDEX idx_emb_asset_mod ON emb_asset(dataset_id, mod_time);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND INDEX_NAME = 'idx_shadow_vec_docs_scn'
        ) THEN
            CREATE INDEX idx_shadow_vec_docs_scn ON shadow_vec_docs(dataset_id, scn);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND INDEX_NAME = 'idx_shadow_vec_docs_archived'
        ) THEN
            CREATE INDEX idx_shadow_vec_docs_archived ON shadow_vec_docs(dataset_id, archived);
        END IF;

CALL set_schema_version(2);
END IF;
END $$

CALL schema_upgrade_1() $$
DROP PROCEDURE schema_upgrade_1 $$

DROP PROCEDURE IF EXISTS schema_upgrade_2 $$
CREATE PROCEDURE schema_upgrade_2()
BEGIN
    IF get_schema_version() = 2 THEN
        ALTER TABLE message ADD COLUMN raw_content MEDIUMTEXT AFTER content;
        CALL set_schema_version(3);
    END IF;
END $$

CALL schema_upgrade_2() $$
DROP PROCEDURE schema_upgrade_2 $$

DROP PROCEDURE IF EXISTS schema_upgrade_3 $$
CREATE PROCEDURE schema_upgrade_3()
BEGIN
    IF get_schema_version() = 3 THEN
        ALTER TABLE tool_call
            DROP INDEX idx_tool_call_op,
            DROP INDEX idx_tool_op_attempt,
            MODIFY COLUMN op_id TEXT NOT NULL,
            ADD INDEX idx_tool_call_op (turn_id, op_id(191)),
            ADD UNIQUE INDEX idx_tool_op_attempt (turn_id, op_id(191), attempt);
        CALL set_schema_version(4);
    END IF;
END $$

CALL schema_upgrade_3() $$
DROP PROCEDURE schema_upgrade_3 $$

DROP PROCEDURE IF EXISTS schema_upgrade_4 $$
CREATE PROCEDURE schema_upgrade_4()
BEGIN
    IF get_schema_version() = 4 THEN

        -- turn: queue_seq + 'queued' status + indexes
        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND COLUMN_NAME = 'queue_seq'
        ) THEN
            ALTER TABLE turn
                ADD COLUMN queue_seq BIGINT NULL AFTER created_at;
        END IF;

        -- Replace legacy CHECK constraint so 'queued' is allowed.
        IF EXISTS (
            SELECT 1
            FROM information_schema.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND CONSTRAINT_NAME = 'turn_chk_1'
              AND CONSTRAINT_TYPE = 'CHECK'
        ) THEN
            ALTER TABLE turn DROP CHECK turn_chk_1;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND CONSTRAINT_NAME = 'turn_chk_1'
              AND CONSTRAINT_TYPE = 'CHECK'
        ) THEN
            ALTER TABLE turn
                ADD CONSTRAINT turn_chk_1 CHECK (status IN
                                                 ('queued', 'pending', 'running', 'waiting_for_user',
                                                  'succeeded', 'failed', 'canceled'));
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND INDEX_NAME = 'idx_turn_conv_status_created'
        ) THEN
            CREATE INDEX idx_turn_conv_status_created ON turn (conversation_id, status, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND INDEX_NAME = 'idx_turn_conv_queue_seq'
        ) THEN
            CREATE INDEX idx_turn_conv_queue_seq ON turn (conversation_id, queue_seq);
        END IF;

        -- schedule: lease schema + index
        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'lease_owner'
        ) THEN
            ALTER TABLE schedule
                ADD COLUMN lease_owner VARCHAR(255) NULL AFTER last_error;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'lease_until'
        ) THEN
            ALTER TABLE schedule
                ADD COLUMN lease_until TIMESTAMP NULL DEFAULT NULL AFTER lease_owner;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND INDEX_NAME = 'idx_schedule_enabled_next_lease'
        ) THEN
            CREATE INDEX idx_schedule_enabled_next_lease ON schedule(enabled, next_run_at, lease_until);
        END IF;

        -- schedule_run: scheduled_for + unique index
        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule_run'
              AND COLUMN_NAME = 'scheduled_for'
        ) THEN
            ALTER TABLE schedule_run
                ADD COLUMN scheduled_for TIMESTAMP NULL DEFAULT NULL AFTER conversation_kind;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule_run'
              AND INDEX_NAME = 'ux_run_schedule_scheduled_for'
        ) THEN
            CREATE UNIQUE INDEX ux_run_schedule_scheduled_for ON schedule_run(schedule_id, scheduled_for);
        END IF;

        CALL set_schema_version(5);
    END IF;
END $$

CALL schema_upgrade_4() $$
DROP PROCEDURE schema_upgrade_4 $$

DROP PROCEDURE IF EXISTS schema_upgrade_5 $$
CREATE PROCEDURE schema_upgrade_5()
BEGIN
    IF get_schema_version() = 5 THEN
        -- Session table + indexes
        CREATE TABLE IF NOT EXISTS session (
            id          VARCHAR(64)  PRIMARY KEY,
            user_id     VARCHAR(255) NOT NULL,
            provider    VARCHAR(128) NOT NULL,
            created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at  TIMESTAMP    NULL DEFAULT NULL,
            expires_at  TIMESTAMP    NOT NULL
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'session'
              AND INDEX_NAME = 'idx_session_user_id'
        ) THEN
            CREATE INDEX idx_session_user_id ON session(user_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'session'
              AND INDEX_NAME = 'idx_session_provider'
        ) THEN
            CREATE INDEX idx_session_provider ON session(provider);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'session'
              AND INDEX_NAME = 'idx_session_expires_at'
        ) THEN
            CREATE INDEX idx_session_expires_at ON session(expires_at);
        END IF;

        CALL set_schema_version(6);
    END IF;
END $$

CALL schema_upgrade_5() $$
DROP PROCEDURE schema_upgrade_5 $$

DROP PROCEDURE IF EXISTS schema_upgrade_6 $$
CREATE PROCEDURE schema_upgrade_6()
BEGIN
    IF get_schema_version() = 6 THEN
        CREATE TABLE IF NOT EXISTS vec_dataset (
          dataset_id   VARCHAR(255) PRIMARY KEY,
          description  TEXT,
          source_uri   TEXT,
          last_scn     BIGINT NOT NULL DEFAULT 0
        );

        CREATE TABLE IF NOT EXISTS vec_dataset_scn (
          dataset_id VARCHAR(255) PRIMARY KEY,
          next_scn   BIGINT NOT NULL DEFAULT 0
        );

        CREATE TABLE IF NOT EXISTS vec_shadow_log (
          dataset_id   VARCHAR(255) NOT NULL,
          shadow_table VARCHAR(255) NOT NULL,
          scn          BIGINT NOT NULL,
          op           VARCHAR(16) NOT NULL,
          document_id  VARCHAR(512) NOT NULL,
          payload      LONGBLOB NOT NULL,
          created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
          PRIMARY KEY(dataset_id, shadow_table, scn)
        );

        CREATE TABLE IF NOT EXISTS shadow_vec_docs (
          dataset_id       VARCHAR(255) NOT NULL,
          id               VARCHAR(512) NOT NULL,
          content          MEDIUMTEXT,
          meta             MEDIUMTEXT,
          embedding        LONGBLOB,
          embedding_model  VARCHAR(255),
          scn              BIGINT NOT NULL DEFAULT 0,
          archived         TINYINT NOT NULL DEFAULT 0,
          PRIMARY KEY(dataset_id, id)
        );

        CREATE TABLE IF NOT EXISTS emb_root (
          dataset_id      VARCHAR(255) PRIMARY KEY,
          source_uri      TEXT,
          description     TEXT,
          last_indexed_at TIMESTAMP NULL,
          last_scn        BIGINT NOT NULL DEFAULT 0
        );

        CREATE TABLE IF NOT EXISTS emb_root_config (
          dataset_id     VARCHAR(255) PRIMARY KEY,
          include_globs  TEXT,
          exclude_globs  TEXT,
          max_size_bytes BIGINT NOT NULL DEFAULT 0,
          updated_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        );

        CREATE TABLE IF NOT EXISTS emb_asset (
          dataset_id VARCHAR(255) NOT NULL,
          asset_id   VARCHAR(512) NOT NULL,
          path       TEXT NOT NULL,
          md5        VARCHAR(64) NOT NULL,
          size       BIGINT NOT NULL,
          mod_time   TIMESTAMP NOT NULL,
          scn        BIGINT NOT NULL,
          archived   TINYINT NOT NULL DEFAULT 0,
          PRIMARY KEY (dataset_id, asset_id)
        );

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'emb_asset'
              AND INDEX_NAME = 'idx_emb_asset_path'
        ) THEN
            CREATE INDEX idx_emb_asset_path ON emb_asset(dataset_id, path(255));
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'emb_asset'
              AND INDEX_NAME = 'idx_emb_asset_mod'
        ) THEN
            CREATE INDEX idx_emb_asset_mod ON emb_asset(dataset_id, mod_time);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND INDEX_NAME = 'idx_shadow_vec_docs_scn'
        ) THEN
            CREATE INDEX idx_shadow_vec_docs_scn ON shadow_vec_docs(dataset_id, scn);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND INDEX_NAME = 'idx_shadow_vec_docs_archived'
        ) THEN
            CREATE INDEX idx_shadow_vec_docs_archived ON shadow_vec_docs(dataset_id, archived);
        END IF;

        CALL set_schema_version(7);
    END IF;
END $$

CALL schema_upgrade_6() $$
DROP PROCEDURE schema_upgrade_6 $$

DROP PROCEDURE IF EXISTS schema_upgrade_7 $$
CREATE PROCEDURE schema_upgrade_7()
BEGIN
    IF get_schema_version() = 7 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
        ) THEN
            CREATE TABLE IF NOT EXISTS `investigation` (
                id          VARCHAR(255) PRIMARY KEY,
                title       TEXT NULL,
                created_by  VARCHAR(255) NULL,
                summary     TEXT NULL,
                ad_order_id INT NULL,
                verdict     VARCHAR(255) NULL,
                created     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
            ) ENGINE=InnoDB
              DEFAULT CHARSET=utf8mb4
              COLLATE=utf8mb4_0900_ai_ci;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
              AND INDEX_NAME = 'idx_investigation_title'
        ) THEN
            CREATE FULLTEXT INDEX idx_investigation_title ON investigation (title);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
              AND INDEX_NAME = 'idx_investigation_created_by'
        ) THEN
            CREATE INDEX idx_investigation_created_by ON investigation (created_by);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
              AND INDEX_NAME = 'idx_investigation_created'
        ) THEN
            CREATE INDEX idx_investigation_created ON investigation (created);
        END IF;

        CALL set_schema_version(8);
    END IF;
END $$

CALL schema_upgrade_7() $$
DROP PROCEDURE schema_upgrade_7 $$


DROP PROCEDURE IF EXISTS schema_upgrade_8 $$
CREATE PROCEDURE schema_upgrade_8()
BEGIN
    IF get_schema_version() = 8 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'timeout_seconds'
        ) THEN
ALTER TABLE schedule
    ADD COLUMN timeout_seconds BIGINT NOT NULL DEFAULT 0 AFTER timezone;
END IF;

CALL set_schema_version(9);
END IF;
END $$

CALL schema_upgrade_8() $$
DROP PROCEDURE schema_upgrade_8 $$

DROP PROCEDURE IF EXISTS schema_upgrade_9 $$
CREATE PROCEDURE schema_upgrade_9()
BEGIN
    IF get_schema_version() = 9 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'user_cred_url'
        ) THEN
            ALTER TABLE schedule
                ADD COLUMN user_cred_url TEXT AFTER model_override;
        END IF;

        CALL set_schema_version(10);
    END IF;
END $$

CALL schema_upgrade_9() $$
DROP PROCEDURE schema_upgrade_9 $$

DROP PROCEDURE IF EXISTS schema_upgrade_10 $$
CREATE PROCEDURE schema_upgrade_10()
BEGIN
    IF get_schema_version() = 10 THEN
        ALTER TABLE `message`
            MODIFY COLUMN `type` VARCHAR(255) NOT NULL DEFAULT 'text'
                CHECK (`type` IN ('text', 'tool_op', 'control', 'elicitation_request', 'elicitation_response'));

        CALL set_schema_version(11);
    END IF;
END $$

CALL schema_upgrade_10() $$
DROP PROCEDURE schema_upgrade_10 $$

DROP PROCEDURE IF EXISTS schema_upgrade_11 $$
CREATE PROCEDURE schema_upgrade_11()
BEGIN
    IF get_schema_version() = 11 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule_run'
              AND COLUMN_NAME = 'lease_owner'
        ) THEN
            ALTER TABLE schedule_run
                ADD COLUMN lease_owner VARCHAR(255) NULL AFTER error_message;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule_run'
              AND COLUMN_NAME = 'lease_until'
        ) THEN
            ALTER TABLE schedule_run
                ADD COLUMN lease_until TIMESTAMP NULL DEFAULT NULL AFTER lease_owner;
        END IF;

        CALL set_schema_version(12);
    END IF;
END $$

CALL schema_upgrade_11() $$
DROP PROCEDURE schema_upgrade_11 $$

DROP PROCEDURE IF EXISTS schema_upgrade_12 $$
CREATE PROCEDURE schema_upgrade_12()
BEGIN
    IF get_schema_version() = 12 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'created_by_user_id'
        ) THEN
            ALTER TABLE schedule
                ADD COLUMN created_by_user_id VARCHAR(255) NULL AFTER description;
        END IF;

        CALL set_schema_version(13);
    END IF;
END $$

CALL schema_upgrade_12() $$
DROP PROCEDURE schema_upgrade_12 $$

DROP PROCEDURE IF EXISTS schema_upgrade_13 $$
CREATE PROCEDURE schema_upgrade_13()
BEGIN
    IF get_schema_version() = 13 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
              AND COLUMN_NAME = 'visibility'
        ) THEN
            ALTER TABLE schedule
                ADD COLUMN visibility VARCHAR(255) NOT NULL DEFAULT 'private' AFTER created_by_user_id;
        END IF;

        CALL set_schema_version(14);
    END IF;
END $$

CALL schema_upgrade_13() $$
DROP PROCEDURE schema_upgrade_13 $$


DROP PROCEDURE IF EXISTS schema_upgrade_14 $$
CREATE PROCEDURE schema_upgrade_14()
BEGIN
    IF get_schema_version() = 14 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
              AND COLUMN_NAME = 'conversation_id'
        ) THEN
ALTER TABLE investigation
    ADD COLUMN conversation_id VARCHAR(255) NULL AFTER created_by;
END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.REFERENTIAL_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND CONSTRAINT_NAME = 'fk_investigation_conversation'
        ) THEN
ALTER TABLE investigation
    ADD CONSTRAINT fk_investigation_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE SET NULL;
END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'investigation'
              AND INDEX_NAME = 'idx_investigation_conversation_id'
        ) THEN
CREATE INDEX idx_investigation_conversation_id ON investigation (conversation_id);
END IF;

CALL set_schema_version(15);
END IF;
END $$

CALL schema_upgrade_14() $$
DROP PROCEDURE schema_upgrade_14 $$


DROP PROCEDURE IF EXISTS schema_upgrade_15 $$
CREATE PROCEDURE schema_upgrade_15()
BEGIN
    IF get_schema_version() = 15 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'conversation'
              AND COLUMN_NAME = 'shareable'
        ) THEN
ALTER TABLE conversation
    ADD COLUMN shareable TINYINT NOT NULL DEFAULT 0 CHECK (shareable IN (0,1)) AFTER visibility;
END IF;

CALL set_schema_version(16);
END IF;
END $$

CALL schema_upgrade_15() $$
DROP PROCEDURE schema_upgrade_15 $$

DROP PROCEDURE IF EXISTS schema_upgrade_16 $$
CREATE PROCEDURE schema_upgrade_16()
BEGIN
    IF get_schema_version() = 16 THEN
    ALTER DATABASE `agently`
        CHARACTER SET utf8mb4
        COLLATE utf8mb4_unicode_ci;

    ALTER TABLE emb_root
        CONVERT TO CHARACTER SET utf8mb4
        COLLATE utf8mb4_unicode_ci;

    ALTER TABLE emb_asset
        CONVERT TO CHARACTER SET utf8mb4
        COLLATE utf8mb4_unicode_ci;

    ALTER TABLE shadow_vec_docs
        CONVERT TO CHARACTER SET utf8mb4
        COLLATE utf8mb4_unicode_ci;

    ALTER TABLE vec_shadow_log
        CONVERT TO CHARACTER SET utf8mb4
        COLLATE utf8mb4_unicode_ci;
CALL set_schema_version(17);
    END IF;
END $$

CALL schema_upgrade_16() $$
DROP PROCEDURE schema_upgrade_16 $$

DROP PROCEDURE IF EXISTS schema_upgrade_17 $$
CREATE PROCEDURE schema_upgrade_17()
BEGIN
    IF get_schema_version() = 17 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND COLUMN_NAME = 'asset_id'
        ) THEN
            ALTER TABLE shadow_vec_docs
                ADD COLUMN asset_id VARCHAR(512) NOT NULL AFTER id;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'shadow_vec_docs'
              AND INDEX_NAME = 'idx_shadow_vec_docs_asset'
        ) THEN
            CREATE INDEX idx_shadow_vec_docs_asset
                ON shadow_vec_docs(dataset_id, asset_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'vec_sync_state'
        ) THEN
            CREATE TABLE vec_sync_state (
                dataset_id   VARCHAR(255) NOT NULL,
                shadow_table VARCHAR(255) NOT NULL,
                last_scn     BIGINT NOT NULL DEFAULT 0,
                updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                PRIMARY KEY(dataset_id, shadow_table)
            );
        END IF;

        CALL set_schema_version(18);
    END IF;
END $$

CALL schema_upgrade_17() $$
DROP PROCEDURE schema_upgrade_17 $$

DROP PROCEDURE IF EXISTS schema_upgrade_18 $$
CREATE PROCEDURE schema_upgrade_18()
BEGIN
    IF get_schema_version() = 18 THEN

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
        ) THEN
            CREATE TABLE generated_file
            (
                id               VARCHAR(255) PRIMARY KEY,
                conversation_id  VARCHAR(255) NOT NULL,
                turn_id          VARCHAR(255) NULL,
                message_id       VARCHAR(255) NULL,
                provider         VARCHAR(255) NOT NULL,
                mode             VARCHAR(32)  NOT NULL CHECK (mode IN ('interpreter', 'inline', 'tool')),
                copy_mode        VARCHAR(32)  NOT NULL CHECK (copy_mode IN ('eager', 'lazy', 'lazy_cache')),
                status           VARCHAR(32)  NOT NULL DEFAULT 'ready' CHECK (status IN ('pending', 'ready', 'materializing', 'expired', 'failed')),
                payload_id       VARCHAR(255) NULL,
                container_id     VARCHAR(255) NULL,
                provider_file_id VARCHAR(255) NULL,
                filename         TEXT,
                mime_type        VARCHAR(255) NULL,
                size_bytes       BIGINT       NULL,
                checksum         VARCHAR(255) NULL,
                error_message    TEXT,
                expires_at       TIMESTAMP    NULL DEFAULT NULL,
                created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at       TIMESTAMP    NULL DEFAULT NULL,

                CONSTRAINT fk_generated_file_conversation
                    FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
                CONSTRAINT fk_generated_file_turn
                    FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
                CONSTRAINT fk_generated_file_message
                    FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE SET NULL,
                CONSTRAINT fk_generated_file_payload
                    FOREIGN KEY (payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
            ) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND INDEX_NAME = 'idx_generated_file_conversation_created'
        ) THEN
            CREATE INDEX idx_generated_file_conversation_created ON generated_file (conversation_id, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND INDEX_NAME = 'idx_generated_file_message'
        ) THEN
            CREATE INDEX idx_generated_file_message ON generated_file (message_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND INDEX_NAME = 'idx_generated_file_provider_ref'
        ) THEN
            CREATE INDEX idx_generated_file_provider_ref ON generated_file (provider, container_id, provider_file_id);
        END IF;

        CALL set_schema_version(19);
    END IF;
END $$

CALL schema_upgrade_18() $$
DROP PROCEDURE schema_upgrade_18 $$

DROP PROCEDURE IF EXISTS schema_upgrade_19 $$
CREATE PROCEDURE schema_upgrade_19()
BEGIN
    DECLARE has_constraint INT DEFAULT 0;

    IF get_schema_version() = 19 THEN

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
              AND COLUMN_NAME = 'run_id'
        ) THEN
            ALTER TABLE turn
                ADD COLUMN run_id VARCHAR(255) NULL AFTER model_params_override;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND COLUMN_NAME = 'preamble'
        ) THEN
            ALTER TABLE `message`
                ADD COLUMN preamble TEXT NULL AFTER embedding_index;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND COLUMN_NAME = 'iteration'
        ) THEN
            ALTER TABLE `message`
                ADD COLUMN iteration INT NULL AFTER preamble;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND COLUMN_NAME = 'phase'
        ) THEN
            ALTER TABLE `message`
                ADD COLUMN phase VARCHAR(16) NULL DEFAULT 'final' AFTER iteration;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND INDEX_NAME = 'idx_message_iteration'
        ) THEN
            CREATE INDEX idx_message_iteration ON `message` (turn_id, iteration, created_at);
        END IF;

        SELECT COUNT(*) INTO has_constraint
        FROM information_schema.TABLE_CONSTRAINTS
        WHERE CONSTRAINT_SCHEMA = DATABASE()
          AND TABLE_NAME = 'message'
          AND CONSTRAINT_NAME = 'message_chk_1'
          AND CONSTRAINT_TYPE = 'CHECK';
        IF has_constraint > 0 THEN
            ALTER TABLE `message` DROP CHECK message_chk_1;
        END IF;

        SELECT COUNT(*) INTO has_constraint
        FROM information_schema.TABLE_CONSTRAINTS
        WHERE CONSTRAINT_SCHEMA = DATABASE()
          AND TABLE_NAME = 'message'
          AND CONSTRAINT_NAME = 'message_chk_3'
          AND CONSTRAINT_TYPE = 'CHECK';
        IF has_constraint > 0 THEN
            ALTER TABLE `message` DROP CHECK message_chk_3;
        END IF;

        SELECT COUNT(*) INTO has_constraint
        FROM information_schema.TABLE_CONSTRAINTS
        WHERE CONSTRAINT_SCHEMA = DATABASE()
          AND TABLE_NAME = 'message'
          AND CONSTRAINT_NAME = 'message_chk_5'
          AND CONSTRAINT_TYPE = 'CHECK';
        IF has_constraint > 0 THEN
            ALTER TABLE `message` DROP CHECK message_chk_5;
        END IF;

        ALTER TABLE `message`
            ADD CONSTRAINT message_chk_1 CHECK ((`status` IS NULL) OR (`status` IN ('', 'pending', 'accepted', 'rejected', 'cancel', 'open', 'summary', 'summarized', 'completed', 'error', 'running', 'failed', 'canceled', 'in_progress'))),
            ADD CONSTRAINT message_chk_3 CHECK ((`type` IN ('text', 'tool_op', 'control', 'task', 'elicitation_request', 'elicitation_response')));

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'model_call'
              AND COLUMN_NAME = 'run_id'
        ) THEN
            ALTER TABLE model_call
                ADD COLUMN run_id VARCHAR(255) NULL AFTER stream_payload_id;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'model_call'
              AND COLUMN_NAME = 'iteration'
        ) THEN
            ALTER TABLE model_call
                ADD COLUMN iteration INT NULL AFTER run_id;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'model_call'
              AND INDEX_NAME = 'idx_model_call_run'
        ) THEN
            CREATE INDEX idx_model_call_run ON model_call (run_id, iteration);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_call'
              AND COLUMN_NAME = 'run_id'
        ) THEN
            ALTER TABLE tool_call
                ADD COLUMN run_id VARCHAR(255) NULL AFTER response_payload_id;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_call'
              AND COLUMN_NAME = 'iteration'
        ) THEN
            ALTER TABLE tool_call
                ADD COLUMN iteration INT NULL AFTER run_id;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_call'
              AND INDEX_NAME = 'idx_tool_call_run'
        ) THEN
            CREATE INDEX idx_tool_call_run ON tool_call (run_id, iteration);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'user_oauth_token'
              AND COLUMN_NAME = 'version'
        ) THEN
            ALTER TABLE user_oauth_token
                ADD COLUMN version BIGINT NOT NULL DEFAULT 0 AFTER updated_at;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'user_oauth_token'
              AND COLUMN_NAME = 'lease_owner'
        ) THEN
            ALTER TABLE user_oauth_token
                ADD COLUMN lease_owner VARCHAR(255) NULL DEFAULT NULL AFTER version;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'user_oauth_token'
              AND COLUMN_NAME = 'lease_until'
        ) THEN
            ALTER TABLE user_oauth_token
                ADD COLUMN lease_until TIMESTAMP NULL DEFAULT NULL AFTER lease_owner;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'user_oauth_token'
              AND COLUMN_NAME = 'refresh_status'
        ) THEN
            ALTER TABLE user_oauth_token
                ADD COLUMN refresh_status VARCHAR(32) NOT NULL DEFAULT 'idle' AFTER lease_until;
        END IF;

        IF EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND COLUMN_NAME = 'mode'
        ) THEN
            ALTER TABLE generated_file
                MODIFY COLUMN mode VARCHAR(255) NOT NULL;
        END IF;

        IF EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND COLUMN_NAME = 'copy_mode'
        ) THEN
            ALTER TABLE generated_file
                MODIFY COLUMN copy_mode VARCHAR(255) NOT NULL;
        END IF;

        IF EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND COLUMN_NAME = 'status'
        ) THEN
            ALTER TABLE generated_file
                MODIFY COLUMN status VARCHAR(255) NOT NULL DEFAULT 'ready';
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND COLUMN_NAME = 'dedup_key'
        ) THEN
            ALTER TABLE generated_file
                ADD COLUMN dedup_key VARCHAR(255) NULL AFTER error_message;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'generated_file'
              AND INDEX_NAME = 'idx_gf_dedup'
        ) THEN
            CREATE INDEX idx_gf_dedup ON generated_file (dedup_key);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn_queue'
        ) THEN
            CREATE TABLE turn_queue
            (
                id              VARCHAR(255) PRIMARY KEY,
                conversation_id VARCHAR(255) NOT NULL,
                turn_id         VARCHAR(255) NOT NULL,
                message_id      VARCHAR(255) NOT NULL,
                queue_seq       BIGINT       NOT NULL,
                status          VARCHAR(255) NOT NULL DEFAULT 'queued',
                created_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at      TIMESTAMP    NULL DEFAULT NULL,

                CONSTRAINT fk_turn_queue_conversation
                    FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
                CONSTRAINT fk_turn_queue_turn
                    FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE CASCADE,
                CONSTRAINT fk_turn_queue_message
                    FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE CASCADE
            ) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn_queue'
              AND INDEX_NAME = 'ux_turn_queue_turn_id'
        ) THEN
            CREATE UNIQUE INDEX ux_turn_queue_turn_id ON turn_queue (turn_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn_queue'
              AND INDEX_NAME = 'ux_turn_queue_message_id'
        ) THEN
            CREATE UNIQUE INDEX ux_turn_queue_message_id ON turn_queue (message_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn_queue'
              AND INDEX_NAME = 'idx_turn_queue_conv_status_seq'
        ) THEN
            CREATE INDEX idx_turn_queue_conv_status_seq ON turn_queue (conversation_id, status, queue_seq, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
        ) THEN
            CREATE TABLE tool_approval_queue
            (
                id                  VARCHAR(255) PRIMARY KEY,
                user_id             VARCHAR(255) NOT NULL,
                conversation_id     VARCHAR(255),
                turn_id             VARCHAR(255),
                message_id          VARCHAR(255),
                tool_name           VARCHAR(255) NOT NULL,
                title               TEXT,
                arguments           LONGBLOB     NOT NULL,
                metadata            LONGBLOB,
                status              VARCHAR(32)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'canceled', 'executed', 'failed', 'timed_out')),
                decision            TEXT,
                approved_by_user_id VARCHAR(255),
                approved_at         TIMESTAMP    NULL DEFAULT NULL,
                executed_at         TIMESTAMP    NULL DEFAULT NULL,
                expires_at          TIMESTAMP    NULL DEFAULT NULL,
                timed_out_at        TIMESTAMP    NULL DEFAULT NULL,
                error_message       TEXT,
                created_at          TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at          TIMESTAMP    NULL DEFAULT NULL,

                CONSTRAINT fk_taq_conversation
                    FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
                CONSTRAINT fk_taq_turn
                    FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
                CONSTRAINT fk_taq_message
                    FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE SET NULL
            ) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
              AND INDEX_NAME = 'idx_taq_user_status_created'
        ) THEN
            CREATE INDEX idx_taq_user_status_created ON tool_approval_queue (user_id, status, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
              AND INDEX_NAME = 'idx_taq_conversation_status'
        ) THEN
            CREATE INDEX idx_taq_conversation_status ON tool_approval_queue (conversation_id, status, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
              AND INDEX_NAME = 'idx_taq_turn'
        ) THEN
            CREATE INDEX idx_taq_turn ON tool_approval_queue (turn_id, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
              AND INDEX_NAME = 'idx_taq_status_expires_at'
        ) THEN
            CREATE INDEX idx_taq_status_expires_at ON tool_approval_queue (status, expires_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
        ) THEN
            CREATE TABLE run
            (
                id                      VARCHAR(255) PRIMARY KEY,
                turn_id                 VARCHAR(255),
                schedule_id             VARCHAR(255),
                conversation_id         VARCHAR(255),
                conversation_kind       VARCHAR(32)  NOT NULL DEFAULT 'interactive',
                attempt                 INT          NOT NULL DEFAULT 1,
                resumed_from_run_id     VARCHAR(255),
                status                  VARCHAR(32)  NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'prechecking', 'skipped', 'queued', 'running', 'completed', 'succeeded', 'failed', 'interrupted', 'canceled')),
                error_code              VARCHAR(255),
                error_message           TEXT,
                iteration               INT          NOT NULL DEFAULT 0,
                max_iterations          INT,
                checkpoint_response_id  VARCHAR(255),
                checkpoint_message_id   VARCHAR(255),
                checkpoint_data         MEDIUMTEXT,
                agent_id                VARCHAR(255),
                model_provider          VARCHAR(255),
                model                   VARCHAR(255),
                worker_id               VARCHAR(255),
                worker_pid              INT,
                worker_host             VARCHAR(255),
                lease_owner             VARCHAR(255),
                lease_until             TIMESTAMP    NULL DEFAULT NULL,
                last_heartbeat_at       TIMESTAMP    NULL DEFAULT NULL,
                security_context        MEDIUMTEXT,
                effective_user_id       VARCHAR(255),
                auth_authority          VARCHAR(255),
                auth_audience           VARCHAR(255),
                user_cred_url           TEXT,
                heartbeat_interval_sec  INT DEFAULT 5,
                scheduled_for           TIMESTAMP    NULL DEFAULT NULL,
                precondition_ran_at     TIMESTAMP    NULL DEFAULT NULL,
                precondition_passed     TINYINT      NULL CHECK (precondition_passed IN (0, 1)),
                precondition_result     MEDIUMTEXT,
                usage_prompt_tokens     BIGINT DEFAULT 0,
                usage_completion_tokens BIGINT DEFAULT 0,
                usage_total_tokens      BIGINT DEFAULT 0,
                usage_cost              DOUBLE DEFAULT 0,
                created_at              TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at              TIMESTAMP    NULL DEFAULT NULL,
                started_at              TIMESTAMP    NULL DEFAULT NULL,
                completed_at            TIMESTAMP    NULL DEFAULT NULL,

                CONSTRAINT fk_core_run_conversation FOREIGN KEY (conversation_id)
                    REFERENCES conversation (id) ON DELETE CASCADE,
                CONSTRAINT fk_core_run_turn FOREIGN KEY (turn_id)
                    REFERENCES turn (id) ON DELETE SET NULL,
                CONSTRAINT fk_core_run_schedule FOREIGN KEY (schedule_id)
                    REFERENCES schedule (id) ON DELETE CASCADE
            ) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_turn'
        ) THEN
            CREATE INDEX idx_run_turn ON run (turn_id, attempt);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_worker'
        ) THEN
            CREATE INDEX idx_run_worker ON run (worker_id, status);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_heartbeat'
        ) THEN
            CREATE INDEX idx_run_heartbeat ON run (status, last_heartbeat_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_conversation'
        ) THEN
            CREATE INDEX idx_run_conversation ON run (conversation_id, status);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_schedule_status'
        ) THEN
            CREATE INDEX idx_run_schedule_status ON run (schedule_id, status);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'ux_run_schedule_slot'
        ) THEN
            CREATE UNIQUE INDEX ux_run_schedule_slot ON run (schedule_id, scheduled_for);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'run'
              AND INDEX_NAME = 'idx_run_pid'
        ) THEN
            CREATE INDEX idx_run_pid ON run (worker_pid, worker_host, status);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'workspace_resources'
        ) THEN
            CREATE TABLE workspace_resources
            (
                kind       VARCHAR(255) NOT NULL,
                name       VARCHAR(255) NOT NULL,
                data       MEDIUMBLOB   NOT NULL,
                updated_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                PRIMARY KEY (kind, name)
            ) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
        END IF;

        CALL set_schema_version(20);
    END IF;
END $$

CALL schema_upgrade_19() $$
DROP PROCEDURE schema_upgrade_19 $$

DROP PROCEDURE IF EXISTS schema_upgrade_20 $$
CREATE PROCEDURE schema_upgrade_20()
BEGIN
    IF get_schema_version() = 20 THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'call_payload'
              AND INDEX_NAME = 'idx_call_payload_created_at'
        ) THEN
            CREATE INDEX idx_call_payload_created_at ON call_payload (created_at);
        END IF;

        CALL set_schema_version(21);
    END IF;
END $$

CALL schema_upgrade_20() $$
DROP PROCEDURE schema_upgrade_20 $$

DROP PROCEDURE IF EXISTS schema_upgrade_21 $$
CREATE PROCEDURE schema_upgrade_21()
BEGIN
    DECLARE has_constraint INT DEFAULT 0;

    IF get_schema_version() = 21 THEN
        SELECT COUNT(*) INTO has_constraint
        FROM information_schema.TABLE_CONSTRAINTS
        WHERE CONSTRAINT_SCHEMA = DATABASE()
          AND TABLE_NAME = 'message'
          AND CONSTRAINT_NAME = 'message_chk_1'
          AND CONSTRAINT_TYPE = 'CHECK';
        IF has_constraint > 0 THEN
            ALTER TABLE `message` DROP CHECK message_chk_1;
        END IF;

        ALTER TABLE `message`
            ADD CONSTRAINT message_chk_1 CHECK ((`status` IS NULL) OR (`status` IN ('', 'pending', 'accepted', 'rejected', 'cancel', 'open', 'summary', 'summarized', 'completed', 'error', 'running', 'failed', 'canceled', 'in_progress')));

        CALL set_schema_version(22);
    END IF;
END $$

CALL schema_upgrade_21() $$
DROP PROCEDURE schema_upgrade_21 $$

DROP PROCEDURE IF EXISTS schema_upgrade_22 $$
CREATE PROCEDURE schema_upgrade_22()
BEGIN
    IF get_schema_version() = 22 THEN
        -- deleted deprecated content
        CALL set_schema_version(23);
    END IF;
END $$

CALL schema_upgrade_22() $$
DROP PROCEDURE schema_upgrade_22 $$

DROP PROCEDURE IF EXISTS schema_upgrade_23 $$
CREATE PROCEDURE schema_upgrade_23()
BEGIN
    DECLARE taq_status_constraint VARCHAR(255) DEFAULT NULL;

    IF get_schema_version() = 23 THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_approval_queue'
        ) THEN
            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'approval_mode'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN approval_mode;
            END IF;

            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'request_payload'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN request_payload;
            END IF;

            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'approval_payload'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN approval_payload;
            END IF;

            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'result_payload'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN result_payload;
            END IF;

            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'status_detail'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN status_detail;
            END IF;

            IF EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'started_at'
            ) THEN
                ALTER TABLE tool_approval_queue
                    DROP COLUMN started_at;
            END IF;

            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'completed_at'
            ) THEN
                ALTER TABLE tool_approval_queue
                    ADD COLUMN completed_at TIMESTAMP NULL DEFAULT NULL AFTER updated_at;
            END IF;

            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'expires_at'
            ) THEN
                ALTER TABLE tool_approval_queue
                    ADD COLUMN expires_at TIMESTAMP NULL DEFAULT NULL AFTER completed_at;
            END IF;

            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND COLUMN_NAME = 'timed_out_at'
            ) THEN
                ALTER TABLE tool_approval_queue
                    ADD COLUMN timed_out_at TIMESTAMP NULL DEFAULT NULL AFTER expires_at;
            END IF;

            SELECT MAX(tc.CONSTRAINT_NAME) INTO taq_status_constraint
            FROM information_schema.TABLE_CONSTRAINTS tc
            JOIN information_schema.CHECK_CONSTRAINTS cc
              ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
             AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
            WHERE tc.CONSTRAINT_SCHEMA = DATABASE()
              AND tc.TABLE_NAME = 'tool_approval_queue'
              AND tc.CONSTRAINT_TYPE = 'CHECK'
              AND cc.CHECK_CLAUSE LIKE '%status%';
            IF taq_status_constraint IS NOT NULL THEN
                SET @drop_taq_status_check = CONCAT('ALTER TABLE tool_approval_queue DROP CHECK `', REPLACE(taq_status_constraint, '`', '``'), '`');
                PREPARE drop_taq_status_stmt FROM @drop_taq_status_check;
                EXECUTE drop_taq_status_stmt;
                DEALLOCATE PREPARE drop_taq_status_stmt;
            END IF;

            IF NOT EXISTS (
                SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
                WHERE CONSTRAINT_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND CONSTRAINT_NAME = 'tool_approval_queue_status_chk'
                  AND CONSTRAINT_TYPE = 'CHECK'
            ) THEN
                ALTER TABLE tool_approval_queue
                    ADD CONSTRAINT tool_approval_queue_status_chk CHECK (status IN ('pending', 'approved', 'rejected', 'canceled', 'executed', 'failed', 'timed_out'));
            END IF;

            IF NOT EXISTS (
                SELECT 1 FROM information_schema.STATISTICS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'tool_approval_queue'
                  AND INDEX_NAME = 'idx_taq_status_expires_at'
            ) THEN
                CREATE INDEX idx_taq_status_expires_at ON tool_approval_queue (status, expires_at);
            END IF;
        END IF;

        CALL set_schema_version(24);
    END IF;
END $$

CALL schema_upgrade_23() $$
DROP PROCEDURE schema_upgrade_23 $$

DROP PROCEDURE IF EXISTS schema_upgrade_24 $$
CREATE PROCEDURE schema_upgrade_24()
BEGIN
    IF get_schema_version() = 24 THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'goal'
        ) THEN
            CREATE TABLE goal
            (
                id                VARCHAR(255) PRIMARY KEY,
                conversation_id   VARCHAR(255) NOT NULL UNIQUE,
                objective         TEXT NOT NULL,
                status            VARCHAR(255) NOT NULL,
                status_reason     TEXT NULL,
                pause_reason      VARCHAR(255) NULL,
                controller_spec   TEXT NULL,
                token_budget      BIGINT NULL,
                tokens_used       BIGINT NOT NULL DEFAULT 0,
                time_used_seconds BIGINT NOT NULL DEFAULT 0,
                autonomous_turns_used BIGINT NOT NULL DEFAULT 0,
                consecutive_no_progress BIGINT NOT NULL DEFAULT 0,
                last_continuation_fingerprint TEXT NULL,
                created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at        TIMESTAMP NULL DEFAULT NULL,
                CONSTRAINT fk_goal_conversation
                    FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE
            ) ENGINE = InnoDB
              DEFAULT CHARSET = utf8mb4
              COLLATE = utf8mb4_0900_ai_ci;

            CREATE INDEX idx_goal_conversation ON goal (conversation_id);
        ELSE
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'goal'
                  AND COLUMN_NAME = 'status_reason'
            ) THEN
                ALTER TABLE goal
                    ADD COLUMN status_reason TEXT NULL AFTER status;
            END IF;
        END IF;

        CALL set_schema_version(25);
    END IF;
END $$

CALL schema_upgrade_24() $$
DROP PROCEDURE schema_upgrade_24 $$

DROP PROCEDURE IF EXISTS schema_upgrade_25 $$
CREATE PROCEDURE schema_upgrade_25()
BEGIN
    IF get_schema_version() = 25 THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'turn'
        ) THEN
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'turn'
                  AND COLUMN_NAME = 'origin'
            ) THEN
                ALTER TABLE turn
                    ADD COLUMN origin VARCHAR(64) NULL AFTER queue_seq;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'turn'
                  AND COLUMN_NAME = 'goal_id'
            ) THEN
                ALTER TABLE turn
                    ADD COLUMN goal_id VARCHAR(255) NULL AFTER origin;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'turn'
                  AND COLUMN_NAME = 'status_reason'
            ) THEN
                ALTER TABLE turn
                    ADD COLUMN status_reason TEXT NULL AFTER goal_id;
            END IF;
        END IF;

        CALL set_schema_version(26);
    END IF;
END $$

CALL schema_upgrade_25() $$
DROP PROCEDURE schema_upgrade_25 $$

DROP PROCEDURE IF EXISTS schema_upgrade_26 $$
CREATE PROCEDURE schema_upgrade_26()
BEGIN
    IF get_schema_version() = 26 THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'goal'
        ) THEN
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'goal'
                  AND COLUMN_NAME = 'autonomous_turns_used'
            ) THEN
                ALTER TABLE goal
                    ADD COLUMN autonomous_turns_used BIGINT NOT NULL DEFAULT 0 AFTER time_used_seconds;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'goal'
                  AND COLUMN_NAME = 'consecutive_no_progress'
            ) THEN
                ALTER TABLE goal
                    ADD COLUMN consecutive_no_progress BIGINT NOT NULL DEFAULT 0 AFTER autonomous_turns_used;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'goal'
                  AND COLUMN_NAME = 'last_continuation_fingerprint'
            ) THEN
                ALTER TABLE goal
                    ADD COLUMN last_continuation_fingerprint TEXT NULL AFTER consecutive_no_progress;
            END IF;
        END IF;

        CALL set_schema_version(27);
    END IF;
END $$

CALL schema_upgrade_26() $$
DROP PROCEDURE schema_upgrade_26 $$

DROP PROCEDURE IF EXISTS schema_upgrade_27 $$
CREATE PROCEDURE schema_upgrade_27()
BEGIN
    IF get_schema_version() = 27 THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'schedule'
        ) THEN
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'schedule'
                  AND COLUMN_NAME = 'internal'
            ) THEN
                ALTER TABLE schedule
                    ADD COLUMN internal TINYINT NOT NULL DEFAULT 0 AFTER visibility;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'schedule'
                  AND COLUMN_NAME = 'conversation_id'
            ) THEN
                ALTER TABLE schedule
                    ADD COLUMN conversation_id VARCHAR(255) NULL AFTER internal;
            END IF;
            IF NOT EXISTS (
                SELECT 1 FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = 'schedule'
                  AND COLUMN_NAME = 'goal_id'
            ) THEN
                ALTER TABLE schedule
                    ADD COLUMN goal_id VARCHAR(255) NULL AFTER conversation_id;
            END IF;
        END IF;

        CALL set_schema_version(28);
    END IF;
END $$

CALL schema_upgrade_27() $$
DROP PROCEDURE schema_upgrade_27 $$


DROP PROCEDURE IF EXISTS schema_upgrade_28 $$
CREATE PROCEDURE schema_upgrade_28()
BEGIN
    DECLARE has_constraint INT DEFAULT 0;

    IF get_schema_version() = 28 THEN
        IF EXISTS (
            SELECT 1 FROM information_schema.TABLES
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'call_payload'
        ) THEN
            SELECT COUNT(*) INTO has_constraint
            FROM information_schema.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'call_payload'
              AND CONSTRAINT_NAME = 'call_payload_chk_1'
              AND CONSTRAINT_TYPE = 'CHECK';
            IF has_constraint > 0 THEN
                ALTER TABLE call_payload DROP CHECK call_payload_chk_1;
            END IF;

            ALTER TABLE call_payload
                ADD CONSTRAINT call_payload_chk_1 CHECK ((kind IN ('model_request', 'model_response', 'provider_request', 'provider_response', 'model_stream', 'tool_request', 'tool_response', 'elicitation_request', 'elicitation_response', 'attachment')));
        END IF;

        CALL set_schema_version(29);
    END IF;
END $$

CALL schema_upgrade_28() $$
DROP PROCEDURE schema_upgrade_28 $$

DROP PROCEDURE IF EXISTS schema_upgrade_29 $$
CREATE PROCEDURE schema_upgrade_29()
BEGIN
    IF get_schema_version() = 29 THEN
CREATE TABLE IF NOT EXISTS report_shared_artifact (
                                                      artifact_id VARCHAR(255) PRIMARY KEY,
    artifact_ref TEXT NOT NULL,
    owner_id VARCHAR(255) NOT NULL,
    owner_ref TEXT NULL,
    kind VARCHAR(255) NOT NULL,
    lifecycle VARCHAR(255) NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    report_id VARCHAR(255) NULL,
    title TEXT NULL,
    source_artifact_id VARCHAR(255) NULL,
    base_artifact_ref TEXT NULL,
    policy_ref VARCHAR(255) NULL,
    document_version BIGINT NOT NULL DEFAULT 0,
    report_document_json MEDIUMBLOB NULL,
    report_spec_json MEDIUMBLOB NULL,
    compile_state_json MEDIUMBLOB NULL,
    report_fill_json MEDIUMBLOB NULL,
    report_print_json MEDIUMBLOB NULL,
    saved_view_overlay_json MEDIUMBLOB NULL,
    metadata_json MEDIUMBLOB NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NULL
    );

CREATE INDEX idx_report_shared_artifact_owner_artifact_ref
    ON report_shared_artifact(owner_id(191), artifact_ref(191));
CREATE INDEX idx_report_shared_artifact_owner_report_id
    ON report_shared_artifact(owner_id, report_id);
CREATE INDEX idx_report_shared_artifact_owner_kind_lifecycle_updated
    ON report_shared_artifact(owner_id, kind, lifecycle, updated_at, created_at);

CREATE TABLE IF NOT EXISTS report_export_job (
                                                 job_id VARCHAR(255) PRIMARY KEY,
    artifact_ref TEXT NOT NULL,
    owner_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(255) NULL,
    workspace_id VARCHAR(255) NULL,
    auth_context_ref TEXT NULL,
    format VARCHAR(64) NOT NULL,
    scope VARCHAR(64) NOT NULL,
    status VARCHAR(64) NOT NULL,
    report_spec_json MEDIUMBLOB NULL,
    report_fill_json MEDIUMBLOB NULL,
    report_print_json MEDIUMBLOB NULL,
    metadata_json MEDIUMBLOB NULL,
    artifact_id VARCHAR(255) NULL,
    error_text TEXT NULL,
    diagnostics_json MEDIUMBLOB NULL,
    submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME NULL,
    completed_at DATETIME NULL,
    retention_ttl_sec BIGINT NOT NULL DEFAULT 0,
    report_run_id VARCHAR(255) NULL,
    report_run_revision BIGINT NULL,
    export_request_id VARCHAR(128) NULL,
    CONSTRAINT ux_report_export_job_owner_conversation_request
        UNIQUE (owner_id, conversation_id, export_request_id),
    CONSTRAINT chk_report_export_job_status
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    CONSTRAINT chk_report_export_job_run_reference
        CHECK (
            (report_run_id IS NULL AND report_run_revision IS NULL AND export_request_id IS NULL)
            OR
            (
                report_run_id IS NOT NULL
                AND report_run_revision IS NOT NULL
                AND report_run_revision > 0
                AND export_request_id IS NOT NULL
                AND conversation_id IS NOT NULL
            )
        ),
    CONSTRAINT chk_report_export_job_run_pdf
        CHECK (report_run_id IS NULL OR (format = 'pdf' AND scope = 'draft'))
    );

CREATE INDEX idx_report_export_job_owner_submitted_at
    ON report_export_job(owner_id, submitted_at);
CREATE INDEX idx_report_export_job_owner_artifact_ref
    ON report_export_job(owner_id, artifact_ref(191));
CREATE INDEX idx_report_export_job_owner_status
    ON report_export_job(owner_id, status);

CREATE TABLE IF NOT EXISTS report_export_artifact (
                                                      artifact_id VARCHAR(255) PRIMARY KEY,
    job_id VARCHAR(255) NOT NULL,
    artifact_ref TEXT NOT NULL,
    owner_id VARCHAR(255) NOT NULL,
    format VARCHAR(64) NOT NULL,
    content_type VARCHAR(255) NOT NULL,
    inline_data LONGBLOB NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retention_ttl_sec BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT ux_report_export_artifact_job UNIQUE (job_id),
    CONSTRAINT fk_report_export_artifact_job
        FOREIGN KEY (job_id) REFERENCES report_export_job(job_id) ON DELETE RESTRICT
    );

CREATE INDEX idx_report_export_artifact_owner_created_at
    ON report_export_artifact(owner_id, created_at);
CREATE INDEX idx_report_export_artifact_owner_artifact_ref
    ON report_export_artifact(owner_id, artifact_ref(191));

CREATE TABLE IF NOT EXISTS report_audit_event (
                                                  event_id VARCHAR(255) PRIMARY KEY,
    event_type VARCHAR(255) NOT NULL,
    artifact_ref TEXT NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    job_id VARCHAR(255) NULL,
    artifact_id VARCHAR(255) NULL,
    actor_id VARCHAR(255) NOT NULL,
    actor_ref TEXT NULL,
    occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata_json MEDIUMBLOB NULL
    );

CREATE INDEX idx_report_audit_event_actor_occurred_at
    ON report_audit_event(actor_id, occurred_at);
CREATE INDEX idx_report_audit_event_artifact_ref
    ON report_audit_event(artifact_ref(191));

CALL set_schema_version(30);
END IF;
END $$

CALL schema_upgrade_29() $$
DROP PROCEDURE schema_upgrade_29 $$

DROP PROCEDURE IF EXISTS schema_upgrade_30 $$
CREATE PROCEDURE schema_upgrade_30()
BEGIN
    IF get_schema_version() = 30 THEN
        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND INDEX_NAME = 'idx_message_parent_seq_created'
        ) THEN
            ALTER TABLE `message`
                ADD INDEX idx_message_parent_seq_created (parent_message_id, sequence, created_at);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM information_schema.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_call'
              AND INDEX_NAME = 'idx_tool_call_trace'
        ) THEN
            ALTER TABLE tool_call
                ADD INDEX idx_tool_call_trace (trace_id(191));
        END IF;

        CALL set_schema_version(31);
    END IF;
END $$

CALL schema_upgrade_30() $$
DROP PROCEDURE schema_upgrade_30 $$

DROP PROCEDURE IF EXISTS schema_upgrade_31 $$
CREATE PROCEDURE schema_upgrade_31()
BEGIN
    IF get_schema_version() = 31 THEN
        CREATE TABLE IF NOT EXISTS report_run (
            report_run_id VARCHAR(255) PRIMARY KEY,
            owner_id VARCHAR(255) NOT NULL,
            conversation_id VARCHAR(255) COLLATE utf8mb4_0900_ai_ci NULL,
            materializer VARCHAR(64) NOT NULL,
            origin VARCHAR(64) NULL,
            builder_ref VARCHAR(255) NULL,
            preset_id VARCHAR(255) NULL,
            source_kind VARCHAR(64) NULL,
            source_id VARCHAR(255) NULL,
            requested_params_json MEDIUMBLOB NULL,
            effective_params_json MEDIUMBLOB NULL,
            status VARCHAR(32) NOT NULL,
            failure_code VARCHAR(255) NULL,
            failure_text TEXT NULL,
            started_at DATETIME NOT NULL,
            completed_at DATETIME NULL,
            revision BIGINT NOT NULL,
            ui_run_request_id VARCHAR(255) NOT NULL,
            report_spec_json MEDIUMBLOB NULL,
            report_fill_json MEDIUMBLOB NULL,
            report_print_json MEDIUMBLOB NULL,
            activation_source VARCHAR(64) NULL,
            adoption_source VARCHAR(64) NULL,
            actor_id VARCHAR(255) NULL,
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            CONSTRAINT chk_report_run_status CHECK (status IN ('running', 'completed', 'failed')),
            CONSTRAINT ux_report_run_owner_request UNIQUE (owner_id, ui_run_request_id),
            CONSTRAINT ux_report_run_owner_id UNIQUE (owner_id, report_run_id),
            KEY idx_report_run_owner_conversation_updated (owner_id, conversation_id, updated_at),
            KEY idx_report_run_owner_status_updated (owner_id, status, updated_at),
            CONSTRAINT fk_report_run_conversation
                FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE SET NULL
        );

        CREATE TABLE IF NOT EXISTS conversation_report_context (
            owner_id VARCHAR(255) NOT NULL,
            conversation_id VARCHAR(255) COLLATE utf8mb4_0900_ai_ci NOT NULL,
            active_report_run_id VARCHAR(255) NOT NULL,
            revision BIGINT NOT NULL,
            activation_source VARCHAR(64) NOT NULL DEFAULT '',
            actor_id VARCHAR(255) NOT NULL DEFAULT '',
            updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            PRIMARY KEY (owner_id, conversation_id),
            KEY idx_conversation_report_context_active_run (owner_id, active_report_run_id),
            CONSTRAINT fk_conversation_report_context_conversation
                FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE CASCADE,
            CONSTRAINT fk_conversation_report_context_run
                FOREIGN KEY (owner_id, active_report_run_id)
                REFERENCES report_run(owner_id, report_run_id) ON DELETE RESTRICT
        );

        CALL set_schema_version(32);
    END IF;
END $$

CALL schema_upgrade_31() $$
DROP PROCEDURE schema_upgrade_31 $$

DROP PROCEDURE IF EXISTS schema_upgrade_32 $$
CREATE PROCEDURE schema_upgrade_32()
BEGIN
    IF get_schema_version() = 32 THEN
        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND COLUMN_NAME = 'report_run_id'
        ) THEN
            ALTER TABLE report_export_job
                ADD COLUMN report_run_id VARCHAR(255) NULL;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND COLUMN_NAME = 'report_run_revision'
        ) THEN
            ALTER TABLE report_export_job
                ADD COLUMN report_run_revision BIGINT NULL;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.COLUMNS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND COLUMN_NAME = 'export_request_id'
        ) THEN
            ALTER TABLE report_export_job
                ADD COLUMN export_request_id VARCHAR(128) NULL;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND INDEX_NAME = 'ux_report_export_job_owner_conversation_request'
        ) THEN
            ALTER TABLE report_export_job
                ADD UNIQUE KEY ux_report_export_job_owner_conversation_request
                    (owner_id, conversation_id, export_request_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND INDEX_NAME = 'idx_report_export_job_run'
        ) THEN
            ALTER TABLE report_export_job
                ADD KEY idx_report_export_job_run (report_run_id);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND CONSTRAINT_NAME = 'fk_report_export_job_run'
        ) THEN
            ALTER TABLE report_export_job
                ADD CONSTRAINT fk_report_export_job_run
                    FOREIGN KEY (report_run_id)
                    REFERENCES report_run(report_run_id) ON DELETE RESTRICT;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND CONSTRAINT_NAME = 'chk_report_export_job_run_reference'
        ) THEN
            ALTER TABLE report_export_job
                ADD CONSTRAINT chk_report_export_job_run_reference
                    CHECK (
                        (report_run_id IS NULL AND report_run_revision IS NULL AND export_request_id IS NULL)
                        OR
                        (
                            report_run_id IS NOT NULL
                            AND report_run_revision IS NOT NULL
                            AND export_request_id IS NOT NULL
                            AND conversation_id IS NOT NULL
                            AND report_run_revision > 0
                        )
                    );
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND CONSTRAINT_NAME = 'chk_report_export_job_status'
        ) THEN
            ALTER TABLE report_export_job
                ADD CONSTRAINT chk_report_export_job_status
                    CHECK (status IN ('queued', 'running', 'succeeded', 'failed'));
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_job'
              AND CONSTRAINT_NAME = 'chk_report_export_job_run_pdf'
        ) THEN
            ALTER TABLE report_export_job
                ADD CONSTRAINT chk_report_export_job_run_pdf
                    CHECK (report_run_id IS NULL OR (format = 'pdf' AND scope = 'draft'));
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_artifact'
              AND INDEX_NAME = 'ux_report_export_artifact_job'
        ) THEN
            ALTER TABLE report_export_artifact
                ADD UNIQUE KEY ux_report_export_artifact_job (job_id);
        END IF;

        IF EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_artifact'
              AND INDEX_NAME = 'idx_report_export_artifact_job_id'
        ) AND EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_artifact'
              AND INDEX_NAME = 'ux_report_export_artifact_job'
              AND NON_UNIQUE = 0
        ) THEN
            ALTER TABLE report_export_artifact
                DROP INDEX idx_report_export_artifact_job_id;
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS
            WHERE CONSTRAINT_SCHEMA = DATABASE()
              AND TABLE_NAME = 'report_export_artifact'
              AND CONSTRAINT_NAME = 'fk_report_export_artifact_job'
        ) THEN
            ALTER TABLE report_export_artifact
                ADD CONSTRAINT fk_report_export_artifact_job
                    FOREIGN KEY (job_id)
                    REFERENCES report_export_job(job_id) ON DELETE RESTRICT;
        END IF;

        CALL set_schema_version(33);
    END IF;
END $$

CALL schema_upgrade_32() $$
DROP PROCEDURE schema_upgrade_32 $$

DROP PROCEDURE IF EXISTS schema_upgrade_33 $$
CREATE PROCEDURE schema_upgrade_33()
BEGIN
    IF get_schema_version() = 33 THEN
        CREATE TABLE IF NOT EXISTS tool_execution_claim (
            claim_key CHAR(64) NOT NULL,
            rule_id VARCHAR(255) NOT NULL,
            canonical_tool_name VARCHAR(255) NOT NULL,
            turn_id VARCHAR(255) NOT NULL,
            semantic_request_hash CHAR(64) NOT NULL,
            state VARCHAR(16) NOT NULL,
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            finished_at DATETIME NULL DEFAULT NULL,
            PRIMARY KEY (claim_key),
            CONSTRAINT chk_tool_execution_claim_state
                CHECK (state IN ('claimed', 'completed', 'failed', 'unknown')),
            KEY idx_tool_execution_claim_turn_hash (turn_id, semantic_request_hash),
            KEY idx_tool_execution_claim_rule_tool_state_updated
                (rule_id, canonical_tool_name, state, updated_at)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_execution_claim'
              AND INDEX_NAME = 'idx_tool_execution_claim_turn_hash'
        ) THEN
            ALTER TABLE tool_execution_claim
                ADD KEY idx_tool_execution_claim_turn_hash (turn_id, semantic_request_hash);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'tool_execution_claim'
              AND INDEX_NAME = 'idx_tool_execution_claim_rule_tool_state_updated'
        ) THEN
            ALTER TABLE tool_execution_claim
                ADD KEY idx_tool_execution_claim_rule_tool_state_updated
                    (rule_id, canonical_tool_name, state, updated_at);
        END IF;

        CALL set_schema_version(34);
    END IF;
END $$

CALL schema_upgrade_33() $$
DROP PROCEDURE schema_upgrade_33 $$

DROP PROCEDURE IF EXISTS schema_upgrade_34 $$
CREATE PROCEDURE schema_upgrade_34()
BEGIN
    IF get_schema_version() = 34 THEN
        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND INDEX_NAME = 'idx_message_superseded_by'
        ) THEN
            ALTER TABLE `message`
                ADD KEY idx_message_superseded_by (superseded_by);
        END IF;

        IF NOT EXISTS (
            SELECT 1 FROM INFORMATION_SCHEMA.STATISTICS
            WHERE TABLE_SCHEMA = DATABASE()
              AND TABLE_NAME = 'message'
              AND INDEX_NAME = 'idx_message_linked_conversation'
        ) THEN
            ALTER TABLE `message`
                ADD KEY idx_message_linked_conversation (linked_conversation_id);
        END IF;

        CALL set_schema_version(35);
    END IF;
END $$

CALL schema_upgrade_34() $$
DROP PROCEDURE schema_upgrade_34 $$

DROP PROCEDURE IF EXISTS schema_upgrade_35 $$
CREATE PROCEDURE schema_upgrade_35()
BEGIN
    IF get_schema_version() = 35 THEN
        CREATE TABLE IF NOT EXISTS oauth_link_state (
            state_hash   VARCHAR(64)  NOT NULL,
            flow_hash    VARCHAR(64)  NOT NULL,
            user_id      VARCHAR(255) NOT NULL,
            session_hash VARCHAR(64)  NOT NULL,
            provider     VARCHAR(128) NOT NULL,
            expires_at   DATETIME     NOT NULL,
            consumed_at  DATETIME     NULL DEFAULT NULL,
            created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
            PRIMARY KEY (state_hash),
            UNIQUE KEY ux_oauth_link_state_flow (flow_hash),
            KEY ix_oauth_link_state_expires (expires_at)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

        CALL set_schema_version(36);
    END IF;
END $$

CALL schema_upgrade_35() $$
DROP PROCEDURE schema_upgrade_35 $$

DELIMITER ;
