package leads

import (
	"fmt"
)

// Policy 是园区优惠政策的一个版本。
type Policy struct {
	ParkRef     string `json:"park_ref"`
	Revision    string `json:"revision"`
	Content     string `json:"content"`
	EffectiveAt string `json:"effective_at"`
	CreatedAt   string `json:"created_at"`
}

// RegisterPolicy 登记园区优惠政策版本；同园区同版本号重复登记视为冲突。
func (s *Service) RegisterPolicy(p Policy) error {
	if p.ParkRef == "" || p.Revision == "" || p.EffectiveAt == "" {
		return fmt.Errorf("%w: park_ref / revision / effective_at 均为必填", ErrValidation)
	}
	_, found, err := s.db.QueryOne(
		"SELECT revision FROM policies WHERE park_ref = ? AND revision = ?", p.ParkRef, p.Revision,
	)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("%w: 园区 %s 政策版本 %s 已存在", ErrConflict, p.ParkRef, p.Revision)
	}
	return s.db.Exec(
		"INSERT INTO policies(park_ref, revision, content, effective_at, created_at) VALUES (?,?,?,?,?)",
		p.ParkRef, p.Revision, p.Content, p.EffectiveAt, s.timestamp(),
	)
}

// latestPolicyRevision 返回园区当前生效的最新政策版本；无政策时为空串。
func (s *Service) latestPolicyRevision(parkRef string) (string, error) {
	row, found, err := s.db.QueryOne(
		`SELECT revision FROM policies WHERE park_ref = ?
		 ORDER BY effective_at DESC, revision DESC LIMIT 1`,
		parkRef,
	)
	if err != nil || !found {
		return "", err
	}
	return asString(row["revision"]), nil
}

// ReconfirmPolicy 在意向公开后、签约前重新确认当前优惠政策版本。
// 所属园区经办人或意向企业均可发起。
func (s *Service) ReconfirmPolicy(intentID string, actor Actor) (*Intent, error) {
	intent, err := s.intentForMember(intentID, actor)
	if err != nil {
		return nil, err
	}
	if intent.Status != StatusPublished {
		return nil, fmt.Errorf("%w: 仅已公开且未签约的意向需要政策确认", ErrConflict)
	}
	latest, err := s.latestPolicyRevision(intent.ParkRef)
	if err != nil {
		return nil, err
	}
	previous := intent.PolicyConfirmedRevision
	err = s.withTx(func() error {
		if err := s.db.Exec(
			"UPDATE intents SET policy_confirmed_revision = ?, updated_at = ? WHERE id = ?",
			latest, s.timestamp(), intentID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventPolicyReconfirmed, actor.label(), map[string]any{
			"from_revision": previous, "to_revision": latest,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.GetIntent(intentID)
}

// Sign 完成签约。要求：意向已公开、分歧均已处理、
// 且已确认的优惠政策版本与园区当前版本一致。
func (s *Service) Sign(intentID string, actor Actor) (*Intent, error) {
	intent, err := s.intentForHandler(intentID, actor)
	if err != nil {
		return nil, err
	}
	if intent.Status != StatusPublished {
		return nil, fmt.Errorf("%w: 仅已公开的意向可签约", ErrConflict)
	}
	row, found, err := s.db.QueryOne(
		"SELECT COUNT(*) AS n FROM disputes WHERE intent_id = ? AND status = 'open'", intentID,
	)
	if err != nil {
		return nil, err
	}
	if found {
		if n, _ := row["n"].(int64); n > 0 {
			return nil, fmt.Errorf("%w: 还有 %d 条分歧未处理", ErrDisputesOpen, n)
		}
	}
	latest, err := s.latestPolicyRevision(intent.ParkRef)
	if err != nil {
		return nil, err
	}
	if intent.PolicyConfirmedRevision != latest {
		return nil, fmt.Errorf("%w: 已确认版本 %q，当前版本 %q",
			ErrPolicyStale, intent.PolicyConfirmedRevision, latest)
	}
	err = s.withTx(func() error {
		now := s.timestamp()
		if err := s.db.Exec(
			"UPDATE intents SET status = ?, signed_at = ?, updated_at = ? WHERE id = ?",
			StatusSigned, now, now, intentID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventSigned, actor.label(), map[string]any{
			"policy_revision": latest,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.GetIntent(intentID)
}
