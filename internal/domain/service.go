package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Service 是线索归口的领域服务，全部写操作在数据库事务内完成。
type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service { return &Service{db: db} }

// ---------- 基础目录 ----------

// EnsurePark 登记或取得园区。
func (s *Service) EnsurePark(ctx context.Context, ref, name string) (*Park, error) {
	if ref == "" || name == "" {
		return nil, fmt.Errorf("%w: 园区编号与名称必填", ErrInvalidInput)
	}
	t := now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO parks(park_ref, name, created_at) VALUES (?,?,?)
		 ON CONFLICT(park_ref) DO NOTHING`, ref, name, ts(t)); err != nil {
		return nil, err
	}
	return s.getPark(ctx, ref)
}

func (s *Service) getPark(ctx context.Context, ref string) (*Park, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT park_ref, name, created_at FROM parks WHERE park_ref = ?`, ref)
	var p Park
	var created string
	if err := row.Scan(&p.Ref, &p.Name, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 园区 %s", ErrNotFound, ref)
		}
		return nil, err
	}
	p.CreatedAt = pt(created)
	return &p, nil
}

// EnsureEnterprise 登记或取得来访企业。
func (s *Service) EnsureEnterprise(ctx context.Context, ref, name string) (*Enterprise, error) {
	if ref == "" {
		return nil, fmt.Errorf("%w: 企业编号必填", ErrInvalidInput)
	}
	if name == "" {
		name = ref
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO enterprises(enterprise_ref, name, created_at) VALUES (?,?,?)
		 ON CONFLICT(enterprise_ref) DO NOTHING`, ref, name, ts(now())); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT enterprise_ref, name, created_at FROM enterprises WHERE enterprise_ref = ?`, ref)
	var e Enterprise
	var created string
	if err := row.Scan(&e.Ref, &e.Name, &created); err != nil {
		return nil, err
	}
	e.CreatedAt = pt(created)
	return &e, nil
}

// RegisterOperator 将经办人登记到所属园区；重复登记按幂等处理。
func (s *Service) RegisterOperator(ctx context.Context, id, parkRef, name string) (*Operator, error) {
	if id == "" || name == "" {
		return nil, fmt.Errorf("%w: 经办人编号与姓名必填", ErrInvalidInput)
	}
	if _, err := s.getPark(ctx, parkRef); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO operators(operator_id, park_ref, name, active, created_at)
		 VALUES (?,?,?,1,?)
		 ON CONFLICT(operator_id) DO NOTHING`, id, parkRef, name, ts(now())); err != nil {
		return nil, err
	}
	return s.GetOperator(ctx, id)
}

// GetOperator 取得经办人；停用或不存在均按不可见处理。
func (s *Service) GetOperator(ctx context.Context, id string) (*Operator, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT operator_id, park_ref, name, active, created_at
		 FROM operators WHERE operator_id = ?`, id)
	var o Operator
	var active int
	var created string
	if err := row.Scan(&o.ID, &o.ParkRef, &o.Name, &active, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 经办人 %s", ErrNotFound, id)
		}
		return nil, err
	}
	if active == 0 {
		return nil, fmt.Errorf("%w: 经办人已停用", ErrNotFound)
	}
	o.Active = true
	o.CreatedAt = pt(created)
	return &o, nil
}

// requireOperator 在事务内校验经办人并返回其所属园区。
func requireOperatorTx(ctx context.Context, tx *sql.Tx, id string) (*Operator, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT operator_id, park_ref, name, active, created_at
		 FROM operators WHERE operator_id = ?`, id)
	var o Operator
	var active int
	var created string
	if err := row.Scan(&o.ID, &o.ParkRef, &o.Name, &active, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 经办人 %s", ErrNotFound, id)
		}
		return nil, err
	}
	if active == 0 {
		return nil, fmt.Errorf("%w: 经办人已停用", ErrNotFound)
	}
	o.Active = true
	o.CreatedAt = pt(created)
	return &o, nil
}

// ---------- 政策版本 ----------

// CreatePolicyRevision 登记一个优惠政策版本。
func (s *Service) CreatePolicyRevision(ctx context.Context, version, title string,
	detail map[string]any, effectiveAt time.Time) (*PolicyRevision, error) {
	if version == "" {
		return nil, fmt.Errorf("%w: 政策版本号必填", ErrInvalidInput)
	}
	if effectiveAt.IsZero() {
		effectiveAt = now()
	}
	raw, err := marshalJSON(detail)
	if err != nil {
		return nil, err
	}
	created := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO policy_revisions(version, title, detail_json, effective_at, created_at)
		 VALUES (?,?,?,?,?)`, version, title, raw, ts(effectiveAt), ts(created))
	if err != nil {
		return nil, mapConflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: 政策版本 %s", ErrAlreadyExists, version)
	}
	return &PolicyRevision{Version: version, Title: title, Detail: detail,
		EffectiveAt: effectiveAt, CreatedAt: created}, nil
}

// ActivePolicy 返回当前生效（生效时间不晚于现在）的最新政策版本。
func (s *Service) ActivePolicy(ctx context.Context) (*PolicyRevision, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT version, title, detail_json, effective_at, created_at
		 FROM policy_revisions
		 WHERE effective_at <= ?
		 ORDER BY effective_at DESC, rowid DESC LIMIT 1`, ts(now()))
	return scanPolicy(row)
}

func activePolicyTx(ctx context.Context, tx *sql.Tx) (*PolicyRevision, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT version, title, detail_json, effective_at, created_at
		 FROM policy_revisions
		 WHERE effective_at <= ?
		 ORDER BY effective_at DESC, rowid DESC LIMIT 1`, ts(now()))
	return scanPolicy(row)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanPolicy(sc rowScanner) (*PolicyRevision, error) {
	var p PolicyRevision
	var detail, effective, created string
	if err := sc.Scan(&p.Version, &p.Title, &detail, &effective, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoPolicy
		}
		return nil, err
	}
	var err error
	if p.Detail, err = unmarshalJSON(detail); err != nil {
		return nil, err
	}
	p.EffectiveAt = pt(effective)
	p.CreatedAt = pt(created)
	return &p, nil
}

// ---------- 线索归口 ----------

// IngestRecord 接收一条来源线索（台商/台青/区县招商）。
// 同一企业同一次走访的多条记录归口到同一条意向；与归口视图冲突的
// 字段以 conflicts 返回并标记在意向上，等待经办人工研判。
func (s *Service) IngestRecord(ctx context.Context, operatorID, source string, req IngestRequest) (*Intent, []SourceRecord, []string, error) {
	if !validSource(source) {
		return nil, nil, nil, fmt.Errorf("%w: 来源须为 enterprise/youth/district", ErrInvalidInput)
	}
	if req.SourceRecordRef == "" || req.EnterpriseRef == "" || req.VisitRef == "" {
		return nil, nil, nil, fmt.Errorf("%w: source_record_ref/enterprise_ref/visit_ref 必填", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return nil, nil, nil, err
	}

	// 同一条来源记录不重复入库。
	var dup int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM source_records WHERE source = ? AND source_record_ref = ?`,
		source, req.SourceRecordRef).Scan(&dup); err != nil {
		return nil, nil, nil, err
	}
	if dup > 0 {
		return nil, nil, nil, fmt.Errorf("%w: %s/%s", ErrDuplicateSource, source, req.SourceRecordRef)
	}

	entName, _ := req.Payload["enterprise_name"].(string)
	if err := s.ensureEnterpriseTx(ctx, tx, req.EnterpriseRef, entName); err != nil {
		return nil, nil, nil, err
	}
	if err := s.ensureVisitTx(ctx, tx, req, op.ParkRef); err != nil {
		return nil, nil, nil, err
	}

	intent, created, err := s.ensureIntentTx(ctx, tx, req.EnterpriseRef, req.VisitRef, op.ParkRef, req.title())
	if err != nil {
		return nil, nil, nil, err
	}

	conflicts := diffRecord(intent, source, req)
	// 首条来源记录的条件作为归口初始值（首条记录不与自身冲突）。
	if created {
		if c := req.conditions(); len(c) > 0 {
			intent.Conditions = c
		}
	}

	raw, err := marshalJSON(req.Payload)
	if err != nil {
		return nil, nil, nil, err
	}
	conflictsRaw, err := marshalStringSlice(conflicts)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO source_records
		    (intent_id, source, source_record_ref, submitter_ref, park_ref,
		     payload_json, conflicts_json, received_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		intent.ID, source, req.SourceRecordRef, req.SubmitterRef, op.ParkRef,
		raw, conflictsRaw, ts(now())); err != nil {
		return nil, nil, nil, mapConflict(err)
	}

	if created {
		intent.HasConflicts = false
	}
	if intent.Title == "" {
		intent.Title = req.title()
	}
	intent.HasConflicts = intent.HasConflicts || len(conflicts) > 0
	if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
		return nil, nil, nil, err
	}

	detail := map[string]any{
		"source": source, "source_record_ref": req.SourceRecordRef,
		"submitter_ref": req.SubmitterRef, "park_ref": op.ParkRef,
	}
	if len(conflicts) > 0 {
		detail["conflicts"] = conflicts
	}
	if err := appendEventTx(ctx, tx, intent.ID, "SOURCE_RECORDED", op.ID, "", "", detail); err != nil {
		return nil, nil, nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	intent, sources, err := s.loadIntent(ctx, intent.ID)
	return intent, sources, conflicts, err
}

// IngestRequest 是一条来源线索的提交内容；字段宽松以兼容三方各自口径。
type IngestRequest struct {
	SourceRecordRef string         `json:"source_record_ref"`
	EnterpriseRef   string         `json:"enterprise_ref"`
	VisitRef        string         `json:"visit_ref"`
	SubmitterRef    string         `json:"submitter_ref"`
	OccurredAt      string         `json:"occurred_at,omitempty"`
	Payload         map[string]any `json:"payload"`
}

func (r IngestRequest) title() string {
	if r.Payload != nil {
		if t, ok := r.Payload["title"].(string); ok {
			return t
		}
	}
	return ""
}

func (r IngestRequest) conditions() map[string]any {
	if r.Payload == nil {
		return nil
	}
	if c, ok := r.Payload["conditions"].(map[string]any); ok {
		return c
	}
	return nil
}

func (r IngestRequest) parkRef() string {
	if r.Payload == nil {
		return ""
	}
	if p, ok := r.Payload["park_ref"].(string); ok {
		return p
	}
	return ""
}

func validSource(source string) bool {
	switch source {
	case SourceEnterprise, SourceYouth, SourceDistrict:
		return true
	}
	return false
}

// diffRecord 比较新到来源记录与归口视图，列出冲突字段。
func diffRecord(intent *Intent, source string, req IngestRequest) []string {
	var conflicts []string
	if t := req.title(); t != "" && intent.Title != "" && t != intent.Title {
		conflicts = append(conflicts, "title")
	}
	if c := req.conditions(); len(c) > 0 && len(intent.Conditions) > 0 &&
		canonicalJSON(c) != canonicalJSON(intent.Conditions) {
		conflicts = append(conflicts, "conditions")
	}
	if p := req.parkRef(); p != "" && p != intent.CurrentParkRef {
		conflicts = append(conflicts, "park_ref")
	}
	return conflicts
}

func (s *Service) ensureEnterpriseTx(ctx context.Context, tx *sql.Tx, ref, name string) error {
	if name == "" {
		name = ref
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO enterprises(enterprise_ref, name, created_at) VALUES (?,?,?)
		 ON CONFLICT(enterprise_ref) DO NOTHING`, ref, name, ts(now()))
	return err
}

func (s *Service) ensureVisitTx(ctx context.Context, tx *sql.Tx, req IngestRequest, fallbackPark string) error {
	var existed int
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM visits WHERE enterprise_ref = ? AND visit_ref = ?`,
		req.EnterpriseRef, req.VisitRef).Scan(&existed)
	if err != nil {
		return err
	}
	if existed > 0 {
		return nil
	}
	occurred := parseLoose(req.OccurredAt)
	if occurred.IsZero() {
		if v, ok := req.Payload["occurred_at"].(string); ok {
			occurred = parseLoose(v)
		}
	}
	if occurred.IsZero() {
		occurred = now()
	}
	note, _ := req.Payload["visit_note"].(string)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO visits(enterprise_ref, visit_ref, park_ref, occurred_at, note, created_at)
		 VALUES (?,?,?,?,?,?)`,
		req.EnterpriseRef, req.VisitRef, fallbackPark, ts(occurred), note, ts(now()))
	return mapConflict(err)
}

// ensureIntentTx 按（企业，走访）归口：存在即取回，不存在则建立。
func (s *Service) ensureIntentTx(ctx context.Context, tx *sql.Tx,
	entRef, visitRef, parkRef, title string) (*Intent, bool, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+intentColumns+` FROM intents WHERE enterprise_ref = ? AND visit_ref = ?`,
		entRef, visitRef)
	intent, err := scanIntent(row)
	if err == nil {
		return intent, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	id := newID("INTENT")
	t := now()
	intent = &Intent{
		ID: id, EnterpriseRef: entRef, VisitRef: visitRef,
		CurrentParkRef: parkRef, Status: StatusDraft, Title: title,
		Conditions: map[string]any{}, CreatedAt: t, UpdatedAt: t,
	}
	if err := insertIntentTx(ctx, tx, intent); err != nil {
		return nil, false, err
	}
	if err := appendEventTx(ctx, tx, id, "INTENT_CREATED", "", "", StatusDraft,
		map[string]any{"visit_ref": visitRef, "park_ref": parkRef}); err != nil {
		return nil, false, err
	}
	return intent, true, nil
}

const intentColumns = `intent_id, enterprise_ref, visit_ref, current_park_ref, status, title,
	conditions_json, consent_ref, confirmed_policy_version, has_conflicts,
	published_at, created_at, updated_at`

func scanIntent(sc rowScanner) (*Intent, error) {
	var i Intent
	var conditions, consent, policyVer, published sql.NullString
	var hasConflicts int
	var created, updated string
	if err := sc.Scan(&i.ID, &i.EnterpriseRef, &i.VisitRef, &i.CurrentParkRef, &i.Status,
		&i.Title, &conditions, &consent, &policyVer, &hasConflicts,
		&published, &created, &updated); err != nil {
		return nil, err
	}
	var err error
	if i.Conditions, err = unmarshalJSON(conditions.String); err != nil {
		return nil, err
	}
	i.ConsentRef = consent.String
	i.ConfirmedPolicyVersion = policyVer.String
	i.HasConflicts = hasConflicts == 1
	i.Published = published.Valid
	if published.Valid {
		t := pt(published.String)
		i.PublishedAt = &t
	}
	i.CreatedAt = pt(created)
	i.UpdatedAt = pt(updated)
	return &i, nil
}

func insertIntentTx(ctx context.Context, tx *sql.Tx, i *Intent) error {
	conditions, err := marshalJSON(i.Conditions)
	if err != nil {
		return err
	}
	var published sql.NullString
	if i.Published {
		published = sql.NullString{String: ts(i.UpdatedAt), Valid: true}
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO intents(intent_id, enterprise_ref, visit_ref, current_park_ref, status, title,
			conditions_json, consent_ref, confirmed_policy_version, has_conflicts,
			published_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.EnterpriseRef, i.VisitRef, i.CurrentParkRef, i.Status, i.Title,
		conditions, nullable(i.ConsentRef), nullable(i.ConfirmedPolicyVersion),
		boolInt(i.HasConflicts), published, ts(i.CreatedAt), ts(i.UpdatedAt))
	return err
}

func updateIntentCoreTx(ctx context.Context, tx *sql.Tx, i *Intent) error {
	conditions, err := marshalJSON(i.Conditions)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE intents SET current_park_ref = ?, status = ?, title = ?, conditions_json = ?,
			consent_ref = ?, confirmed_policy_version = ?, has_conflicts = ?, updated_at = ?
		 WHERE intent_id = ?`,
		i.CurrentParkRef, i.Status, i.Title, conditions,
		nullable(i.ConsentRef), nullable(i.ConfirmedPolicyVersion),
		boolInt(i.HasConflicts), ts(now()), i.ID)
	return err
}

// ---------- 意向读取 ----------

func (s *Service) loadIntent(ctx context.Context, id string) (*Intent, []SourceRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+intentColumns+` FROM intents WHERE intent_id = ?`, id)
	intent, err := scanIntent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("%w: 意向 %s", ErrNotFound, id)
		}
		return nil, nil, err
	}
	sources, err := s.listSources(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return intent, sources, nil
}

// GetIntent 供经办人读取本园区意向及其全部来源记录。
func (s *Service) GetIntent(ctx context.Context, operatorID, intentID string) (*Intent, []SourceRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, nil, err
	}
	sources, err := listSourcesTx(ctx, tx, intentID)
	if err != nil {
		return nil, nil, err
	}
	return intent, sources, nil
}

func loadIntentForOperatorTx(ctx context.Context, tx *sql.Tx, operatorID, intentID string) (*Intent, error) {
	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx,
		`SELECT `+intentColumns+` FROM intents WHERE intent_id = ?`, intentID)
	intent, err := scanIntent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 意向 %s", ErrNotFound, intentID)
		}
		return nil, err
	}
	if intent.CurrentParkRef != op.ParkRef {
		return nil, ErrCrossPark
	}
	return intent, nil
}

// ListIntents 列出经办人所属园区的意向（可按状态过滤）。
func (s *Service) ListIntents(ctx context.Context, operatorID, status string) ([]Intent, error) {
	op, err := s.GetOperator(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + intentColumns + ` FROM intents WHERE current_park_ref = ?`
	args := []any{op.ParkRef}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Intent
	for rows.Next() {
		i, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func (s *Service) listSources(ctx context.Context, intentID string) ([]SourceRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_record_id, source, source_record_ref, submitter_ref, park_ref,
		        payload_json, conflicts_json, received_at
		 FROM source_records WHERE intent_id = ? ORDER BY source_record_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSources(rows)
}

func listSourcesTx(ctx context.Context, tx *sql.Tx, intentID string) ([]SourceRecord, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT source_record_id, source, source_record_ref, submitter_ref, park_ref,
		        payload_json, conflicts_json, received_at
		 FROM source_records WHERE intent_id = ? ORDER BY source_record_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSources(rows)
}

func scanSources(rows *sql.Rows) ([]SourceRecord, error) {
	var out []SourceRecord
	for rows.Next() {
		var r SourceRecord
		var payload, conflicts, received string
		if err := rows.Scan(&r.ID, &r.Source, &r.SourceRecordRef, &r.SubmitterRef,
			&r.ParkRef, &payload, &conflicts, &received); err != nil {
			return nil, err
		}
		var err error
		if r.Payload, err = unmarshalJSON(payload); err != nil {
			return nil, err
		}
		r.Conflicts = unmarshalStringSlice(conflicts)
		r.ReceivedAt = pt(received)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- 企业授权与合作范围 ----------

// SubmitAuthorization 登记企业一次授权（consent_ref）及其联系人公开意愿。
// 自动关联到该企业在本园区尚未关联授权的在途意向。
func (s *Service) SubmitAuthorization(ctx context.Context, operatorID, consentRef string,
	req AuthorizationRequest) (*Authorization, error) {
	if consentRef == "" || req.EnterpriseRef == "" {
		return nil, fmt.Errorf("%w: consent_ref 与 enterprise_ref 必填", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return nil, err
	}
	if err := ensureEnterpriseExistsTx(ctx, tx, req.EnterpriseRef); err != nil {
		return nil, err
	}

	var existed int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM authorizations WHERE enterprise_ref = ? AND consent_ref = ?`,
		req.EnterpriseRef, consentRef).Scan(&existed); err != nil {
		return nil, err
	}
	if existed > 0 {
		return nil, fmt.Errorf("%w: 授权 %s", ErrAlreadyExists, consentRef)
	}

	effective := parseLoose(req.EffectiveAt)
	if effective.IsZero() {
		effective = now()
	}
	created := now()
	scopeRaw, err := marshalJSON(req.Scope)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO authorizations(enterprise_ref, consent_ref, scope_json, effective_at, created_at)
		 VALUES (?,?,?,?,?)`, req.EnterpriseRef, consentRef, scopeRaw, ts(effective), ts(created))
	if err != nil {
		return nil, mapConflict(err)
	}
	authID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, c := range req.Contacts {
		if strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("%w: 联系人姓名必填", ErrInvalidInput)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contacts(authorization_id, enterprise_ref, name, role_tag, channel,
				public_consent, created_at) VALUES (?,?,?,?,?,?,?)`,
			authID, req.EnterpriseRef, c.Name, c.RoleTag, c.Channel,
			boolInt(c.PublicConsent), ts(created)); err != nil {
			return nil, err
		}
	}

	// 关联本园区内在途且尚未挂授权的意向。
	rows, err := tx.QueryContext(ctx,
		`SELECT intent_id FROM intents
		 WHERE enterprise_ref = ? AND current_park_ref = ?
		   AND (consent_ref IS NULL OR consent_ref = '')
		   AND status NOT IN (?, ?)`,
		req.EnterpriseRef, op.ParkRef, StatusSigned, StatusClosed)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE intents SET consent_ref = ?, updated_at = ? WHERE intent_id = ?`,
			consentRef, ts(now()), id); err != nil {
			return nil, err
		}
		if err := appendEventTx(ctx, tx, id, "CONSENT_ATTACHED", op.ID, "", "",
			map[string]any{"consent_ref": consentRef}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getAuthorization(ctx, req.EnterpriseRef, consentRef)
}

// AuthorizationRequest 提交企业授权与联系人名单。
type AuthorizationRequest struct {
	EnterpriseRef string         `json:"enterprise_ref"`
	Scope         map[string]any `json:"scope"`
	EffectiveAt   string         `json:"effective_at,omitempty"`
	Contacts      []Contact      `json:"contacts"`
}

// AttachConsent 把指定授权版本挂到意向；公开后不得更换（合作范围锁定）。
func (s *Service) AttachConsent(ctx context.Context, operatorID, intentID, consentRef string) error {
	return s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		if intent.Published {
			return ErrPublished
		}
		var ent string
		err := tx.QueryRowContext(ctx,
			`SELECT enterprise_ref FROM authorizations WHERE enterprise_ref = ? AND consent_ref = ?`,
			intent.EnterpriseRef, consentRef).Scan(&ent)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: 授权 %s 不属于该企业", ErrInvalidInput, consentRef)
		}
		if err != nil {
			return err
		}
		intent.ConsentRef = consentRef
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, intent.ID, "CONSENT_ATTACHED", op.ID, "", "",
			map[string]any{"consent_ref": consentRef})
	})
}

// AdjustScope 供企业在未公开前调整合作范围：登记新版本授权并替换意向挂载。
func (s *Service) AdjustScope(ctx context.Context, operatorID, intentID, newConsentRef string,
	req AuthorizationRequest) (*Authorization, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, err
	}
	if intent.Published {
		return nil, ErrPublished
	}
	if isFinal(intent.Status) {
		return nil, ErrFinalized
	}
	op, _ := requireOperatorTx(ctx, tx, operatorID)
	req.EnterpriseRef = intent.EnterpriseRef

	auth, err := s.submitAuthorizationTx(ctx, tx, op.ID, newConsentRef, req)
	if err != nil {
		return nil, err
	}
	intent.ConsentRef = newConsentRef
	// 范围变化后原政策确认不再代表企业最新意愿，需重新确认。
	intent.ConfirmedPolicyVersion = ""
	if intent.Status == StatusPolicyConfirmed {
		prev := intent.Status
		intent.Status = StatusDraft
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return nil, err
		}
		if err := appendEventTx(ctx, tx, intent.ID, "STATUS_CHANGED", op.ID, prev, StatusDraft,
			map[string]any{"reason": "scope_adjusted"}); err != nil {
			return nil, err
		}
	} else if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
		return nil, err
	}
	if err := appendEventTx(ctx, tx, intent.ID, "SCOPE_ADJUSTED", op.ID, "", "",
		map[string]any{"consent_ref": newConsentRef}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return auth, nil
}

func (s *Service) submitAuthorizationTx(ctx context.Context, tx *sql.Tx, operatorID, consentRef string,
	req AuthorizationRequest) (*Authorization, error) {
	if consentRef == "" {
		return nil, fmt.Errorf("%w: consent_ref 必填", ErrInvalidInput)
	}
	if err := ensureEnterpriseExistsTx(ctx, tx, req.EnterpriseRef); err != nil {
		return nil, err
	}
	var existed int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM authorizations WHERE enterprise_ref = ? AND consent_ref = ?`,
		req.EnterpriseRef, consentRef).Scan(&existed); err != nil {
		return nil, err
	}
	if existed > 0 {
		return nil, fmt.Errorf("%w: 授权 %s", ErrAlreadyExists, consentRef)
	}
	effective := parseLoose(req.EffectiveAt)
	if effective.IsZero() {
		effective = now()
	}
	created := now()
	scopeRaw, err := marshalJSON(req.Scope)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO authorizations(enterprise_ref, consent_ref, scope_json, effective_at, created_at)
		 VALUES (?,?,?,?,?)`, req.EnterpriseRef, consentRef, scopeRaw, ts(effective), ts(created))
	if err != nil {
		return nil, mapConflict(err)
	}
	authID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, c := range req.Contacts {
		if strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("%w: 联系人姓名必填", ErrInvalidInput)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contacts(authorization_id, enterprise_ref, name, role_tag, channel,
				public_consent, created_at) VALUES (?,?,?,?,?,?,?)`,
			authID, req.EnterpriseRef, c.Name, c.RoleTag, c.Channel,
			boolInt(c.PublicConsent), ts(created)); err != nil {
			return nil, err
		}
	}
	auth := &Authorization{ID: authID, ConsentRef: consentRef, Scope: req.Scope,
		EffectiveAt: effective, CreatedAt: created, Contacts: req.Contacts}
	return auth, nil
}

func ensureEnterpriseExistsTx(ctx context.Context, tx *sql.Tx, ref string) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM enterprises WHERE enterprise_ref = ?`, ref).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: 企业 %s", ErrNotFound, ref)
	}
	return nil
}

func (s *Service) getAuthorization(ctx context.Context, entRef, consentRef string) (*Authorization, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT authorization_id, consent_ref, scope_json, effective_at, created_at
		 FROM authorizations WHERE enterprise_ref = ? AND consent_ref = ?`,
		entRef, consentRef)
	var a Authorization
	var scope, effective, created string
	if err := row.Scan(&a.ID, &a.ConsentRef, &scope, &effective, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 授权 %s", ErrNotFound, consentRef)
		}
		return nil, err
	}
	var err error
	if a.Scope, err = unmarshalJSON(scope); err != nil {
		return nil, err
	}
	a.EffectiveAt, a.CreatedAt = pt(effective), pt(created)
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, role_tag, channel, public_consent FROM contacts
		 WHERE authorization_id = ? ORDER BY contact_id`, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Contact
		var pub int
		if err := rows.Scan(&c.Name, &c.RoleTag, &c.Channel, &pub); err != nil {
			return nil, err
		}
		c.PublicConsent = pub == 1
		a.Contacts = append(a.Contacts, c)
	}
	return &a, rows.Err()
}

// ---------- 项目条件、冲突研判、园区反馈 ----------

// UpdateConditions 由经办人维护归口后的项目条件（可公开前调整）。
func (s *Service) UpdateConditions(ctx context.Context, operatorID, intentID, title string,
	conditions map[string]any) (*Intent, error) {
	err := s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		if intent.Published {
			return ErrPublished
		}
		if isFinal(intent.Status) {
			return ErrFinalized
		}
		if title != "" {
			intent.Title = title
		}
		if conditions != nil {
			intent.Conditions = conditions
		}
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, intent.ID, "CONDITIONS_UPDATED", op.ID, "", "",
			map[string]any{"title": intent.Title})
	})
	if err != nil {
		return nil, err
	}
	intent, _, err := s.loadIntent(ctx, intentID)
	return intent, err
}

// ReviewConflicts 标记冲突已人工研判（原始冲突仍保留在来源记录与事件中）。
func (s *Service) ReviewConflicts(ctx context.Context, operatorID, intentID string) error {
	return s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		intent.HasConflicts = false
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, intent.ID, "CONFLICTS_REVIEWED", op.ID, "", "", nil)
	})
}

// AddFeedback 记录所属园区对意向的反馈。
func (s *Service) AddFeedback(ctx context.Context, operatorID, intentID, content string) (*Feedback, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%w: 反馈内容必填", ErrInvalidInput)
	}
	var result *Feedback
	err := s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		created := now()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO park_feedback(intent_id, park_ref, operator_id, content, created_at)
			 VALUES (?,?,?,?,?)`, intent.ID, op.ParkRef, op.ID, content, ts(created))
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		result = &Feedback{ID: id, ParkRef: op.ParkRef, OperatorID: op.ID,
			Content: content, CreatedAt: created}
		return appendEventTx(ctx, tx, intent.ID, "FEEDBACK_ADDED", op.ID, "", "",
			map[string]any{"content": content})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---------- 政策确认与签约 ----------

// ConfirmPolicy 企业按当前生效政策版本确认；每次确认随授权编号留痕。
func (s *Service) ConfirmPolicy(ctx context.Context, operatorID, intentID, consentRef string) (*PolicyConfirmation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, err
	}
	if isFinal(intent.Status) {
		return nil, ErrFinalized
	}
	op, _ := requireOperatorTx(ctx, tx, operatorID)

	if consentRef == "" {
		consentRef = intent.ConsentRef
	}
	if consentRef == "" {
		return nil, ErrNoAuthorization
	}
	var ent string
	if err := tx.QueryRowContext(ctx,
		`SELECT enterprise_ref FROM authorizations WHERE enterprise_ref = ? AND consent_ref = ?`,
		intent.EnterpriseRef, consentRef).Scan(&ent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 授权 %s 不属于该企业", ErrInvalidInput, consentRef)
		}
		return nil, err
	}

	policy, err := activePolicyTx(ctx, tx)
	if err != nil {
		return nil, err
	}

	var already int
	_ = tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM policy_confirmations WHERE intent_id = ? AND version = ?`,
		intentID, policy.Version).Scan(&already)

	confirmed := now()
	if already == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO policy_confirmations(intent_id, version, consent_ref, operator_id, confirmed_at)
			 VALUES (?,?,?,?,?)`,
			intentID, policy.Version, consentRef, op.ID, ts(confirmed)); err != nil {
			return nil, mapConflict(err)
		}
	} else {
		if err := tx.QueryRowContext(ctx,
			`SELECT confirmed_at FROM policy_confirmations WHERE intent_id = ? AND version = ?`,
			intentID, policy.Version).Scan(&confirmed); err != nil {
			return nil, err
		}
	}

	prev := intent.Status
	intent.ConsentRef = consentRef
	intent.ConfirmedPolicyVersion = policy.Version
	if intent.Status == StatusDraft {
		intent.Status = StatusPolicyConfirmed
	}
	if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
		return nil, err
	}
	if prev != intent.Status {
		if err := appendEventTx(ctx, tx, intent.ID, "STATUS_CHANGED", op.ID, prev, intent.Status, nil); err != nil {
			return nil, err
		}
	}
	if err := appendEventTx(ctx, tx, intent.ID, "POLICY_CONFIRMED", op.ID, "", "",
		map[string]any{"version": policy.Version, "consent_ref": consentRef}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &PolicyConfirmation{IntentID: intentID, Version: policy.Version,
		ConsentRef: consentRef, OperatorID: op.ID, ConfirmedAt: confirmed}, nil
}

// PolicyReadiness 表示签约前政策门槛检查结果。
type PolicyReadiness struct {
	Ready                bool   `json:"ready"`
	ActiveVersion        string `json:"active_version,omitempty"`
	ConfirmedVersion     string `json:"confirmed_version,omitempty"`
	HasAuthorization     bool   `json:"has_authorization"`
	ReconfirmationNeeded bool   `json:"reconfirmation_needed"`
}

// Readiness 检查意向是否具备签约条件（授权齐备、政策为最新确认版本）。
func (s *Service) Readiness(ctx context.Context, operatorID, intentID string) (*PolicyReadiness, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, err
	}
	r := &PolicyReadiness{HasAuthorization: intent.ConsentRef != "", ConfirmedVersion: intent.ConfirmedPolicyVersion}
	policy, err := activePolicyTx(ctx, tx)
	if err == nil {
		r.ActiveVersion = policy.Version
		r.ReconfirmationNeeded = intent.ConfirmedPolicyVersion != policy.Version
	} else if errors.Is(err, ErrNoPolicy) {
		r.ReconfirmationNeeded = true
	} else {
		return nil, err
	}
	r.Ready = r.HasAuthorization && !r.ReconfirmationNeeded
	return r, nil
}

// SignIntent 签约：政策版本必须仍是企业确认过的版本，否则要求重新确认。
func (s *Service) SignIntent(ctx context.Context, operatorID, intentID string) error {
	return s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		if isFinal(intent.Status) {
			return ErrFinalized
		}
		if intent.ConsentRef == "" {
			return ErrNoAuthorization
		}
		policy, err := activePolicyTx(ctx, tx)
		if err != nil {
			return err
		}
		if intent.ConfirmedPolicyVersion != policy.Version {
			return ErrPolicyChanged
		}
		prev := intent.Status
		intent.Status = StatusSigned
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, intent.ID, "STATUS_CHANGED", op.ID, prev, StatusSigned,
			map[string]any{"policy_version": policy.Version})
	})
}

// PublishIntent 将意向对外公开；公开后合作范围锁定，且进入对外清单。
func (s *Service) PublishIntent(ctx context.Context, operatorID, intentID string) (time.Time, error) {
	published := now()
	err := s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		if intent.Published {
			return fmt.Errorf("%w: 意向已公开", ErrAlreadyExists)
		}
		if isFinal(intent.Status) {
			return ErrFinalized
		}
		if intent.ConsentRef == "" {
			return ErrNoAuthorization
		}
		policy, err := activePolicyTx(ctx, tx)
		if err != nil {
			return err
		}
		if intent.ConfirmedPolicyVersion != policy.Version {
			return ErrPolicyChanged
		}
		intent.Published = true
		if _, err := tx.ExecContext(ctx,
			`UPDATE intents SET published_at = ?, updated_at = ? WHERE intent_id = ?`,
			ts(published), ts(now()), intent.ID); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, intent.ID, "PUBLISHED", op.ID, "", "",
			map[string]any{"published_at": ts(published)})
	})
	if err != nil {
		return time.Time{}, err
	}
	return published, nil
}

// ---------- 跨园转交与接收回执 ----------

// TransferRequest 发起跨园转交。
type TransferRequest struct {
	ToParkRef string `json:"to_park_ref"`
	Note      string `json:"note"`
}

// Transfer 发起转交到另一园区；意向在对方凭回执接收前仍由原园区持有。
func (s *Service) Transfer(ctx context.Context, operatorID, intentID string, req TransferRequest) (*Transfer, error) {
	if req.ToParkRef == "" {
		return nil, fmt.Errorf("%w: to_park_ref 必填", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, err
	}
	if isFinal(intent.Status) {
		return nil, ErrFinalized
	}
	op, _ := requireOperatorTx(ctx, tx, operatorID)
	if op.ParkRef == req.ToParkRef {
		return nil, fmt.Errorf("%w: 接收园区须与当前园区不同", ErrInvalidInput)
	}
	if err := ensureParkExistsTx(ctx, tx, req.ToParkRef); err != nil {
		return nil, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM transfers WHERE intent_id = ? AND status = ?`,
		intentID, TransferPending).Scan(&pending); err != nil {
		return nil, err
	}
	if pending > 0 {
		return nil, fmt.Errorf("%w: 已有待接收的转交", ErrTransferState)
	}

	transfer := &Transfer{
		TransferID: newID("TRF"), ReceiptNo: newID("RC"),
		IntentID: intentID, FromParkRef: op.ParkRef, ToParkRef: req.ToParkRef,
		OperatorID: op.ID, PreviousStatus: intent.Status, Status: TransferPending,
		Note: req.Note, TransferredAt: now(),
	}
	if err := insertTransferTx(ctx, tx, transfer); err != nil {
		return nil, err
	}
	if err := appendEventTx(ctx, tx, intentID, "TRANSFER_REQUESTED", op.ID, "", "",
		map[string]any{
			"receipt_no": transfer.ReceiptNo, "transfer_id": transfer.TransferID,
			"from_park_ref": transfer.FromParkRef, "to_park_ref": transfer.ToParkRef,
			"note": req.Note,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return transfer, nil
}

// AcknowledgeTransfer 由接收园区经办人凭回执编号接收或拒收。
func (s *Service) AcknowledgeTransfer(ctx context.Context, operatorID, receiptNo string,
	accept bool, reason string) (*Transfer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return nil, err
	}
	transfer, err := scanTransferTx(ctx, tx,
		`SELECT `+transferColumns+` FROM transfers WHERE receipt_no = ?`, receiptNo)
	if err != nil {
		return nil, err
	}
	if transfer.ToParkRef != op.ParkRef {
		return nil, ErrParkScope
	}
	if transfer.Status != TransferPending {
		return nil, fmt.Errorf("%w: 转交已处理", ErrTransferState)
	}

	ackAt := now()
	transfer.AcknowledgedAt = &ackAt
	transfer.AckOperatorID = op.ID
	eventType := "TRANSFER_ACCEPTED"
	if accept {
		transfer.Status = TransferAccepted
	} else {
		transfer.Status = TransferRejected
		transfer.RejectReason = reason
		eventType = "TRANSFER_REJECTED"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE transfers SET status = ?, acknowledged_at = ?, ack_operator_id = ?,
			reject_reason = ? WHERE receipt_no = ?`,
		transfer.Status, ts(ackAt), op.ID, transfer.RejectReason, receiptNo); err != nil {
		return nil, err
	}

	intent, err := scanIntent(tx.QueryRowContext(ctx,
		`SELECT `+intentColumns+` FROM intents WHERE intent_id = ?`, transfer.IntentID))
	if err != nil {
		return nil, err
	}
	if accept {
		intent.CurrentParkRef = transfer.ToParkRef
		if err := updateIntentCoreTx(ctx, tx, intent); err != nil {
			return nil, err
		}
	}
	detail := map[string]any{
		"receipt_no": receiptNo, "from_park_ref": transfer.FromParkRef,
		"to_park_ref": transfer.ToParkRef,
	}
	if !accept {
		detail["reject_reason"] = reason
	}
	if err := appendEventTx(ctx, tx, transfer.IntentID, eventType, op.ID, "", "", detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return transfer, nil
}

// GetTransfer 按回执号查询转交单。
func (s *Service) GetTransfer(ctx context.Context, receiptNo string) (*Transfer, error) {
	return scanTransferTx(ctx, s.db,
		`SELECT `+transferColumns+` FROM transfers WHERE receipt_no = ?`, receiptNo)
}

// GetTransferForOperator 供经办人查询回执，仅发起园区或接收园区可见。
func (s *Service) GetTransferForOperator(ctx context.Context, operatorID, receiptNo string) (*Transfer, error) {
	op, err := s.GetOperator(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	transfer, err := s.GetTransfer(ctx, receiptNo)
	if err != nil {
		return nil, err
	}
	if op.ParkRef != transfer.FromParkRef && op.ParkRef != transfer.ToParkRef {
		return nil, ErrParkScope
	}
	return transfer, nil
}

// ListInboxTransfers 列出派给经办人园区待接收的转交。
func (s *Service) ListInboxTransfers(ctx context.Context, operatorID string) ([]Transfer, error) {
	op, err := s.GetOperator(ctx, operatorID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+transferColumns+` FROM transfers WHERE to_park_ref = ? AND status = ?
		 ORDER BY transferred_at`, op.ParkRef, TransferPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

const transferColumns = `transfer_id, receipt_no, intent_id, from_park_ref, to_park_ref,
	operator_id, previous_status, status, note, transferred_at,
	acknowledged_at, ack_operator_id, reject_reason`

func insertTransferTx(ctx context.Context, tx *sql.Tx, t *Transfer) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO transfers(transfer_id, receipt_no, intent_id, from_park_ref, to_park_ref,
			operator_id, previous_status, status, note, transferred_at,
			acknowledged_at, ack_operator_id, reject_reason)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.TransferID, t.ReceiptNo, t.IntentID, t.FromParkRef, t.ToParkRef,
		t.OperatorID, t.PreviousStatus, t.Status, t.Note, ts(t.TransferredAt),
		nilTime(t.AcknowledgedAt), nilString(t.AckOperatorID), t.RejectReason)
	return err
}

func scanTransferTx(ctx context.Context, q querier, query string, args ...any) (*Transfer, error) {
	row := q.QueryRowContext(ctx, query, args...)
	t, err := scanTransfer(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 转交回执", ErrNotFound)
		}
		return nil, err
	}
	return t, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanTransfer(sc rowScanner) (*Transfer, error) {
	var t Transfer
	var transferred string
	var ackAt, ackOp sql.NullString
	if err := sc.Scan(&t.TransferID, &t.ReceiptNo, &t.IntentID, &t.FromParkRef, &t.ToParkRef,
		&t.OperatorID, &t.PreviousStatus, &t.Status, &t.Note, &transferred,
		&ackAt, &ackOp, &t.RejectReason); err != nil {
		return nil, err
	}
	t.TransferredAt = pt(transferred)
	if ackAt.Valid {
		at := pt(ackAt.String)
		t.AcknowledgedAt = &at
	}
	t.AckOperatorID = ackOp.String
	return &t, nil
}

func ensureParkExistsTx(ctx context.Context, tx *sql.Tx, ref string) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM parks WHERE park_ref = ?`, ref).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: 园区 %s", ErrNotFound, ref)
	}
	return nil
}

// ---------- 实地对接与分歧处理 ----------

// EngagementRequest 登记一次实地对接。
type EngagementRequest struct {
	OccurredAt string `json:"occurred_at,omitempty"`
	Location   string `json:"location"`
	Summary    string `json:"summary"`
}

// AddEngagement 登记一次实地对接并进入意向追溯链。
func (s *Service) AddEngagement(ctx context.Context, operatorID, intentID string,
	req EngagementRequest) (*Engagement, error) {
	var result *Engagement
	err := s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		occurred := parseLoose(req.OccurredAt)
		if occurred.IsZero() {
			occurred = now()
		}
		e := &Engagement{EngagementID: newID("ENG"), ParkRef: op.ParkRef,
			OperatorID: op.ID, OccurredAt: occurred, Location: req.Location,
			Summary: req.Summary, CreatedAt: now()}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO engagements(engagement_id, intent_id, park_ref, operator_id,
				occurred_at, location, summary, created_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			e.EngagementID, intent.ID, e.ParkRef, e.OperatorID,
			ts(e.OccurredAt), e.Location, e.Summary, ts(e.CreatedAt)); err != nil {
			return err
		}
		if err := appendEventTx(ctx, tx, intent.ID, "ENGAGEMENT", op.ID, "", "",
			map[string]any{
				"engagement_id": e.EngagementID, "occurred_at": ts(e.OccurredAt),
				"location": e.Location, "summary": e.Summary,
			}); err != nil {
			return err
		}
		result = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AddDispute 登记实地对接中或围绕意向产生的分歧。
func (s *Service) AddDispute(ctx context.Context, operatorID, intentID, engagementID, raisedBy, issue string) (*Dispute, error) {
	if strings.TrimSpace(issue) == "" {
		return nil, fmt.Errorf("%w: 分歧内容必填", ErrInvalidInput)
	}
	var result *Dispute
	err := s.mutateIntent(ctx, operatorID, intentID, func(tx *sql.Tx, op *Operator, intent *Intent) error {
		if engagementID != "" {
			var n int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(1) FROM engagements WHERE engagement_id = ? AND intent_id = ?`,
				engagementID, intentID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: 对接记录 %s", ErrNotFound, engagementID)
			}
		}
		d := &Dispute{DisputeID: newID("DSP"), EngagementID: engagementID,
			RaisedBy: raisedBy, Issue: issue, Status: DisputeOpen, CreatedAt: now()}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO disputes(dispute_id, intent_id, engagement_id, raised_by, issue,
				status, resolution, resolver_operator_id, created_at, resolved_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			d.DisputeID, intentID, nilString(d.EngagementID), d.RaisedBy, d.Issue,
			d.Status, "", nil, ts(d.CreatedAt), nil); err != nil {
			return err
		}
		if err := appendEventTx(ctx, tx, intent.ID, "DISPUTE_OPENED", op.ID, "", "",
			map[string]any{
				"dispute_id": d.DisputeID, "engagement_id": d.EngagementID,
				"raised_by": d.RaisedBy, "issue": d.Issue,
			}); err != nil {
			return err
		}
		result = d
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ResolveDispute 记录分歧处理结论。
func (s *Service) ResolveDispute(ctx context.Context, operatorID, disputeID, resolution string) (*Dispute, error) {
	if strings.TrimSpace(resolution) == "" {
		return nil, fmt.Errorf("%w: 处理结论必填", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx,
		`SELECT dispute_id, intent_id, COALESCE(engagement_id,''), raised_by, issue,
		        status, COALESCE(resolution,''), created_at
		 FROM disputes WHERE dispute_id = ?`, disputeID)
	var d Dispute
	var created string
	if err := row.Scan(&d.DisputeID, &d.IntentID, &d.EngagementID,
		&d.RaisedBy, &d.Issue, &d.Status, &d.Resolution, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: 分歧 %s", ErrNotFound, disputeID)
		}
		return nil, err
	}
	d.CreatedAt = pt(created)
	// 园区范围校验以意向当前归属园区为准。
	parkRow := tx.QueryRowContext(ctx,
		`SELECT current_park_ref FROM intents WHERE intent_id = ?`, d.IntentID)
	var parkRef string
	if err := parkRow.Scan(&parkRef); err != nil {
		return nil, err
	}
	if parkRef != op.ParkRef {
		return nil, ErrCrossPark
	}
	if d.Status == DisputeResolved {
		return nil, fmt.Errorf("%w: 分歧已处理", ErrTransferState)
	}

	resolved := now()
	d.Status = DisputeResolved
	d.Resolution = resolution
	d.ResolverID = op.ID
	d.ResolvedAt = &resolved
	if _, err := tx.ExecContext(ctx,
		`UPDATE disputes SET status = ?, resolution = ?, resolver_operator_id = ?, resolved_at = ?
		 WHERE dispute_id = ?`,
		d.Status, d.Resolution, d.ResolverID, ts(resolved), disputeID); err != nil {
		return nil, err
	}
	if err := appendEventTx(ctx, tx, d.IntentID, "DISPUTE_RESOLVED", op.ID, "", "",
		map[string]any{
			"dispute_id": disputeID, "resolution": resolution,
		}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &d, nil
}

// ---------- 时间线与对外清单 ----------

// Timeline 汇总一条意向的归口全貌：状态轨迹可追到每次实地对接与分歧处理。
func (s *Service) Timeline(ctx context.Context, operatorID, intentID string) (*Timeline, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return nil, err
	}

	tl := &Timeline{Intent: *intent}

	visitRow := tx.QueryRowContext(ctx,
		`SELECT enterprise_ref, visit_ref, park_ref, occurred_at, COALESCE(note,''), created_at
		 FROM visits WHERE enterprise_ref = ? AND visit_ref = ?`,
		intent.EnterpriseRef, intent.VisitRef)
	var v Visit
	var occurred, vCreated string
	if err := visitRow.Scan(&v.EnterpriseRef, &v.VisitRef, &v.ParkRef, &occurred, &v.Note, &vCreated); err != nil {
		return nil, err
	}
	v.OccurredAt, v.CreatedAt = pt(occurred), pt(vCreated)
	tl.Visit = v

	entRow := tx.QueryRowContext(ctx,
		`SELECT enterprise_ref, name, created_at FROM enterprises WHERE enterprise_ref = ?`,
		intent.EnterpriseRef)
	var ent Enterprise
	var eCreated string
	if err := entRow.Scan(&ent.Ref, &ent.Name, &eCreated); err != nil {
		return nil, err
	}
	ent.CreatedAt = pt(eCreated)
	tl.Enterprise = ent

	if tl.Sources, err = listSourcesTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	if tl.Authorizations, err = listAuthorizationsTx(ctx, tx, intent.EnterpriseRef); err != nil {
		return nil, err
	}
	if tl.Confirmations, err = listConfirmationsTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	if tl.Transfers, err = listTransfersTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	if tl.Feedbacks, err = listFeedbackTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	if tl.Engagements, err = listEngagementsTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	if tl.Events, err = listEventsTx(ctx, tx, intentID); err != nil {
		return nil, err
	}
	return tl, nil
}

func listAuthorizationsTx(ctx context.Context, tx *sql.Tx, entRef string) ([]Authorization, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT authorization_id, consent_ref, scope_json, effective_at, created_at
		 FROM authorizations WHERE enterprise_ref = ?
		 ORDER BY effective_at, authorization_id`, entRef)
	if err != nil {
		return nil, err
	}
	var out []Authorization
	for rows.Next() {
		var a Authorization
		var scope, eff, created string
		if err := rows.Scan(&a.ID, &a.ConsentRef, &scope, &eff, &created); err != nil {
			rows.Close()
			return nil, err
		}
		var err error
		if a.Scope, err = unmarshalJSON(scope); err != nil {
			rows.Close()
			return nil, err
		}
		a.EffectiveAt, a.CreatedAt = pt(eff), pt(created)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // 释放外层游标后再查联系人。
	for i := range out {
		cRows, err := tx.QueryContext(ctx,
			`SELECT name, role_tag, channel, public_consent FROM contacts
			 WHERE authorization_id = ? ORDER BY contact_id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for cRows.Next() {
			var c Contact
			var pub int
			if err := cRows.Scan(&c.Name, &c.RoleTag, &c.Channel, &pub); err != nil {
				cRows.Close()
				return nil, err
			}
			c.PublicConsent = pub == 1
			out[i].Contacts = append(out[i].Contacts, c)
		}
		cRows.Close()
		if err := cRows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func listConfirmationsTx(ctx context.Context, tx *sql.Tx, intentID string) ([]PolicyConfirmation, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT intent_id, version, consent_ref, operator_id, confirmed_at
		 FROM policy_confirmations WHERE intent_id = ? ORDER BY confirmation_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyConfirmation
	for rows.Next() {
		var c PolicyConfirmation
		var at string
		if err := rows.Scan(&c.IntentID, &c.Version, &c.ConsentRef, &c.OperatorID, &at); err != nil {
			return nil, err
		}
		c.ConfirmedAt = pt(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

func listTransfersTx(ctx context.Context, tx *sql.Tx, intentID string) ([]Transfer, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+transferColumns+` FROM transfers WHERE intent_id = ? ORDER BY transferred_at`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func listFeedbackTx(ctx context.Context, tx *sql.Tx, intentID string) ([]Feedback, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT feedback_id, park_ref, operator_id, content, created_at
		 FROM park_feedback WHERE intent_id = ? ORDER BY feedback_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feedback
	for rows.Next() {
		var f Feedback
		var created string
		if err := rows.Scan(&f.ID, &f.ParkRef, &f.OperatorID, &f.Content, &created); err != nil {
			return nil, err
		}
		f.CreatedAt = pt(created)
		out = append(out, f)
	}
	return out, rows.Err()
}

func listEngagementsTx(ctx context.Context, tx *sql.Tx, intentID string) ([]Engagement, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT engagement_id, park_ref, operator_id, occurred_at, location, summary, created_at
		 FROM engagements WHERE intent_id = ? ORDER BY occurred_at, engagement_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Engagement
	for rows.Next() {
		var e Engagement
		var occurred, created string
		if err := rows.Scan(&e.EngagementID, &e.ParkRef, &e.OperatorID,
			&occurred, &e.Location, &e.Summary, &created); err != nil {
			return nil, err
		}
		e.OccurredAt, e.CreatedAt = pt(occurred), pt(created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	disputes, err := listDisputesTx(ctx, tx, intentID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, d := range disputes {
			if d.EngagementID == out[i].EngagementID {
				out[i].Disputes = append(out[i].Disputes, d)
			}
		}
	}
	return out, nil
}

func listDisputesTx(ctx context.Context, tx *sql.Tx, intentID string) ([]Dispute, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT dispute_id, COALESCE(engagement_id,''), raised_by, issue, status,
		        COALESCE(resolution,''), COALESCE(resolver_operator_id,''), created_at, resolved_at
		 FROM disputes WHERE intent_id = ? ORDER BY created_at`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dispute
	for rows.Next() {
		var d Dispute
		var created string
		var resolvedAt, resolver sql.NullString
		if err := rows.Scan(&d.DisputeID, &d.EngagementID, &d.RaisedBy, &d.Issue,
			&d.Status, &d.Resolution, &resolver, &created, &resolvedAt); err != nil {
			return nil, err
		}
		d.CreatedAt = pt(created)
		d.ResolverID = resolver.String
		if resolvedAt.Valid {
			at := pt(resolvedAt.String)
			d.ResolvedAt = &at
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func listEventsTx(ctx context.Context, tx *sql.Tx, intentID string) ([]Event, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT event_id, event_type, COALESCE(operator_id,''), COALESCE(from_status,''),
		        COALESCE(to_status,''), detail_json, created_at
		 FROM intent_events WHERE intent_id = ? ORDER BY event_id`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var detail, created string
		if err := rows.Scan(&e.EventID, &e.Type, &e.OperatorID, &e.FromStatus,
			&e.ToStatus, &detail, &created); err != nil {
			return nil, err
		}
		var err error
		if e.Detail, err = unmarshalJSON(detail); err != nil {
			return nil, err
		}
		e.CreatedAt = pt(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PublicListing 生成对外合作清单：仅含已公开意向，联系人只列公开同意者。
func (s *Service) PublicListing(ctx context.Context, parkRef string) ([]PublicEntry, error) {
	query := `SELECT i.intent_id, i.enterprise_ref, i.current_park_ref, i.title,
			i.conditions_json, i.status, i.consent_ref, i.published_at,
			COALESCE(e.name, ''), COALESCE(p.name, '')
		FROM intents i
		JOIN enterprises e ON e.enterprise_ref = i.enterprise_ref
		JOIN parks p ON p.park_ref = i.current_park_ref
		WHERE i.published_at IS NOT NULL`
	var args []any
	if parkRef != "" {
		query += ` AND i.current_park_ref = ?`
		args = append(args, parkRef)
	}
	query += ` ORDER BY i.published_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type rawEntry struct {
		e       PublicEntry
		consent string
	}
	var raws []rawEntry
	for rows.Next() {
		var r rawEntry
		var conditions string
		var publishedAt sql.NullString
		if err := rows.Scan(&r.e.IntentID, &r.e.EnterpriseRef, &r.e.ParkRef, &r.e.Title,
			&conditions, &r.e.Status, &r.consent, &publishedAt,
			&r.e.EnterpriseName, &r.e.ParkName); err != nil {
			rows.Close()
			return nil, err
		}
		if r.e.Conditions, err = unmarshalJSON(conditions); err != nil {
			rows.Close()
			return nil, err
		}
		if publishedAt.Valid {
			r.e.PublishedAt = pt(publishedAt.String)
		}
		raws = append(raws, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // 先释放唯一连接，再逐条查询联系人，避免自占死锁。

	entries := make([]PublicEntry, 0, len(raws))
	for _, r := range raws {
		if r.e.Contacts, err = s.publicContacts(ctx, r.e.EnterpriseRef, r.consent); err != nil {
			return nil, err
		}
		entries = append(entries, r.e)
	}
	return entries, nil
}

// publicContacts 取意向挂载授权版本里公开同意的联系人；
// 若挂载版本缺失则回退到企业最新授权版本。
func (s *Service) publicContacts(ctx context.Context, entRef, consentRef string) ([]Contact, error) {
	var authID int64
	if consentRef != "" {
		err := s.db.QueryRowContext(ctx,
			`SELECT authorization_id FROM authorizations
			 WHERE enterprise_ref = ? AND consent_ref = ?`, entRef, consentRef).Scan(&authID)
		if errors.Is(err, sql.ErrNoRows) {
			consentRef = ""
		} else if err != nil {
			return nil, err
		}
	}
	if consentRef == "" {
		err := s.db.QueryRowContext(ctx,
			`SELECT authorization_id FROM authorizations WHERE enterprise_ref = ?
			 ORDER BY effective_at DESC, authorization_id DESC LIMIT 1`, entRef).Scan(&authID)
		if errors.Is(err, sql.ErrNoRows) {
			return []Contact{}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, role_tag, channel FROM contacts
		 WHERE authorization_id = ? AND public_consent = 1 ORDER BY contact_id`, authID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		c.PublicConsent = true
		if err := rows.Scan(&c.Name, &c.RoleTag, &c.Channel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- 事务骨架与通用工具 ----------

// mutateIntent 在单个事务内完成“校验经办人园区范围 → 载入意向 → 修改 → 提交”。
func (s *Service) mutateIntent(ctx context.Context, operatorID, intentID string,
	fn func(tx *sql.Tx, op *Operator, intent *Intent) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	intent, err := loadIntentForOperatorTx(ctx, tx, operatorID, intentID)
	if err != nil {
		return err
	}
	op, err := requireOperatorTx(ctx, tx, operatorID)
	if err != nil {
		return err
	}
	if err := fn(tx, op, intent); err != nil {
		return err
	}
	return tx.Commit()
}

func appendEventTx(ctx context.Context, tx *sql.Tx, intentID, eventType, operatorID,
	fromStatus, toStatus string, detail map[string]any) error {
	raw, err := marshalJSON(detail)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO intent_events(intent_id, event_type, operator_id, from_status,
			to_status, detail_json, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		intentID, eventType, nilString(operatorID), fromStatus, toStatus, raw, ts(now()))
	return err
}

func isFinal(status string) bool { return status == StatusSigned || status == StatusClosed }

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nilString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nilTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func mapConflict(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrAlreadyExists
	}
	return err
}
