-- Existing reporting baseline, applied after the host conversation table.
CREATE TABLE IF NOT EXISTS report_shared_artifact (
    artifact_id TEXT PRIMARY KEY,
    artifact_ref TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    owner_ref TEXT,
    kind TEXT NOT NULL,
    lifecycle TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    report_id TEXT,
    title TEXT,
    source_artifact_id TEXT,
    base_artifact_ref TEXT,
    policy_ref TEXT,
    document_version INTEGER NOT NULL DEFAULT 0,
    report_document_json BLOB,
    report_spec_json BLOB,
    compile_state_json BLOB,
    report_fill_json BLOB,
    report_print_json BLOB,
    saved_view_overlay_json BLOB,
    metadata_json BLOB,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_artifact_ref ON report_shared_artifact(owner_id, artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_report_id ON report_shared_artifact(owner_id, report_id);
CREATE INDEX IF NOT EXISTS idx_report_shared_artifact_owner_kind_lifecycle_updated ON report_shared_artifact(owner_id, kind, lifecycle, updated_at, created_at);

CREATE TABLE IF NOT EXISTS report_export_job (
    job_id TEXT PRIMARY KEY,
    artifact_ref TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    conversation_id TEXT,
    workspace_id TEXT,
    auth_context_ref TEXT,
    format TEXT NOT NULL,
    scope TEXT NOT NULL,
    status TEXT NOT NULL,
    report_spec_json BLOB,
    report_fill_json BLOB,
    report_print_json BLOB,
    metadata_json BLOB,
    artifact_id TEXT,
    error_text TEXT,
    diagnostics_json BLOB,
    submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME,
    retention_ttl_sec INTEGER NOT NULL DEFAULT 0,
    report_run_id TEXT,
    report_run_revision INTEGER,
    export_request_id TEXT,
    UNIQUE(owner_id, conversation_id, export_request_id),
    CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
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
    CHECK (report_run_id IS NULL OR (format = 'pdf' AND scope = 'draft')),
    FOREIGN KEY(report_run_id) REFERENCES report_run(report_run_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_submitted_at ON report_export_job(owner_id, submitted_at);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_artifact_ref ON report_export_job(owner_id, artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_export_job_owner_status ON report_export_job(owner_id, status);

CREATE TABLE IF NOT EXISTS report_export_artifact (
    artifact_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    artifact_ref TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    format TEXT NOT NULL,
    content_type TEXT NOT NULL,
    inline_data BLOB,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retention_ttl_sec INTEGER NOT NULL DEFAULT 0,
    UNIQUE(job_id),
    FOREIGN KEY(job_id) REFERENCES report_export_job(job_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_created_at ON report_export_artifact(owner_id, created_at);
CREATE INDEX IF NOT EXISTS idx_report_export_artifact_owner_artifact_ref ON report_export_artifact(owner_id, artifact_ref);

CREATE TABLE IF NOT EXISTS report_audit_event (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    artifact_ref TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    job_id TEXT,
    artifact_id TEXT,
    actor_id TEXT NOT NULL,
    actor_ref TEXT,
    occurred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata_json BLOB
);

CREATE INDEX IF NOT EXISTS idx_report_audit_event_actor_occurred_at ON report_audit_event(actor_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_artifact_ref ON report_audit_event(artifact_ref);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_occurred ON report_audit_event(occurred_at, event_id);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_job ON report_audit_event(job_id);
CREATE INDEX IF NOT EXISTS idx_report_audit_event_artifact ON report_audit_event(artifact_id);

CREATE TABLE IF NOT EXISTS report_run (
    report_run_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    conversation_id TEXT,
    materializer TEXT NOT NULL,
    origin TEXT,
    builder_ref TEXT,
    preset_id TEXT,
    source_kind TEXT,
    source_id TEXT,
    requested_params_json BLOB,
    effective_params_json BLOB,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    failure_code TEXT,
    failure_text TEXT,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    revision INTEGER NOT NULL,
    ui_run_request_id TEXT NOT NULL,
    report_spec_json BLOB,
    report_fill_json BLOB,
    report_print_json BLOB,
    activation_source TEXT,
    adoption_source TEXT,
    actor_id TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (owner_id, ui_run_request_id),
    UNIQUE (owner_id, report_run_id),
    FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_report_run_owner_conversation_updated
    ON report_run(owner_id, conversation_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_report_run_owner_status_updated
    ON report_run(owner_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_report_run_updated
    ON report_run(updated_at, report_run_id);

CREATE TABLE IF NOT EXISTS conversation_report_context (
    owner_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    active_report_run_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    activation_source TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (owner_id, conversation_id),
    FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE CASCADE,
    FOREIGN KEY (owner_id, active_report_run_id)
        REFERENCES report_run(owner_id, report_run_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_conversation_report_context_active_run
    ON conversation_report_context(owner_id, active_report_run_id);
