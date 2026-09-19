-- 合作线索归口领域模型
CREATE TABLE IF NOT EXISTS visits (
    visit_ref      TEXT PRIMARY KEY,
    enterprise_ref TEXT NOT NULL,
    park_ref       TEXT NOT NULL,
    visited_at     TEXT NOT NULL,
    summary        TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS intents (
    id                        TEXT PRIMARY KEY,
    visit_ref                 TEXT NOT NULL REFERENCES visits(visit_ref),
    enterprise_ref            TEXT NOT NULL,
    park_ref                  TEXT NOT NULL,
    title                     TEXT NOT NULL DEFAULT '',
    scope                     TEXT NOT NULL DEFAULT '',
    status                    TEXT NOT NULL DEFAULT 'draft',
    source                    TEXT NOT NULL DEFAULT '',
    policy_confirmed_revision TEXT NOT NULL DEFAULT '',
    published_at              TEXT,
    signed_at                 TEXT,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_intents_visit ON intents(enterprise_ref, visit_ref);

-- 每次来源提交（推介会、园区记录、区县招商等）各留一条原始记录
CREATE TABLE IF NOT EXISTS source_records (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id    TEXT NOT NULL REFERENCES intents(id),
    source       TEXT NOT NULL,
    submitted_by TEXT NOT NULL DEFAULT '',
    payload      TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_source_records_intent ON source_records(intent_id);

-- 多来源字段冲突形成的分歧
CREATE TABLE IF NOT EXISTS disputes (
    id          TEXT PRIMARY KEY,
    intent_id   TEXT NOT NULL REFERENCES intents(id),
    field       TEXT NOT NULL,
    values_json TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'open',
    resolution  TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    resolved_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_disputes_intent ON disputes(intent_id);

CREATE TABLE IF NOT EXISTS feedbacks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id  TEXT NOT NULL REFERENCES intents(id),
    park_ref   TEXT NOT NULL,
    handler_id TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS contacts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id       TEXT NOT NULL REFERENCES intents(id),
    name            TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT '',
    consent_granted INTEGER NOT NULL DEFAULT 0,
    consent_ref     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS transfers (
    id           TEXT PRIMARY KEY,
    intent_id    TEXT NOT NULL REFERENCES intents(id),
    from_park    TEXT NOT NULL,
    to_park      TEXT NOT NULL,
    initiated_by TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    receipt_json TEXT,
    created_at   TEXT NOT NULL,
    received_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_transfers_intent ON transfers(intent_id);

CREATE TABLE IF NOT EXISTS policies (
    park_ref     TEXT NOT NULL,
    revision     TEXT NOT NULL,
    content      TEXT NOT NULL DEFAULT '',
    effective_at TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (park_ref, revision)
);

-- 事件流：状态变更、实地对接、分歧处理、转交回执等全部留痕
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id   TEXT NOT NULL,
    kind        TEXT NOT NULL,
    actor       TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_intent ON events(intent_id, id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_domain');
