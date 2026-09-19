// Package domain 实现合作线索归口的业务规则：
// 多来源线索合并到同一意向、企业授权与联系人公开同意、
// 园区经办人数据隔离、跨园转交回执、政策版本重新确认、
// 实地对接与分歧处理的全程追溯。
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// 意向状态
const (
	StatusDraft           = "DRAFT"            // 线索归口，尚未完成授权与政策确认
	StatusPolicyConfirmed = "POLICY_CONFIRMED" // 企业已按当前政策版本确认
	StatusSigned          = "SIGNED"           // 已签约（终态）
	StatusClosed          = "CLOSED"           // 终止（终态）
)

// 线索来源
const (
	SourceEnterprise = "enterprise" // 台商
	SourceYouth      = "youth"      // 台青
	SourceDistrict   = "district"   // 区县招商人员
)

// 转交状态
const (
	TransferPending  = "PENDING"
	TransferAccepted = "ACCEPTED"
	TransferRejected = "REJECTED"
)

const (
	DisputeOpen     = "OPEN"
	DisputeResolved = "RESOLVED"
)

type Park struct {
	Ref       string    `json:"park_ref"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Operator struct {
	ID        string    `json:"operator_id"`
	ParkRef   string    `json:"park_ref"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type Enterprise struct {
	Ref       string    `json:"enterprise_ref"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Contact struct {
	Name          string `json:"name"`
	RoleTag       string `json:"role_tag"`
	Channel       string `json:"channel"`
	PublicConsent bool   `json:"public_consent"`
}

type Authorization struct {
	ID          int64          `json:"-"`
	ConsentRef  string         `json:"consent_ref"`
	Scope       map[string]any `json:"scope"`
	EffectiveAt time.Time      `json:"effective_at"`
	CreatedAt   time.Time      `json:"created_at"`
	Contacts    []Contact      `json:"contacts"`
}

type Visit struct {
	EnterpriseRef string    `json:"enterprise_ref"`
	VisitRef      string    `json:"visit_ref"`
	ParkRef       string    `json:"park_ref"`
	OccurredAt    time.Time `json:"occurred_at"`
	Note          string    `json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}

type SourceRecord struct {
	ID              int64          `json:"-"`
	Source          string         `json:"source"`
	SourceRecordRef string         `json:"source_record_ref"`
	SubmitterRef    string         `json:"submitter_ref"`
	ParkRef         string         `json:"park_ref"`
	Payload         map[string]any `json:"payload"`
	Conflicts       []string       `json:"conflicts,omitempty"`
	ReceivedAt      time.Time      `json:"received_at"`
}

type Intent struct {
	ID                     string         `json:"intent_id"`
	EnterpriseRef          string         `json:"enterprise_ref"`
	VisitRef               string         `json:"visit_ref"`
	CurrentParkRef         string         `json:"current_park_ref"`
	Status                 string         `json:"status"`
	Title                  string         `json:"title"`
	Conditions             map[string]any `json:"conditions"`
	ConsentRef             string         `json:"consent_ref,omitempty"`
	ConfirmedPolicyVersion string         `json:"confirmed_policy_version,omitempty"`
	HasConflicts           bool           `json:"has_conflicts"`
	Published              bool           `json:"published"`
	PublishedAt            *time.Time     `json:"published_at,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

type PolicyRevision struct {
	Version     string         `json:"version"`
	Title       string         `json:"title"`
	Detail      map[string]any `json:"detail"`
	EffectiveAt time.Time      `json:"effective_at"`
	CreatedAt   time.Time      `json:"created_at"`
}

type PolicyConfirmation struct {
	IntentID    string    `json:"intent_id"`
	Version     string    `json:"version"`
	ConsentRef  string    `json:"consent_ref"`
	OperatorID  string    `json:"operator_id"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}

type Transfer struct {
	TransferID     string     `json:"transfer_id"`
	ReceiptNo      string     `json:"receipt_no"`
	IntentID       string     `json:"intent_id"`
	FromParkRef    string     `json:"from_park_ref"`
	ToParkRef      string     `json:"to_park_ref"`
	OperatorID     string     `json:"operator_id"`
	PreviousStatus string     `json:"previous_status"`
	Status         string     `json:"status"`
	Note           string     `json:"note"`
	TransferredAt  time.Time  `json:"transferred_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AckOperatorID  string     `json:"ack_operator_id,omitempty"`
	RejectReason   string     `json:"reject_reason,omitempty"`
}

type Feedback struct {
	ID         int64     `json:"-"`
	ParkRef    string    `json:"park_ref"`
	OperatorID string    `json:"operator_id"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

type Engagement struct {
	EngagementID string    `json:"engagement_id"`
	ParkRef      string    `json:"park_ref"`
	OperatorID   string    `json:"operator_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Location     string    `json:"location"`
	Summary      string    `json:"summary"`
	CreatedAt    time.Time `json:"created_at"`
	Disputes     []Dispute `json:"disputes,omitempty"`
}

type Dispute struct {
	DisputeID    string     `json:"dispute_id"`
	IntentID     string     `json:"-"`
	EngagementID string     `json:"engagement_id,omitempty"`
	RaisedBy     string     `json:"raised_by"`
	Issue        string     `json:"issue"`
	Status       string     `json:"status"`
	Resolution   string     `json:"resolution,omitempty"`
	ResolverID   string     `json:"resolver_operator_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

type Event struct {
	EventID    int64          `json:"event_id"`
	Type       string         `json:"type"`
	OperatorID string         `json:"operator_id,omitempty"`
	FromStatus string         `json:"from_status,omitempty"`
	ToStatus   string         `json:"to_status,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Timeline 是一条意向从归口到签约的完整追溯视图。
type Timeline struct {
	Intent         Intent               `json:"intent"`
	Visit          Visit                `json:"visit"`
	Enterprise     Enterprise           `json:"enterprise"`
	Authorizations []Authorization      `json:"authorizations"`
	Sources        []SourceRecord       `json:"sources"`
	Confirmations  []PolicyConfirmation `json:"confirmations"`
	Transfers      []Transfer           `json:"transfers"`
	Feedbacks      []Feedback           `json:"feedbacks"`
	Engagements    []Engagement         `json:"engagements"`
	Events         []Event              `json:"events"`
}

// PublicEntry 是对外合作清单中的一条：只含已公开意向与
// 已获得公开同意的联系人。
type PublicEntry struct {
	IntentID       string         `json:"intent_id"`
	EnterpriseRef  string         `json:"enterprise_ref"`
	EnterpriseName string         `json:"enterprise_name"`
	ParkRef        string         `json:"park_ref"`
	ParkName       string         `json:"park_name"`
	Title          string         `json:"title"`
	Conditions     map[string]any `json:"conditions,omitempty"`
	Status         string         `json:"status"`
	Contacts       []Contact      `json:"contacts"`
	PublishedAt    time.Time      `json:"published_at"`
}

// ---- 小工具 ----

func now() time.Time { return time.Now().UTC() }

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// rand 失败在正常运行环境不会发生；退化为时间戳保证仍有标识。
		return prefix + "-" + time.Now().UTC().Format("20060102T150405")
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

func marshalJSON(value map[string]any) (string, error) {
	if value == nil {
		value = map[string]any{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalJSON(raw string) (map[string]any, error) {
	value := map[string]any{}
	if raw == "" {
		return value, nil
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, err
	}
	return value, nil
}

func marshalStringSlice(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalStringSlice(raw string) []string {
	values := []string{}
	if raw == "" {
		return values
	}
	_ = json.Unmarshal([]byte(raw), &values)
	return values
}

// canonicalJSON 用于比较两份 JSON 是否表达相同内容。
func canonicalJSON(value map[string]any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}
