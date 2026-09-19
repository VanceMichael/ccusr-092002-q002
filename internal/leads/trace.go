package leads

import (
	"encoding/json"
	"fmt"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/sqlitedb"
)

// Event 是事件流中的一条留痕。
type Event struct {
	ID        int64          `json:"id"`
	IntentID  string         `json:"intent_id"`
	Kind      string         `json:"kind"`
	Actor     string         `json:"actor"`
	Detail    map[string]any `json:"detail"`
	CreatedAt string         `json:"created_at"`
}

// RecordEngagement 记录一次实地对接。
func (s *Service) RecordEngagement(intentID string, actor Actor, location, summary, occurredAt string) error {
	if summary == "" || occurredAt == "" {
		return fmt.Errorf("%w: summary / occurred_at 均为必填", ErrValidation)
	}
	intent, err := s.intentForHandler(intentID, actor)
	if err != nil {
		return err
	}
	return s.recordEvent(intentID, EventEngagement, actor.label(), map[string]any{
		"park_ref": intent.ParkRef, "location": location,
		"summary": summary, "occurred_at": occurredAt,
	})
}

// ResolveDispute 由所属园区经办人处理一条分歧。
func (s *Service) ResolveDispute(intentID, disputeID string, actor Actor, resolution string) error {
	if resolution == "" {
		return fmt.Errorf("%w: resolution 不能为空", ErrValidation)
	}
	if _, err := s.intentForHandler(intentID, actor); err != nil {
		return err
	}
	row, found, err := s.db.QueryOne(
		"SELECT id, field, status FROM disputes WHERE id = ? AND intent_id = ?", disputeID, intentID,
	)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: 分歧 %s", ErrNotFound, disputeID)
	}
	if asString(row["status"]) != "open" {
		return fmt.Errorf("%w: 分歧 %s 已处理", ErrConflict, disputeID)
	}
	return s.withTx(func() error {
		if err := s.db.Exec(
			"UPDATE disputes SET status = 'resolved', resolution = ?, resolved_by = ?, resolved_at = ? WHERE id = ?",
			resolution, actor.ID, s.timestamp(), disputeID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventDisputeResolved, actor.label(), map[string]any{
			"dispute_id": disputeID, "field": asString(row["field"]), "resolution": resolution,
		})
	})
}

// Trace 是从项目状态到每次对接与分歧处理的完整追溯。
type Trace struct {
	Intent        Intent         `json:"intent"`
	Visit         *Visit         `json:"visit,omitempty"`
	SourceRecords []SourceRecord `json:"source_records"`
	Disputes      []Dispute      `json:"disputes"`
	Transfers     []Transfer     `json:"transfers"`
	Events        []Event        `json:"events"`
}

// Trace 汇总意向的全部留痕，按时间排序。
func (s *Service) Trace(intentID string) (*Trace, error) {
	intent, err := s.GetIntent(intentID)
	if err != nil {
		return nil, err
	}
	trace := &Trace{Intent: *intent}
	if visit, found, err := s.loadVisit(intent.VisitRef); err != nil {
		return nil, err
	} else if found {
		trace.Visit = visit
	}
	if trace.SourceRecords, err = s.listSourceRecords(intentID); err != nil {
		return nil, err
	}
	if trace.Disputes, err = s.listDisputes(intentID); err != nil {
		return nil, err
	}
	if trace.Transfers, err = s.listTransfers(intentID); err != nil {
		return nil, err
	}
	if trace.Events, err = s.listEvents(intentID); err != nil {
		return nil, err
	}
	return trace, nil
}

func (s *Service) listDisputes(intentID string) ([]Dispute, error) {
	rows, err := s.db.Query("SELECT * FROM disputes WHERE intent_id = ? ORDER BY created_at, id", intentID)
	if err != nil {
		return nil, err
	}
	disputes := make([]Dispute, 0, len(rows))
	for _, row := range rows {
		var values map[string]any
		if err := json.Unmarshal([]byte(asString(row["values_json"])), &values); err != nil {
			return nil, err
		}
		dispute := Dispute{
			ID:         asString(row["id"]),
			IntentID:   asString(row["intent_id"]),
			Field:      asString(row["field"]),
			Values:     values,
			Status:     asString(row["status"]),
			Resolution: asString(row["resolution"]),
			ResolvedBy: asString(row["resolved_by"]),
			CreatedAt:  asString(row["created_at"]),
		}
		if v, ok := row["resolved_at"].(string); ok && v != "" {
			dispute.ResolvedAt = &v
		}
		disputes = append(disputes, dispute)
	}
	return disputes, nil
}

func (s *Service) listTransfers(intentID string) ([]Transfer, error) {
	rows, err := s.db.Query("SELECT * FROM transfers WHERE intent_id = ? ORDER BY created_at, id", intentID)
	if err != nil {
		return nil, err
	}
	transfers := make([]Transfer, 0, len(rows))
	for _, row := range rows {
		transfer, err := rowToTransfer(row)
		if err != nil {
			return nil, err
		}
		transfers = append(transfers, *transfer)
	}
	return transfers, nil
}

func (s *Service) listEvents(intentID string) ([]Event, error) {
	rows, err := s.db.Query("SELECT * FROM events WHERE intent_id = ? ORDER BY id", intentID)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		var detail map[string]any
		if err := json.Unmarshal([]byte(asString(row["detail_json"])), &detail); err != nil {
			return nil, err
		}
		id, _ := row["id"].(int64)
		events = append(events, Event{
			ID:        id,
			IntentID:  asString(row["intent_id"]),
			Kind:      asString(row["kind"]),
			Actor:     asString(row["actor"]),
			Detail:    detail,
			CreatedAt: asString(row["created_at"]),
		})
	}
	return events, nil
}

// PublicIntent 是对外合作清单中的一项，只含企业已同意公开的联系人。
type PublicIntent struct {
	ID             string    `json:"id"`
	EnterpriseRef  string    `json:"enterprise_ref"`
	ParkRef        string    `json:"park_ref"`
	Title          string    `json:"title"`
	Scope          string    `json:"scope"`
	Status         string    `json:"status"`
	PolicyRevision string    `json:"policy_revision"`
	PublishedAt    *string   `json:"published_at,omitempty"`
	Contacts       []Contact `json:"contacts"`
}

// PublicList 返回对外合作清单：仅已公开或已签约的意向，
// 且联系人只保留企业已授权同意的。
func (s *Service) PublicList() ([]PublicIntent, error) {
	rows, err := s.db.Query(
		`SELECT * FROM intents WHERE status IN (?, ?) ORDER BY published_at, id`,
		StatusPublished, StatusSigned,
	)
	if err != nil {
		return nil, err
	}
	items := make([]PublicIntent, 0, len(rows))
	for _, row := range rows {
		intent, err := s.rowToIntent(row)
		if err != nil {
			return nil, err
		}
		contacts, err := s.listConsentedContacts(intent.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, PublicIntent{
			ID:             intent.ID,
			EnterpriseRef:  intent.EnterpriseRef,
			ParkRef:        intent.ParkRef,
			Title:          intent.Title,
			Scope:          intent.Scope,
			Status:         intent.Status,
			PolicyRevision: intent.PolicyConfirmedRevision,
			PublishedAt:    intent.PublishedAt,
			Contacts:       contacts,
		})
	}
	return items, nil
}

func (s *Service) listConsentedContacts(intentID string) ([]Contact, error) {
	rows, err := s.db.Query(
		"SELECT * FROM contacts WHERE intent_id = ? AND consent_granted = 1 ORDER BY id", intentID,
	)
	if err != nil {
		return nil, err
	}
	return rowsToContacts(rows), nil
}

// ListContacts 返回意向的全部联系人（内部视图，含未授权）。
func (s *Service) ListContacts(intentID string) ([]Contact, error) {
	rows, err := s.db.Query(
		"SELECT * FROM contacts WHERE intent_id = ? ORDER BY id", intentID,
	)
	if err != nil {
		return nil, err
	}
	return rowsToContacts(rows), nil
}

func rowsToContacts(rows []sqlitedb.Row) []Contact {
	contacts := make([]Contact, 0, len(rows))
	for _, row := range rows {
		id, _ := row["id"].(int64)
		granted, _ := row["consent_granted"].(int64)
		contacts = append(contacts, Contact{
			ID:             id,
			IntentID:       asString(row["intent_id"]),
			Name:           asString(row["name"]),
			Role:           asString(row["role"]),
			ConsentGranted: granted == 1,
			ConsentRef:     asString(row["consent_ref"]),
			CreatedAt:      asString(row["created_at"]),
		})
	}
	return contacts
}
