package leads

import (
	"encoding/json"
	"fmt"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/sqlitedb"
)

// Visit 是一次参访行程，作为同一企业多条来源记录的锚点。
type Visit struct {
	VisitRef      string `json:"visit_ref"`
	EnterpriseRef string `json:"enterprise_ref"`
	ParkRef       string `json:"park_ref"`
	VisitedAt     string `json:"visited_at"`
	Summary       string `json:"summary"`
	CreatedAt     string `json:"created_at"`
}

// Intent 是归口后的合作意向。
type Intent struct {
	ID                      string  `json:"id"`
	VisitRef                string  `json:"visit_ref"`
	EnterpriseRef           string  `json:"enterprise_ref"`
	ParkRef                 string  `json:"park_ref"`
	Title                   string  `json:"title"`
	Scope                   string  `json:"scope"`
	Status                  string  `json:"status"`
	Source                  string  `json:"source"`
	PolicyConfirmedRevision string  `json:"policy_confirmed_revision"`
	PolicyCurrentRevision   string  `json:"policy_current_revision"`
	PublishedAt             *string `json:"published_at,omitempty"`
	SignedAt                *string `json:"signed_at,omitempty"`
	CreatedAt               string  `json:"created_at"`
	UpdatedAt               string  `json:"updated_at"`
}

// SourceRecord 是一条来源提交的原始记录。
type SourceRecord struct {
	ID          int64          `json:"id"`
	IntentID    string         `json:"intent_id"`
	Source      string         `json:"source"`
	SubmittedBy string         `json:"submitted_by"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   string         `json:"created_at"`
}

// Dispute 是多来源字段冲突形成的分歧。
type Dispute struct {
	ID         string         `json:"id"`
	IntentID   string         `json:"intent_id"`
	Field      string         `json:"field"`
	Values     map[string]any `json:"values"`
	Status     string         `json:"status"`
	Resolution string         `json:"resolution,omitempty"`
	ResolvedBy string         `json:"resolved_by,omitempty"`
	CreatedAt  string         `json:"created_at"`
	ResolvedAt *string        `json:"resolved_at,omitempty"`
}

// Contact 是意向关联的联系人；未获企业同意前不得进入对外清单。
type Contact struct {
	ID             int64  `json:"id"`
	IntentID       string `json:"intent_id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	ConsentGranted bool   `json:"consent_granted"`
	ConsentRef     string `json:"consent_ref,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// Feedback 是园区对意向的反馈。
type Feedback struct {
	ID        int64  `json:"id"`
	IntentID  string `json:"intent_id"`
	ParkRef   string `json:"park_ref"`
	HandlerID string `json:"handler_id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// CreateVisit 登记参访行程。
func (s *Service) CreateVisit(v Visit) error {
	if v.VisitRef == "" || v.EnterpriseRef == "" || v.ParkRef == "" || v.VisitedAt == "" {
		return fmt.Errorf("%w: visit_ref / enterprise_ref / park_ref / visited_at 均为必填", ErrValidation)
	}
	row, found, err := s.db.QueryOne("SELECT visit_ref FROM visits WHERE visit_ref = ?", v.VisitRef)
	if err != nil {
		return err
	}
	if found && row["visit_ref"] != nil {
		return fmt.Errorf("%w: 参访行程 %s 已登记", ErrConflict, v.VisitRef)
	}
	return s.db.Exec(
		"INSERT INTO visits(visit_ref, enterprise_ref, park_ref, visited_at, summary, created_at) VALUES (?,?,?,?,?,?)",
		v.VisitRef, v.EnterpriseRef, v.ParkRef, v.VisitedAt, v.Summary, s.timestamp(),
	)
}

// SubmitIntentInput 是一次来源提交。
type SubmitIntentInput struct {
	VisitRef      string         `json:"visit_ref"`
	EnterpriseRef string         `json:"enterprise_ref"`
	Source        string         `json:"source"`
	SubmittedBy   string         `json:"submitted_by"`
	Title         string         `json:"title"`
	Scope         string         `json:"scope"`
	Conditions    map[string]any `json:"conditions"`
}

// SubmitIntentResult 是归口结果：意向、本次是否新建、以及冲突产生的分歧。
type SubmitIntentResult struct {
	Intent   Intent    `json:"intent"`
	Created  bool      `json:"created"`
	Disputes []Dispute `json:"disputes,omitempty"`
}

// SubmitIntent 提交一条来源记录。同一企业同一走访已存在意向时，
// 记录归并到该意向（归口），并对冲突字段自动生成分歧。
func (s *Service) SubmitIntent(in SubmitIntentInput) (*SubmitIntentResult, error) {
	if in.VisitRef == "" || in.EnterpriseRef == "" || in.Source == "" {
		return nil, fmt.Errorf("%w: visit_ref / enterprise_ref / source 均为必填", ErrValidation)
	}
	visit, found, err := s.loadVisit(in.VisitRef)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 参访行程 %s 未登记", ErrNotFound, in.VisitRef)
	}
	if visit.EnterpriseRef != in.EnterpriseRef {
		return nil, fmt.Errorf("%w: 提交企业与参访行程登记企业不一致", ErrValidation)
	}

	payload, err := json.Marshal(map[string]any{
		"title": in.Title, "scope": in.Scope, "conditions": in.Conditions,
	})
	if err != nil {
		return nil, err
	}

	result := &SubmitIntentResult{}
	var intentID string
	err = s.withTx(func() error {
		existing, found, err := s.findActiveIntent(in.EnterpriseRef, in.VisitRef)
		if err != nil {
			return err
		}
		now := s.timestamp()
		if found {
			intentID = existing.ID
			result.Created = false
		} else {
			intentID = newID("IN")
			latest, err := s.latestPolicyRevision(visit.ParkRef)
			if err != nil {
				return err
			}
			if err := s.db.Exec(
				`INSERT INTO intents(id, visit_ref, enterprise_ref, park_ref, title, scope, status, source,
					policy_confirmed_revision, created_at, updated_at)
				 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				intentID, in.VisitRef, in.EnterpriseRef, visit.ParkRef, in.Title, in.Scope,
				StatusDraft, in.Source, latest, now, now,
			); err != nil {
				return err
			}
			result.Created = true
			if err := s.recordEvent(intentID, EventIntentCreated, in.SubmittedBy, map[string]any{
				"source": in.Source, "park_ref": visit.ParkRef,
			}); err != nil {
				return err
			}
		}
		if err := s.db.Exec(
			"INSERT INTO source_records(intent_id, source, submitted_by, payload, created_at) VALUES (?,?,?,?,?)",
			intentID, in.Source, in.SubmittedBy, string(payload), now,
		); err != nil {
			return err
		}
		if !result.Created {
			if err := s.db.Exec("UPDATE intents SET updated_at = ? WHERE id = ?", now, intentID); err != nil {
				return err
			}
			if err := s.recordEvent(intentID, EventSourceAppended, in.SubmittedBy, map[string]any{
				"source": in.Source,
			}); err != nil {
				return err
			}
		}
		disputes, err := s.reconcileConditions(intentID, in.SubmittedBy)
		if err != nil {
			return err
		}
		result.Disputes = disputes
		return nil
	})
	if err != nil {
		return nil, err
	}
	intent, err := s.GetIntent(intentID)
	if err != nil {
		return nil, err
	}
	result.Intent = *intent
	return result, nil
}

// reconcileConditions 汇总该意向全部来源记录的条件字段，
// 对取值不一致的字段建立或更新分歧记录。
func (s *Service) reconcileConditions(intentID, actor string) ([]Dispute, error) {
	records, err := s.listSourceRecords(intentID)
	if err != nil {
		return nil, err
	}
	// field -> source -> value
	byField := map[string]map[string]any{}
	for _, rec := range records {
		conditions, _ := rec.Payload["conditions"].(map[string]any)
		for field, value := range conditions {
			if byField[field] == nil {
				byField[field] = map[string]any{}
			}
			byField[field][rec.Source] = value
		}
	}
	var raised []Dispute
	for field, values := range byField {
		distinct := map[string]bool{}
		for _, value := range values {
			distinct[canonical(value)] = true
		}
		if len(distinct) <= 1 {
			continue
		}
		dispute, err := s.upsertDispute(intentID, field, values, actor)
		if err != nil {
			return nil, err
		}
		if dispute != nil {
			raised = append(raised, *dispute)
		}
	}
	return raised, nil
}

func (s *Service) upsertDispute(intentID, field string, values map[string]any, actor string) (*Dispute, error) {
	valuesJSON, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	row, found, err := s.db.QueryOne(
		"SELECT id, status FROM disputes WHERE intent_id = ? AND field = ? AND status = 'open'",
		intentID, field,
	)
	if err != nil {
		return nil, err
	}
	if found {
		id := asString(row["id"])
		if err := s.db.Exec("UPDATE disputes SET values_json = ? WHERE id = ?", string(valuesJSON), id); err != nil {
			return nil, err
		}
		return &Dispute{ID: id, IntentID: intentID, Field: field, Values: values, Status: "open"}, nil
	}
	id := newID("DP")
	if err := s.db.Exec(
		"INSERT INTO disputes(id, intent_id, field, values_json, status, created_at) VALUES (?,?,?,?, 'open', ?)",
		id, intentID, field, string(valuesJSON), s.timestamp(),
	); err != nil {
		return nil, err
	}
	if err := s.recordEvent(intentID, EventDisputeRaised, actor, map[string]any{
		"dispute_id": id, "field": field, "values": values,
	}); err != nil {
		return nil, err
	}
	return &Dispute{ID: id, IntentID: intentID, Field: field, Values: values, Status: "open"}, nil
}

// AdjustScope 允许企业在意向公开前调整合作范围。
func (s *Service) AdjustScope(intentID string, actor Actor, scope string) error {
	if scope == "" {
		return fmt.Errorf("%w: scope 不能为空", ErrValidation)
	}
	if !actor.isEnterprise() {
		return fmt.Errorf("%w: 仅企业方可调整合作范围", ErrForbidden)
	}
	intent, found, err := s.loadIntent(intentID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: 合作意向 %s", ErrNotFound, intentID)
	}
	if actor.EnterpriseRef != intent.EnterpriseRef {
		return fmt.Errorf("%w: 仅意向所属企业可调整合作范围", ErrForbidden)
	}
	if intent.Status != StatusDraft {
		return fmt.Errorf("%w: 意向已公开，合作范围不可再调整", ErrConflict)
	}
	return s.withTx(func() error {
		if err := s.db.Exec(
			"UPDATE intents SET scope = ?, updated_at = ? WHERE id = ?",
			scope, s.timestamp(), intentID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventScopeAdjusted, actor.label(), map[string]any{
			"scope": scope,
		})
	})
}

// Publish 由所属园区经办人公开意向；公开后企业不可再调整范围。
func (s *Service) Publish(intentID string, actor Actor) error {
	intent, err := s.intentForHandler(intentID, actor)
	if err != nil {
		return err
	}
	if intent.Status != StatusDraft {
		return fmt.Errorf("%w: 仅未公开的意向可执行公开", ErrConflict)
	}
	return s.withTx(func() error {
		now := s.timestamp()
		if err := s.db.Exec(
			"UPDATE intents SET status = ?, published_at = ?, updated_at = ? WHERE id = ?",
			StatusPublished, now, now, intentID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventPublished, actor.label(), map[string]any{
			"park_ref": intent.ParkRef,
		})
	})
}

// AddFeedback 记录所属园区经办人的反馈。
func (s *Service) AddFeedback(intentID string, actor Actor, content string) error {
	if content == "" {
		return fmt.Errorf("%w: content 不能为空", ErrValidation)
	}
	intent, err := s.intentForHandler(intentID, actor)
	if err != nil {
		return err
	}
	return s.withTx(func() error {
		if err := s.db.Exec(
			"INSERT INTO feedbacks(intent_id, park_ref, handler_id, content, created_at) VALUES (?,?,?,?,?)",
			intentID, intent.ParkRef, actor.ID, content, s.timestamp(),
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventFeedbackAdded, actor.label(), map[string]any{
			"park_ref": intent.ParkRef, "content": content,
		})
	})
}

// AddContact 登记联系人，默认未获企业同意。
func (s *Service) AddContact(intentID string, actor Actor, name, role string) (*Contact, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name 不能为空", ErrValidation)
	}
	if _, err := s.intentForMember(intentID, actor); err != nil {
		return nil, err
	}
	now := s.timestamp()
	if err := s.withTx(func() error {
		if err := s.db.Exec(
			"INSERT INTO contacts(intent_id, name, role, consent_granted, created_at) VALUES (?,?,?,0,?)",
			intentID, name, role, now,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventContactAdded, actor.label(), map[string]any{
			"name": name, "role": role,
		})
	}); err != nil {
		return nil, err
	}
	row, found, err := s.db.QueryOne(
		"SELECT id FROM contacts WHERE intent_id = ? AND created_at = ? AND name = ? ORDER BY id DESC LIMIT 1",
		intentID, now, name,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("联系人写入后未找到")
	}
	id, _ := row["id"].(int64)
	return &Contact{ID: id, IntentID: intentID, Name: name, Role: role, CreatedAt: now}, nil
}

// GrantConsent 由企业方授权联系人可出现在对外合作清单。
func (s *Service) GrantConsent(intentID string, contactID int64, actor Actor, consentRef string) error {
	if consentRef == "" {
		return fmt.Errorf("%w: consent_ref 不能为空", ErrValidation)
	}
	if !actor.isEnterprise() {
		return fmt.Errorf("%w: 仅企业方可授予联系人同意", ErrForbidden)
	}
	intent, found, err := s.loadIntent(intentID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: 合作意向 %s", ErrNotFound, intentID)
	}
	if actor.EnterpriseRef != intent.EnterpriseRef {
		return fmt.Errorf("%w: 仅意向所属企业可授予联系人同意", ErrForbidden)
	}
	row, found, err := s.db.QueryOne(
		"SELECT id, name FROM contacts WHERE id = ? AND intent_id = ?", contactID, intentID,
	)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: 联系人 %d", ErrNotFound, contactID)
	}
	return s.withTx(func() error {
		if err := s.db.Exec(
			"UPDATE contacts SET consent_granted = 1, consent_ref = ? WHERE id = ?",
			consentRef, contactID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventConsentGranted, actor.label(), map[string]any{
			"contact_id": contactID, "name": asString(row["name"]), "consent_ref": consentRef,
		})
	})
}

// ---- 内部读取助手 ----

func (s *Service) loadVisit(visitRef string) (*Visit, bool, error) {
	row, found, err := s.db.QueryOne("SELECT * FROM visits WHERE visit_ref = ?", visitRef)
	if err != nil || !found {
		return nil, found, err
	}
	return &Visit{
		VisitRef:      asString(row["visit_ref"]),
		EnterpriseRef: asString(row["enterprise_ref"]),
		ParkRef:       asString(row["park_ref"]),
		VisitedAt:     asString(row["visited_at"]),
		Summary:       asString(row["summary"]),
		CreatedAt:     asString(row["created_at"]),
	}, true, nil
}

func (s *Service) findActiveIntent(enterpriseRef, visitRef string) (*Intent, bool, error) {
	row, found, err := s.db.QueryOne(
		`SELECT id FROM intents WHERE enterprise_ref = ? AND visit_ref = ? AND status != ?
		 ORDER BY created_at LIMIT 1`,
		enterpriseRef, visitRef, StatusClosed,
	)
	if err != nil || !found {
		return nil, found, err
	}
	intent, err := s.GetIntent(asString(row["id"]))
	if err != nil {
		return nil, false, err
	}
	return intent, true, nil
}

func (s *Service) loadIntent(intentID string) (*Intent, bool, error) {
	row, found, err := s.db.QueryOne("SELECT * FROM intents WHERE id = ?", intentID)
	if err != nil || !found {
		return nil, found, err
	}
	intent, err := s.rowToIntent(row)
	if err != nil {
		return nil, false, err
	}
	return intent, true, nil
}

func (s *Service) rowToIntent(row sqlitedb.Row) (*Intent, error) {
	intent := &Intent{
		ID:                      asString(row["id"]),
		VisitRef:                asString(row["visit_ref"]),
		EnterpriseRef:           asString(row["enterprise_ref"]),
		ParkRef:                 asString(row["park_ref"]),
		Title:                   asString(row["title"]),
		Scope:                   asString(row["scope"]),
		Status:                  asString(row["status"]),
		Source:                  asString(row["source"]),
		PolicyConfirmedRevision: asString(row["policy_confirmed_revision"]),
		CreatedAt:               asString(row["created_at"]),
		UpdatedAt:               asString(row["updated_at"]),
	}
	if v, ok := row["published_at"].(string); ok && v != "" {
		intent.PublishedAt = &v
	}
	if v, ok := row["signed_at"].(string); ok && v != "" {
		intent.SignedAt = &v
	}
	latest, err := s.latestPolicyRevision(intent.ParkRef)
	if err != nil {
		return nil, err
	}
	intent.PolicyCurrentRevision = latest
	return intent, nil
}

// GetIntent 返回意向当前状态。
func (s *Service) GetIntent(intentID string) (*Intent, error) {
	intent, found, err := s.loadIntent(intentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 合作意向 %s", ErrNotFound, intentID)
	}
	return intent, nil
}

// intentForHandler 校验经办人属于意向当前所在园区。
func (s *Service) intentForHandler(intentID string, actor Actor) (*Intent, error) {
	if !actor.isHandler() {
		return nil, fmt.Errorf("%w: 需要经办人身份", ErrForbidden)
	}
	intent, found, err := s.loadIntent(intentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 合作意向 %s", ErrNotFound, intentID)
	}
	if intent.ParkRef != actor.ParkRef {
		return nil, fmt.Errorf("%w: 意向属于园区 %s", ErrForbidden, intent.ParkRef)
	}
	return intent, nil
}

// intentForMember 允许所属园区经办人或意向所属企业操作。
func (s *Service) intentForMember(intentID string, actor Actor) (*Intent, error) {
	intent, found, err := s.loadIntent(intentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 合作意向 %s", ErrNotFound, intentID)
	}
	switch {
	case actor.isHandler() && actor.ParkRef == intent.ParkRef:
		return intent, nil
	case actor.isEnterprise() && actor.EnterpriseRef == intent.EnterpriseRef:
		return intent, nil
	default:
		return nil, fmt.Errorf("%w: 仅所属园区经办人或意向企业可操作", ErrForbidden)
	}
}

func (s *Service) listSourceRecords(intentID string) ([]SourceRecord, error) {
	rows, err := s.db.Query(
		"SELECT * FROM source_records WHERE intent_id = ? ORDER BY id", intentID,
	)
	if err != nil {
		return nil, err
	}
	records := make([]SourceRecord, 0, len(rows))
	for _, row := range rows {
		var payload map[string]any
		if err := json.Unmarshal([]byte(asString(row["payload"])), &payload); err != nil {
			return nil, err
		}
		id, _ := row["id"].(int64)
		records = append(records, SourceRecord{
			ID:          id,
			IntentID:    asString(row["intent_id"]),
			Source:      asString(row["source"]),
			SubmittedBy: asString(row["submitted_by"]),
			Payload:     payload,
			CreatedAt:   asString(row["created_at"]),
		})
	}
	return records, nil
}
