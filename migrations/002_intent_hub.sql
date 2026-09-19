-- 合作线索归口：参访行程、企业授权、项目条件、园区反馈、
-- 跨园转交回执、政策版本确认、实地对接与分歧处理、状态轨迹。

-- 园区 ----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS parks (
    park_ref   TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

-- 经办人：归属唯一园区，处理范围以所属园区为界
CREATE TABLE IF NOT EXISTS operators (
    operator_id TEXT PRIMARY KEY,
    park_ref    TEXT NOT NULL REFERENCES parks(park_ref),
    name        TEXT NOT NULL,
    active      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL
);

-- 企业 ----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS enterprises (
    enterprise_ref TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

-- 企业授权按版本保存：consent_ref 是企业一次授权的识别符，
-- 合作范围调整即产生新版本，旧版本留痕。
CREATE TABLE IF NOT EXISTS authorizations (
    authorization_id INTEGER PRIMARY KEY AUTOINCREMENT,
    enterprise_ref   TEXT NOT NULL REFERENCES enterprises(enterprise_ref),
    consent_ref      TEXT NOT NULL,
    scope_json       TEXT NOT NULL DEFAULT '{}',
    effective_at     TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    UNIQUE (enterprise_ref, consent_ref)
);

-- 联系人挂在具体授权版本下；public_consent=0 的联系人
-- 永远不会出现在对外合作清单中。
CREATE TABLE IF NOT EXISTS contacts (
    contact_id       INTEGER PRIMARY KEY AUTOINCREMENT,
    authorization_id INTEGER NOT NULL REFERENCES authorizations(authorization_id),
    enterprise_ref   TEXT NOT NULL REFERENCES enterprises(enterprise_ref),
    name             TEXT NOT NULL,
    role_tag         TEXT NOT NULL DEFAULT '',
    channel          TEXT NOT NULL DEFAULT '',
    public_consent   INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL
);

-- 参访行程（一次走访的归口事实，来源记录可能有多条）
CREATE TABLE IF NOT EXISTS visits (
    enterprise_ref TEXT NOT NULL REFERENCES enterprises(enterprise_ref),
    visit_ref      TEXT NOT NULL,
    park_ref       TEXT NOT NULL REFERENCES parks(park_ref),
    occurred_at    TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    PRIMARY KEY (enterprise_ref, visit_ref)
);

-- 合作意向：同一企业同一次走访只归口为一条意向
CREATE TABLE IF NOT EXISTS intents (
    intent_id                TEXT PRIMARY KEY,
    enterprise_ref           TEXT NOT NULL REFERENCES enterprises(enterprise_ref),
    visit_ref                TEXT NOT NULL,
    current_park_ref         TEXT NOT NULL REFERENCES parks(park_ref),
    status                   TEXT NOT NULL DEFAULT 'DRAFT',
    title                    TEXT NOT NULL DEFAULT '',
    conditions_json          TEXT NOT NULL DEFAULT '{}',
    consent_ref              TEXT,
    confirmed_policy_version TEXT,
    has_conflicts            INTEGER NOT NULL DEFAULT 0,
    published_at             TEXT,
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL,
    UNIQUE (enterprise_ref, visit_ref)
);
CREATE INDEX IF NOT EXISTS idx_intents_park ON intents(current_park_ref, status);

-- 各方（台商、台青、区县招商）提交的原始线索，允许与归口视图不一致
CREATE TABLE IF NOT EXISTS source_records (
    source_record_id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id        TEXT NOT NULL REFERENCES intents(intent_id),
    source           TEXT NOT NULL,            -- enterprise / youth / district
    source_record_ref TEXT NOT NULL,
    submitter_ref    TEXT NOT NULL DEFAULT '',
    park_ref         TEXT NOT NULL DEFAULT '',
    payload_json     TEXT NOT NULL,
    conflicts_json   TEXT NOT NULL DEFAULT '[]',
    received_at      TEXT NOT NULL,
    UNIQUE (source, source_record_ref)
);
CREATE INDEX IF NOT EXISTS idx_source_records_intent ON source_records(intent_id);

-- 优惠政策版本 ----------------------------------------------------------
CREATE TABLE IF NOT EXISTS policy_revisions (
    version     TEXT PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}',
    effective_at TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

-- 企业对政策版本的逐次确认（签约前版本变化须重新确认）
CREATE TABLE IF NOT EXISTS policy_confirmations (
    confirmation_id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id       TEXT NOT NULL REFERENCES intents(intent_id),
    version         TEXT NOT NULL REFERENCES policy_revisions(version),
    consent_ref     TEXT NOT NULL,
    operator_id     TEXT NOT NULL REFERENCES operators(operator_id),
    confirmed_at    TEXT NOT NULL,
    UNIQUE (intent_id, version)
);

-- 跨园转交与接收回执 ----------------------------------------------------
CREATE TABLE IF NOT EXISTS transfers (
    transfer_id     TEXT PRIMARY KEY,
    receipt_no      TEXT NOT NULL UNIQUE,
    intent_id       TEXT NOT NULL REFERENCES intents(intent_id),
    from_park_ref   TEXT NOT NULL REFERENCES parks(park_ref),
    to_park_ref     TEXT NOT NULL REFERENCES parks(park_ref),
    operator_id     TEXT NOT NULL REFERENCES operators(operator_id),
    previous_status TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'PENDING', -- PENDING/ACCEPTED/REJECTED
    note            TEXT NOT NULL DEFAULT '',
    transferred_at  TEXT NOT NULL,
    acknowledged_at TEXT,
    ack_operator_id TEXT REFERENCES operators(operator_id),
    reject_reason   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_transfers_parks
    ON transfers(from_park_ref, to_park_ref, status);

-- 园区反馈
CREATE TABLE IF NOT EXISTS park_feedback (
    feedback_id INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id   TEXT NOT NULL REFERENCES intents(intent_id),
    park_ref    TEXT NOT NULL REFERENCES parks(park_ref),
    operator_id TEXT NOT NULL REFERENCES operators(operator_id),
    content     TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_intent ON park_feedback(intent_id);

-- 每次实地对接
CREATE TABLE IF NOT EXISTS engagements (
    engagement_id TEXT PRIMARY KEY,
    intent_id     TEXT NOT NULL REFERENCES intents(intent_id),
    park_ref      TEXT NOT NULL REFERENCES parks(park_ref),
    operator_id   TEXT NOT NULL REFERENCES operators(operator_id),
    occurred_at   TEXT NOT NULL,
    location      TEXT NOT NULL DEFAULT '',
    summary       TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_engagements_intent ON engagements(intent_id, occurred_at);

-- 分歧处理
CREATE TABLE IF NOT EXISTS disputes (
    dispute_id           TEXT PRIMARY KEY,
    intent_id            TEXT NOT NULL REFERENCES intents(intent_id),
    engagement_id        TEXT REFERENCES engagements(engagement_id),
    raised_by            TEXT NOT NULL DEFAULT '',
    issue                TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'OPEN', -- OPEN/RESOLVED
    resolution           TEXT NOT NULL DEFAULT '',
    resolver_operator_id TEXT REFERENCES operators(operator_id),
    created_at           TEXT NOT NULL,
    resolved_at          TEXT
);
CREATE INDEX IF NOT EXISTS idx_disputes_intent ON disputes(intent_id);

-- 意向状态轨迹：状态流转、归口、授权、转交回执、反馈、对接、分歧、政策确认
CREATE TABLE IF NOT EXISTS intent_events (
    event_id    INTEGER PRIMARY KEY AUTOINCREMENT,
    intent_id   TEXT NOT NULL REFERENCES intents(intent_id),
    event_type  TEXT NOT NULL,
    operator_id TEXT,
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    detail_json TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_intent ON intent_events(intent_id, event_id);

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_intent_hub');
