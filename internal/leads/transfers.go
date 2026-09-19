package leads

import (
	"encoding/json"
	"fmt"
)

// Transfer 是一次跨区转交；接收方确认后留下回执。
type Transfer struct {
	ID          string   `json:"id"`
	IntentID    string   `json:"intent_id"`
	FromPark    string   `json:"from_park"`
	ToPark      string   `json:"to_park"`
	InitiatedBy string   `json:"initiated_by"`
	Status      string   `json:"status"`
	Receipt     *Receipt `json:"receipt,omitempty"`
	CreatedAt   string   `json:"created_at"`
	ReceivedAt  *string  `json:"received_at,omitempty"`
}

// Receipt 是接收方确认跨区转交时留下的回执。
type Receipt struct {
	ReceivedBy string `json:"received_by"`
	ParkRef    string `json:"park_ref"`
	ReceivedAt string `json:"received_at"`
	Note       string `json:"note,omitempty"`
}

// InitiateTransfer 由意向当前所在园区的经办人发起跨区转交。
func (s *Service) InitiateTransfer(intentID string, actor Actor, toPark, note string) (*Transfer, error) {
	if toPark == "" {
		return nil, fmt.Errorf("%w: to_park 不能为空", ErrValidation)
	}
	intent, err := s.intentForHandler(intentID, actor)
	if err != nil {
		return nil, err
	}
	if intent.Status == StatusSigned || intent.Status == StatusClosed {
		return nil, fmt.Errorf("%w: 已签约或已终止的意向不可转交", ErrConflict)
	}
	if toPark == intent.ParkRef {
		return nil, fmt.Errorf("%w: 接收园区与当前园区相同", ErrValidation)
	}
	row, found, err := s.db.QueryOne(
		"SELECT id FROM transfers WHERE intent_id = ? AND status = 'pending'", intentID,
	)
	if err != nil {
		return nil, err
	}
	if found {
		return nil, fmt.Errorf("%w: 存在待接收的转交 %s", ErrConflict, asString(row["id"]))
	}

	transfer := &Transfer{
		ID:          newID("TR"),
		IntentID:    intentID,
		FromPark:    intent.ParkRef,
		ToPark:      toPark,
		InitiatedBy: actor.ID,
		Status:      "pending",
		CreatedAt:   s.timestamp(),
	}
	err = s.withTx(func() error {
		if err := s.db.Exec(
			`INSERT INTO transfers(id, intent_id, from_park, to_park, initiated_by, status, created_at)
			 VALUES (?,?,?,?,?,'pending',?)`,
			transfer.ID, intentID, transfer.FromPark, toPark, actor.ID, transfer.CreatedAt,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventTransferInitiated, actor.label(), map[string]any{
			"transfer_id": transfer.ID, "from_park": transfer.FromPark, "to_park": toPark, "note": note,
		})
	})
	if err != nil {
		return nil, err
	}
	return transfer, nil
}

// AcceptTransfer 由接收园区的经办人确认接收，留下回执并变更意向归属园区。
func (s *Service) AcceptTransfer(transferID string, actor Actor, note string) (*Transfer, error) {
	if !actor.isHandler() {
		return nil, fmt.Errorf("%w: 需要经办人身份", ErrForbidden)
	}
	row, found, err := s.db.QueryOne("SELECT * FROM transfers WHERE id = ?", transferID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 转交 %s", ErrNotFound, transferID)
	}
	if asString(row["status"]) != "pending" {
		return nil, fmt.Errorf("%w: 转交 %s 已被接收", ErrConflict, transferID)
	}
	if asString(row["to_park"]) != actor.ParkRef {
		return nil, fmt.Errorf("%w: 转交接收园区为 %s", ErrForbidden, asString(row["to_park"]))
	}
	intentID := asString(row["intent_id"])
	receipt := Receipt{
		ReceivedBy: actor.ID,
		ParkRef:    actor.ParkRef,
		ReceivedAt: s.timestamp(),
		Note:       note,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	err = s.withTx(func() error {
		if err := s.db.Exec(
			"UPDATE transfers SET status = 'received', receipt_json = ?, received_at = ? WHERE id = ?",
			string(receiptJSON), receipt.ReceivedAt, transferID,
		); err != nil {
			return err
		}
		if err := s.db.Exec(
			"UPDATE intents SET park_ref = ?, updated_at = ? WHERE id = ?",
			actor.ParkRef, receipt.ReceivedAt, intentID,
		); err != nil {
			return err
		}
		return s.recordEvent(intentID, EventTransferReceived, actor.label(), map[string]any{
			"transfer_id": transferID,
			"from_park":   asString(row["from_park"]),
			"to_park":     actor.ParkRef,
			"receipt":     receipt,
		})
	})
	if err != nil {
		return nil, err
	}
	return s.loadTransfer(transferID)
}

func (s *Service) loadTransfer(transferID string) (*Transfer, error) {
	row, found, err := s.db.QueryOne("SELECT * FROM transfers WHERE id = ?", transferID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: 转交 %s", ErrNotFound, transferID)
	}
	return rowToTransfer(row)
}

func rowToTransfer(row map[string]any) (*Transfer, error) {
	transfer := &Transfer{
		ID:          asString(row["id"]),
		IntentID:    asString(row["intent_id"]),
		FromPark:    asString(row["from_park"]),
		ToPark:      asString(row["to_park"]),
		InitiatedBy: asString(row["initiated_by"]),
		Status:      asString(row["status"]),
		CreatedAt:   asString(row["created_at"]),
	}
	if v, ok := row["received_at"].(string); ok && v != "" {
		transfer.ReceivedAt = &v
	}
	if raw := asString(row["receipt_json"]); raw != "" {
		var receipt Receipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			return nil, err
		}
		transfer.Receipt = &receipt
	}
	return transfer, nil
}
