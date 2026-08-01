-- MySQL 8 schema (no JSON; types close to original SQLite)
-- Engine/charset
SET NAMES utf8mb4;

SET FOREIGN_KEY_CHECKS = 0;

DROP TABLE IF EXISTS tool_execution_claim;
DROP TABLE IF EXISTS conversation_report_context;
DROP TABLE IF EXISTS report_export_artifact;
DROP TABLE IF EXISTS report_audit_event;
DROP TABLE IF EXISTS report_export_job;
DROP TABLE IF EXISTS report_run;
DROP TABLE IF EXISTS report_shared_artifact;
DROP TABLE IF EXISTS model_call;
DROP TABLE IF EXISTS tool_call;
DROP TABLE IF EXISTS generated_file;
DROP TABLE IF EXISTS call_payload;
DROP TABLE IF EXISTS `message`;
DROP TABLE IF EXISTS turn;
DROP TABLE IF EXISTS goal;
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
    shareable              TINYINT NOT NULL DEFAULT 0 CHECK (shareable IN (0,1)),
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

-- Optional usage breakdown table (kept for compatibility)
CREATE TABLE turn
(
    id                      VARCHAR(255) PRIMARY KEY,
    conversation_id         VARCHAR(255) NOT NULL,
    created_at              TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- queue_seq provides deterministic FIFO ordering when created_at has low resolution.
    -- It is set by the application when queueing is enabled.
    queue_seq               BIGINT NULL,
    origin                  VARCHAR(64) NULL,
    goal_id                 VARCHAR(255) NULL,
    status_reason           TEXT NULL,
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

    CONSTRAINT fk_turn_conversation
        FOREIGN KEY (conversation_id) REFERENCES conversation (id) ON DELETE CASCADE
);

CREATE INDEX idx_turn_conversation ON turn (conversation_id);
CREATE INDEX idx_turn_conv_status_created ON turn (conversation_id, status, created_at);
CREATE INDEX idx_turn_conv_queue_seq ON turn (conversation_id, queue_seq);



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
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_0900_ai_ci;
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
    op_id               TEXT NOT NULL,
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
CREATE INDEX idx_tool_call_op ON tool_call (turn_id, op_id(191));
CREATE UNIQUE INDEX idx_tool_op_attempt ON tool_call (turn_id, op_id(191), attempt);

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
                                        timeout_seconds       BIGINT NOT NULL DEFAULT 0,
    -- Task payload (predefined user task)
                                        task_prompt_uri       TEXT,
                                        task_prompt           MEDIUMTEXT,

    -- Optional orchestration workflow (reserved)

	    -- Bookkeeping
	                                        next_run_at           TIMESTAMP    NULL DEFAULT NULL,
	                                        last_run_at           TIMESTAMP    NULL DEFAULT NULL,
	                                        last_status           VARCHAR(32),
	                                        last_error            TEXT,
	                                        lease_owner           VARCHAR(255) NULL,
	                                        lease_until           TIMESTAMP    NULL DEFAULT NULL,
	                                        created_at            TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
	                                        updated_at            TIMESTAMP    NULL DEFAULT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

	CREATE INDEX idx_schedule_enabled_next ON schedule(enabled, next_run_at);
	CREATE INDEX idx_schedule_enabled_next_lease ON schedule(enabled, next_run_at, lease_until);

-- Per-run audit trail
CREATE TABLE IF NOT EXISTS schedule_run (
                                            id                     VARCHAR(255) PRIMARY KEY,
                                            schedule_id            VARCHAR(255) NOT NULL,
                                            created_at             TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                            updated_at             TIMESTAMP    NULL DEFAULT NULL,
                                            status                 VARCHAR(32)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','prechecking','skipped','running','succeeded','failed')),
                                            error_message          TEXT,
	                                            lease_owner           VARCHAR(255) NULL,
	                                            lease_until           TIMESTAMP    NULL DEFAULT NULL,

                                            precondition_ran_at    TIMESTAMP    NULL DEFAULT NULL,
                                            precondition_passed    TINYINT      NULL CHECK (precondition_passed IN (0,1)),
                                            precondition_result    MEDIUMTEXT,

	                                            conversation_id        VARCHAR(255) NULL,
	                                            conversation_kind      VARCHAR(32)  NOT NULL DEFAULT 'scheduled' CHECK (conversation_kind IN ('scheduled','precondition')),
	                                            scheduled_for          TIMESTAMP    NULL DEFAULT NULL,
	                                            started_at             TIMESTAMP    NULL DEFAULT NULL,
	                                            completed_at           TIMESTAMP    NULL DEFAULT NULL,

                                            CONSTRAINT fk_run_schedule FOREIGN KEY (schedule_id) REFERENCES schedule(id) ON DELETE CASCADE,
                                            CONSTRAINT fk_run_conversation FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

	CREATE INDEX idx_run_schedule_status ON schedule_run(schedule_id, status);
	CREATE UNIQUE INDEX ux_run_schedule_scheduled_for ON schedule_run(schedule_id, scheduled_for);

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

CREATE INDEX IF NOT EXISTS idx_emb_asset_path
  ON emb_asset(dataset_id, path(255));

CREATE INDEX IF NOT EXISTS idx_emb_asset_mod
  ON emb_asset(dataset_id, mod_time);

CREATE INDEX IF NOT EXISTS idx_shadow_vec_docs_scn
  ON shadow_vec_docs(dataset_id, scn);

CREATE INDEX IF NOT EXISTS idx_shadow_vec_docs_archived
  ON shadow_vec_docs(dataset_id, archived);

CREATE TABLE IF NOT EXISTS tool_approval_queue (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36),
    turn_id VARCHAR(36),
    message_id VARCHAR(36),
    tool_name VARCHAR(255) NOT NULL,
    title TEXT,
    arguments LONGBLOB NOT NULL,
    metadata LONGBLOB,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    decision VARCHAR(50),
    approved_by_user_id VARCHAR(255),
    approved_at DATETIME,
    executed_at DATETIME,
    error_message TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME,
    completed_at DATETIME,
    expires_at DATETIME,
    timed_out_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_taq_user_status_created ON tool_approval_queue(user_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_taq_conversation_status ON tool_approval_queue(conversation_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_taq_turn ON tool_approval_queue(turn_id, created_at);
CREATE INDEX IF NOT EXISTS idx_taq_status_expires_at ON tool_approval_queue(status, expires_at);

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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_artifact_ref
    ON report_shared_artifact(owner_id(191), artifact_ref(191));
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_report_id
    ON report_shared_artifact(owner_id, report_id);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_kind_lifecycle_updated
    ON report_shared_artifact(owner_id, kind, lifecycle, updated_at, created_at);

CREATE TABLE IF NOT EXISTS report_run (
    report_run_id VARCHAR(255) PRIMARY KEY,
    owner_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(255) NULL,
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

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
        CHECK (report_run_id IS NULL OR (format = 'pdf' AND scope = 'draft')),
    KEY idx_report_export_job_run (report_run_id),
    CONSTRAINT fk_report_export_job_run
        FOREIGN KEY (report_run_id) REFERENCES report_run(report_run_id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_submitted_at
    ON report_export_job(owner_id, submitted_at);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_artifact_ref
    ON report_export_job(owner_id, artifact_ref(191));
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_status
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_created_at
    ON report_export_artifact(owner_id, created_at);
CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_artifact_ref
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE INDEX IF NOT EXISTS idx_report_audit_event_actor_occurred_at
    ON report_audit_event(actor_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_artifact_ref
    ON report_audit_event(artifact_ref(191));

CREATE TABLE IF NOT EXISTS conversation_report_context (
    owner_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(255) NOT NULL,
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

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
