CREATE DATABASE IF NOT EXISTS `agently`;
USE `agently`;

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- =========================
-- conversation
-- =========================
CREATE TABLE IF NOT EXISTS conversation
(
    id                          VARCHAR(255) PRIMARY KEY,
    summary                     TEXT,
    last_activity               TIMESTAMP    NULL     DEFAULT NULL,
    usage_input_tokens          BIGINT                DEFAULT 0,
    usage_output_tokens         BIGINT                DEFAULT 0,
    usage_embedding_tokens      BIGINT                DEFAULT 0,

    created_at                  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                  TIMESTAMP    NULL     DEFAULT NULL,
    created_by_user_id          VARCHAR(255),
    agent_id                    VARCHAR(255),
    default_model_provider      TEXT,
    default_model               TEXT,
    default_model_params        TEXT,
    title                       TEXT,
    conversation_parent_id      VARCHAR(255),
    conversation_parent_turn_id VARCHAR(255),
    metadata                    TEXT,
    visibility                  VARCHAR(255) NOT NULL DEFAULT 'private',
    shareable                   TINYINT      NOT NULL DEFAULT 0 CHECK (shareable IN (0, 1)),
    status                      VARCHAR(255),

    -- scheduling annotations
    scheduled                   TINYINT      NULL CHECK (scheduled IN (0, 1)),
    schedule_id                 VARCHAR(255) NULL,
    schedule_run_id             VARCHAR(255) NULL,
    schedule_kind               VARCHAR(32)  NULL,
    schedule_timezone           VARCHAR(64)  NULL,
    schedule_cron_expr          VARCHAR(255) NULL,

    -- external task reference for A2A exposure
    external_task_ref           TEXT
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

-- =========================
-- turn
-- =========================
CREATE TABLE IF NOT EXISTS turn
(
    id                      VARCHAR(255) PRIMARY KEY,
    conversation_id         VARCHAR(255) NOT NULL,
    created_at              TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    queue_seq               BIGINT       NULL,
    status                  VARCHAR(255) NOT NULL CHECK (status IN
                                                         ('queued', 'pending', 'running', 'waiting_for_user',
                                                          'succeeded', 'failed', 'canceled')),
    error_message           TEXT,
    started_by_message_id   VARCHAR(255),
    retry_of                VARCHAR(255),
    agent_id_used           VARCHAR(255),
    agent_config_used_id    VARCHAR(255),
    model_override_provider TEXT,
    model_override          TEXT,
    model_params_override   TEXT,

    -- link to active run
    run_id                  VARCHAR(255),

    CONSTRAINT fk_turn_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_turn_conversation ON turn (conversation_id);
CREATE INDEX idx_turn_conv_status_created ON turn (conversation_id, status, created_at);
CREATE INDEX idx_turn_conv_queue_seq ON turn (conversation_id, queue_seq);

-- =========================
-- turn_queue
-- =========================
CREATE TABLE IF NOT EXISTS turn_queue
(
    id              VARCHAR(255) PRIMARY KEY,
    conversation_id VARCHAR(255) NOT NULL,
    turn_id         VARCHAR(255) NOT NULL,
    message_id      VARCHAR(255) NOT NULL,
    queue_seq       BIGINT       NOT NULL,
    status          VARCHAR(255) NOT NULL DEFAULT 'queued',
    created_at      TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP    NULL     DEFAULT NULL,

    CONSTRAINT fk_turn_queue_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_turn_queue_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE CASCADE,
    CONSTRAINT fk_turn_queue_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE UNIQUE INDEX ux_turn_queue_turn_id ON turn_queue (turn_id);
CREATE UNIQUE INDEX ux_turn_queue_message_id ON turn_queue (message_id);
CREATE INDEX idx_turn_queue_conv_status_seq ON turn_queue (conversation_id, status, queue_seq, created_at);

-- =========================
-- call_payload
-- =========================
CREATE TABLE IF NOT EXISTS call_payload
(
    id                       VARCHAR(255) PRIMARY KEY,
    tenant_id                VARCHAR(255),
    kind                     VARCHAR(255) NOT NULL CHECK (kind IN
                                                          ('model_request', 'model_response', 'provider_request',
                                                           'provider_response', 'model_stream', 'tool_request',
                                                           'tool_response', 'elicitation_request',
                                                           'elicitation_response')),
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
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_payload_tenant_kind ON call_payload (tenant_id, kind, created_at);
CREATE INDEX idx_payload_digest ON call_payload (digest);

-- =========================
-- message
-- =========================
CREATE TABLE IF NOT EXISTS `message`
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
                                                                  'open', 'summary', 'summarized', 'completed', 'error',
                                                                  'running', 'failed', 'canceled')),
    mode                   VARCHAR(255),
    role                   VARCHAR(255) NOT NULL CHECK (role IN ('system', 'user', 'assistant', 'tool', 'chain')),
    `type`                 VARCHAR(255) NOT NULL DEFAULT 'text' CHECK (`type` IN
                                                                       ('text', 'tool_op', 'control', 'task',
                                                                        'elicitation_response')),
    content                MEDIUMTEXT,
    raw_content            MEDIUMTEXT,
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
    tool_name              TEXT,
    embedding_index        LONGBLOB,

    -- new: LLM reasoning text when response included tool calls
    preamble               TEXT,
    -- new: ReAct loop iteration (grouping key)
    iteration              INT,
    -- new: message lifecycle phase
    phase                  VARCHAR(16)           DEFAULT 'final',

    CONSTRAINT fk_message_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_message_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_message_attachment_payload
        FOREIGN KEY (attachment_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_message_elicitation_payload
        FOREIGN KEY (elicitation_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE UNIQUE INDEX idx_message_turn_seq ON `message` (turn_id, sequence);
CREATE INDEX idx_msg_conv_created ON `message` (conversation_id, created_at DESC);
CREATE INDEX idx_message_iteration ON `message` (turn_id, iteration, created_at);

-- =========================
-- users
-- =========================
CREATE TABLE IF NOT EXISTS users
(
    id                 VARCHAR(255) PRIMARY KEY,
    username           VARCHAR(255) NOT NULL UNIQUE,
    display_name       VARCHAR(255),
    email              VARCHAR(255),
    provider           VARCHAR(255) NOT NULL DEFAULT 'local',
    subject            VARCHAR(255),
    hash_ip            VARCHAR(255),
    timezone           VARCHAR(64)  NOT NULL DEFAULT 'UTC',
    default_agent_ref  VARCHAR(255),
    default_model_ref  VARCHAR(255),
    default_embedder_ref VARCHAR(255),
    settings           TEXT,
    disabled           BIGINT       NOT NULL DEFAULT 0,
    created_at         TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP    NULL     DEFAULT NULL
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE UNIQUE INDEX ux_users_provider_subject ON users (provider, subject);
CREATE INDEX ix_users_hash_ip ON users (hash_ip);

-- =========================
-- model_call
-- =========================
CREATE TABLE IF NOT EXISTS model_call
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
    status                                VARCHAR(255) NOT NULL CHECK (status IN
                                                                       ('thinking', 'streaming', 'running', 'completed',
                                                                        'failed', 'canceled')),
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

    -- new: link to run execution context
    run_id                                VARCHAR(255),
    iteration                             INT,

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
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_model_call_model ON model_call (model);
CREATE INDEX idx_model_call_started_at ON model_call (started_at);
CREATE INDEX idx_model_call_run ON model_call (run_id, iteration);

-- =========================
-- tool_call
-- =========================
CREATE TABLE IF NOT EXISTS tool_call
(
    message_id          VARCHAR(255) PRIMARY KEY,
    turn_id             VARCHAR(255),
    op_id               TEXT         NOT NULL,
    attempt             BIGINT       NOT NULL DEFAULT 1,
    tool_name           VARCHAR(255) NOT NULL,
    tool_kind           VARCHAR(255) NOT NULL CHECK (tool_kind IN ('general', 'resource')),
    status              VARCHAR(255) NOT NULL CHECK (status IN
                                                      ('queued', 'running', 'waiting_for_user', 'completed', 'failed', 'skipped',
                                                       'canceled')),
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

    -- new: link to run execution context
    run_id              VARCHAR(255),
    iteration           INT,

    CONSTRAINT fk_tool_call_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE CASCADE,
    CONSTRAINT fk_tool_call_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_tool_call_req_payload
        FOREIGN KEY (request_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL,
    CONSTRAINT fk_tool_call_res_payload
        FOREIGN KEY (response_payload_id) REFERENCES call_payload (id) ON DELETE SET NULL
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE UNIQUE INDEX idx_tool_op_attempt ON tool_call (turn_id, op_id(191), attempt);
CREATE INDEX idx_tool_call_status ON tool_call (status);
CREATE INDEX idx_tool_call_name ON tool_call (tool_name);
CREATE INDEX idx_tool_call_op ON tool_call (turn_id, op_id(191));
CREATE INDEX idx_tool_call_run ON tool_call (run_id, iteration);

-- =========================
-- tool_approval_queue
-- =========================
CREATE TABLE IF NOT EXISTS tool_approval_queue
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
    status              VARCHAR(32)  NOT NULL DEFAULT 'pending' CHECK (status IN
                                                                        ('pending', 'approved', 'rejected',
                                                                         'canceled', 'executed', 'failed')),
    decision            TEXT,
    approved_by_user_id VARCHAR(255),
    approved_at         TIMESTAMP    NULL     DEFAULT NULL,
    executed_at         TIMESTAMP    NULL     DEFAULT NULL,
    error_message       TEXT,
    created_at          TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP    NULL     DEFAULT NULL,

    CONSTRAINT fk_taq_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_taq_turn
        FOREIGN KEY (turn_id) REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_taq_message
        FOREIGN KEY (message_id) REFERENCES `message` (id) ON DELETE SET NULL
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_taq_user_status_created ON tool_approval_queue (user_id, status, created_at);
CREATE INDEX idx_taq_conversation_status ON tool_approval_queue (conversation_id, status, created_at);
CREATE INDEX idx_taq_turn ON tool_approval_queue (turn_id, created_at);

-- =========================
-- schedule
-- =========================
CREATE TABLE IF NOT EXISTS schedule
(
    id               VARCHAR(255) PRIMARY KEY,
    name             VARCHAR(255) NOT NULL UNIQUE,
    description      TEXT,
    created_by_user_id VARCHAR(255),
    visibility       VARCHAR(255) NOT NULL DEFAULT 'private',
    agent_ref        VARCHAR(255) NOT NULL,
    model_override   VARCHAR(255),
    user_cred_url    TEXT,
    enabled          TINYINT      NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    start_at         TIMESTAMP    NULL     DEFAULT NULL,
    end_at           TIMESTAMP    NULL     DEFAULT NULL,
    schedule_type    VARCHAR(32)  NOT NULL DEFAULT 'cron' CHECK (schedule_type IN ('adhoc', 'cron', 'interval')),
    cron_expr        VARCHAR(255),
    interval_seconds BIGINT,
    timezone         VARCHAR(64)  NOT NULL DEFAULT 'UTC',
    timeout_seconds  INT          NOT NULL DEFAULT 0,
    task_prompt_uri  TEXT,
    task_prompt      MEDIUMTEXT,
    next_run_at      TIMESTAMP    NULL     DEFAULT NULL,
    last_run_at      TIMESTAMP    NULL     DEFAULT NULL,
    last_status      VARCHAR(32),
    last_error       TEXT,
    lease_owner      VARCHAR(255),
    lease_until      TIMESTAMP    NULL     DEFAULT NULL,
    created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP    NULL     DEFAULT NULL
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_schedule_enabled_next ON schedule (enabled, next_run_at);
CREATE INDEX idx_schedule_enabled_next_lease ON schedule (enabled, next_run_at, lease_until);

-- =========================
-- run (expanded from schedule_run)
-- =========================
CREATE TABLE IF NOT EXISTS run
(
    id                     VARCHAR(255) PRIMARY KEY,

    -- origin: what triggered this run
    turn_id                VARCHAR(255),
    schedule_id            VARCHAR(255),
    conversation_id        VARCHAR(255),
    conversation_kind      VARCHAR(32)  NOT NULL DEFAULT 'interactive',

    -- attempt tracking
    attempt                INT          NOT NULL DEFAULT 1,
    resumed_from_run_id    VARCHAR(255),

    -- status lifecycle
    status                 VARCHAR(32)  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'prechecking', 'skipped', 'queued', 'running',
                          'completed', 'succeeded', 'failed', 'interrupted', 'canceled')),
    error_code             VARCHAR(255),
    error_message          TEXT,

    -- ReAct loop state
    iteration              INT          NOT NULL DEFAULT 0,
    max_iterations         INT,
    checkpoint_response_id VARCHAR(255),
    checkpoint_message_id  VARCHAR(255),
    checkpoint_data        MEDIUMTEXT,

    -- agent & model context
    agent_id               VARCHAR(255),
    model_provider         VARCHAR(255),
    model                  VARCHAR(255),

    -- worker distribution
    worker_id              VARCHAR(255),
    worker_pid             INT,
    worker_host            VARCHAR(255),

    -- liveness & leasing
    lease_owner            VARCHAR(255),
    lease_until            TIMESTAMP    NULL     DEFAULT NULL,
    last_heartbeat_at      TIMESTAMP    NULL     DEFAULT NULL,

    -- security context (encrypted JSON blob)
    security_context       MEDIUMTEXT,
    effective_user_id      VARCHAR(255),
    auth_authority         VARCHAR(255),
    auth_audience          VARCHAR(255),
    user_cred_url          TEXT,
    heartbeat_interval_sec INT              DEFAULT 5,

    -- scheduler fields (carried from schedule_run)
    scheduled_for          TIMESTAMP    NULL     DEFAULT NULL,
    precondition_ran_at    TIMESTAMP    NULL     DEFAULT NULL,
    precondition_passed    TINYINT      NULL CHECK (precondition_passed IN (0, 1)),
    precondition_result    MEDIUMTEXT,

    -- usage (accumulated across iterations)
    usage_prompt_tokens    BIGINT                DEFAULT 0,
    usage_completion_tokens BIGINT               DEFAULT 0,
    usage_total_tokens     BIGINT                DEFAULT 0,
    usage_cost             DOUBLE                DEFAULT 0,

    -- timing
    created_at             TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             TIMESTAMP    NULL     DEFAULT NULL,
    started_at             TIMESTAMP    NULL     DEFAULT NULL,
    completed_at           TIMESTAMP    NULL     DEFAULT NULL,

    CONSTRAINT fk_run_conversation FOREIGN KEY (conversation_id)
        REFERENCES conversation (id) ON DELETE CASCADE,
    CONSTRAINT fk_run_turn FOREIGN KEY (turn_id)
        REFERENCES turn (id) ON DELETE SET NULL,
    CONSTRAINT fk_run_schedule FOREIGN KEY (schedule_id)
        REFERENCES schedule (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_run_turn ON run (turn_id, attempt);
CREATE INDEX idx_run_worker ON run (worker_id, status);
CREATE INDEX idx_run_heartbeat ON run (status, last_heartbeat_at);
CREATE INDEX idx_run_conversation ON run (conversation_id, status);
CREATE INDEX idx_run_schedule_status ON run (schedule_id, status);
CREATE UNIQUE INDEX ux_run_schedule_slot ON run (schedule_id, scheduled_for);
CREATE INDEX idx_run_pid ON run (worker_pid, worker_host, status);

-- =========================
-- user_oauth_token
-- =========================
CREATE TABLE IF NOT EXISTS user_oauth_token
(
    user_id        VARCHAR(255) NOT NULL,
    provider       VARCHAR(128) NOT NULL,
    enc_token      TEXT         NOT NULL,
    created_at     TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP    NULL     DEFAULT NULL,
    version        BIGINT       NOT NULL DEFAULT 0,
    lease_owner    VARCHAR(255) NULL     DEFAULT NULL,
    lease_until    TIMESTAMP    NULL     DEFAULT NULL,
    refresh_status VARCHAR(32)  NOT NULL DEFAULT 'idle',
    PRIMARY KEY (user_id, provider),
    CONSTRAINT fk_uot_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

-- =========================
-- generated_file
-- =========================
CREATE TABLE IF NOT EXISTS generated_file
(
    id               VARCHAR(255) PRIMARY KEY,
    conversation_id  VARCHAR(255) NOT NULL,
    turn_id          VARCHAR(255),
    message_id       VARCHAR(255),
    provider         VARCHAR(255) NOT NULL,
    mode             VARCHAR(255) NOT NULL,
    copy_mode        VARCHAR(255) NOT NULL,
    status           VARCHAR(255) NOT NULL DEFAULT 'ready',
    payload_id       VARCHAR(255),
    container_id     VARCHAR(255),
    provider_file_id VARCHAR(255),
    filename         VARCHAR(255),
    mime_type        VARCHAR(255),
    size_bytes       BIGINT,
    checksum         VARCHAR(255),
    error_message    TEXT,
    dedup_key        VARCHAR(255),
    expires_at       TIMESTAMP    NULL     DEFAULT NULL,
    created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_gf_conversation FOREIGN KEY (conversation_id)
        REFERENCES conversation (id) ON DELETE CASCADE
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

CREATE INDEX idx_gf_conversation ON generated_file (conversation_id);
CREATE INDEX idx_gf_dedup ON generated_file (dedup_key);

-- =========================
-- workspace_resources
-- =========================
CREATE TABLE IF NOT EXISTS workspace_resources
(
    kind       VARCHAR(255) NOT NULL,
    name       VARCHAR(255) NOT NULL,
    data       MEDIUMBLOB   NOT NULL,
    updated_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (kind, name)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;

SET FOREIGN_KEY_CHECKS = 1;
