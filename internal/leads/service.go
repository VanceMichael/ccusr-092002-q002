// Package leads 实现合作线索归口的领域规则：
// 多来源提交归并到同一意向、未公开前企业可调整合作范围、
// 经办人仅限所属园区、跨区转交留回执、政策版本变化需重新确认、
// 对外清单过滤未授权联系人，并以事件流支撑全程追溯。
package leads

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/sqlitedb"
	"github.com/vancemichael/092002-industrial-visit-intent/migrations"
)

var (
	// ErrValidation 提交内容缺少必要字段。
	ErrValidation = errors.New("提交内容不完整")
	// ErrNotFound 引用的记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrForbidden 经办人试图处理非所属园区的材料。
	ErrForbidden = errors.New("经办人只能处理所属园区的材料")
	// ErrConflict 当前状态不允许该操作（如意向已公开）。
	ErrConflict = errors.New("当前状态不允许该操作")
	// ErrPolicyStale 优惠政策版本已变化，签约前需重新确认。
	ErrPolicyStale = errors.New("优惠政策版本已变化，签约前需重新确认")
	// ErrDisputesOpen 存在未处理的分歧，不能进入签约。
	ErrDisputesOpen = errors.New("存在未处理的分歧")
)

// 意向状态机：draft → published → signed；closed 为终止。
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusSigned    = "signed"
	StatusClosed    = "closed"
)

// 事件类型，写入事件流供追溯。
const (
	EventIntentCreated     = "intent_created"
	EventSourceAppended    = "source_appended"
	EventScopeAdjusted     = "scope_adjusted"
	EventPublished         = "published"
	EventFeedbackAdded     = "feedback_added"
	EventContactAdded      = "contact_added"
	EventConsentGranted    = "consent_granted"
	EventEngagement        = "engagement_recorded"
	EventDisputeRaised     = "dispute_raised"
	EventDisputeResolved   = "dispute_resolved"
	EventTransferInitiated = "transfer_initiated"
	EventTransferReceived  = "transfer_received"
	EventPolicyReconfirmed = "policy_reconfirmed"
	EventSigned            = "signed"
)

// Actor 是请求身份。Kind 为 handler（经办人）或 enterprise（企业方）。
type Actor struct {
	Kind          string
	ID            string
	ParkRef       string
	EnterpriseRef string
}

func (a Actor) isHandler() bool { return a.Kind == "handler" && a.ID != "" && a.ParkRef != "" }
func (a Actor) isEnterprise() bool {
	return a.Kind == "enterprise" && a.EnterpriseRef != ""
}

func (a Actor) label() string {
	if a.ID != "" {
		return a.ID
	}
	return a.EnterpriseRef
}

// Service 提供合作线索的全部用例。
type Service struct {
	db  *sqlitedb.DB
	now func() time.Time
}

// NewService 创建服务；now 为 nil 时使用系统时间。
func NewService(db *sqlitedb.DB, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, now: now}
}

// Migrate 按文件名字序应用未执行过的迁移脚本。
func Migrate(db *sqlitedb.DB) error {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("读取迁移目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	applied := map[string]bool{}
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err == nil {
		for _, row := range rows {
			if v, ok := row["version"].(string); ok {
				applied[v] = true
			}
		}
	}
	for _, name := range names {
		version := name[:len(name)-len(".sql")]
		if applied[version] {
			continue
		}
		script, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s 失败: %w", name, err)
		}
		if err := db.ExecScript("BEGIN;\n" + string(script) + "\nCOMMIT;"); err != nil {
			return fmt.Errorf("应用迁移 %s 失败: %w", name, err)
		}
	}
	return nil
}

func (s *Service) timestamp() string {
	return s.now().UTC().Format("2006-01-02T15:04:05Z07:00")
}

func newID(prefix string) string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return prefix + "-" + hex.EncodeToString(buf)
}

// withTx 在单事务内执行 fn；Service 串行化写事务，避免嵌套 BEGIN。
func (s *Service) withTx(fn func() error) (err error) {
	if err = s.db.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = s.db.Exec("ROLLBACK")
		}
	}()
	if err = fn(); err != nil {
		return err
	}
	return s.db.Exec("COMMIT")
}

func (s *Service) recordEvent(intentID, kind, actor string, detail any) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return s.db.Exec(
		"INSERT INTO events(intent_id, kind, actor, detail_json, created_at) VALUES (?,?,?,?,?)",
		intentID, kind, actor, string(payload), s.timestamp(),
	)
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// canonical 把任意 JSON 值规范化为可比较的字符串。
func canonical(v any) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(buf)
}
