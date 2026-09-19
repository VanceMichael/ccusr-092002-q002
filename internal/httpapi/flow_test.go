package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func do(t *testing.T, h http.Handler, method, path, operator string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码请求: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if operator != "" {
		req.Header.Set("X-Operator-ID", operator)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func mustCode(t *testing.T, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("状态码 = %d, 期望 %d, 响应 %v", got, want, body)
	}
}

// 端到端：登记目录 → 三方线索归口并暴露冲突 → 授权与确认 → 转交接收
// → 实地对接与分歧 → 公开清单只含同意联系人；无鉴权头被拒绝。
func TestEndToEndFlow(t *testing.T) {
	h := newTestRouter(t)

	// 缺少经办人头被拒。
	if code, _ := do(t, h, http.MethodGet, "/v1/intents", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("无鉴权头应 401, 实际 %d", code)
	}

	for _, p := range []map[string]any{
		{"path": "/admin/parks", "operator": "", "body": map[string]any{"park_ref": "PARK-A", "name": "A园"}},
		{"path": "/admin/parks", "operator": "", "body": map[string]any{"park_ref": "PARK-B", "name": "B园"}},
		{"path": "/admin/enterprises", "operator": "", "body": map[string]any{"enterprise_ref": "ORG-1", "name": "台企"}},
		{"path": "/admin/operators", "operator": "", "body": map[string]any{"operator_id": "OP-A", "park_ref": "PARK-A", "name": "甲"}},
		{"path": "/admin/operators", "operator": "", "body": map[string]any{"operator_id": "OP-B", "park_ref": "PARK-B", "name": "乙"}},
		{"path": "/admin/policy-revisions", "operator": "", "body": map[string]any{"version": "V1", "title": "政策"}},
	} {
		code, resp := do(t, h, "POST", p["path"].(string), p["operator"].(string), p["body"].(map[string]any))
		mustCode(t, code, http.StatusOK, resp)
	}

	// 台商首条线索。
	code, body := do(t, h, "POST", "/v1/sources/enterprise", "OP-A", map[string]any{
		"source_record_ref": "E-1", "enterprise_ref": "ORG-1", "visit_ref": "V-1",
		"payload": map[string]any{"title": "落地项目", "conditions": map[string]any{"land": "50亩"}},
	})
	mustCode(t, code, http.StatusCreated, body)
	intentID := body["intent"].(map[string]any)["intent_id"].(string)

	// 台青冲突条件。
	code, body = do(t, h, "POST", "/v1/sources/youth", "OP-A", map[string]any{
		"source_record_ref": "Y-1", "enterprise_ref": "ORG-1", "visit_ref": "V-1",
		"payload": map[string]any{"conditions": map[string]any{"land": "90亩"}},
	})
	mustCode(t, code, http.StatusCreated, body)
	conflicts := body["conflicts"].([]any)
	if len(conflicts) != 1 || conflicts[0] != "conditions" {
		t.Fatalf("应识别 conditions 冲突, 实际 %v", conflicts)
	}

	// B 园区经办人无权读该意向。
	if code, _ := do(t, h, "GET", "/v1/intents/"+intentID, "OP-B", nil); code != http.StatusForbidden {
		t.Fatalf("跨园区访问应 403, 实际 %d", code)
	}

	// 授权（含一名不同意公开的联系人）并确认政策。
	code, body = do(t, h, "POST", "/v1/authorizations", "OP-A", map[string]any{
		"consent_ref": "C-1", "enterprise_ref": "ORG-1",
		"contacts": []any{
			map[string]any{"name": "公开人", "public_consent": true},
			map[string]any{"name": "内部人", "public_consent": false},
		},
	})
	mustCode(t, code, http.StatusOK, body)
	code, _ = do(t, h, "POST", "/v1/intents/"+intentID+"/policy-confirmations", "OP-A",
		map[string]any{"consent_ref": "C-1"})
	mustCode(t, code, http.StatusOK, body)

	// 实地对接与分歧处理。
	code, body = do(t, h, "POST", "/v1/intents/"+intentID+"/engagements", "OP-A",
		map[string]any{"location": "厂房", "summary": "勘察"})
	mustCode(t, code, http.StatusOK, body)
	engID := body["engagement_id"].(string)
	code, body = do(t, h, "POST", "/v1/intents/"+intentID+"/disputes", "OP-A", map[string]any{
		"engagement_id": engID, "raised_by": "企业", "issue": "面积",
	})
	mustCode(t, code, http.StatusOK, body)
	disputeID := body["dispute_id"].(string)
	code, body = do(t, h, "POST", "/v1/disputes/"+disputeID+"/resolve", "OP-A",
		map[string]any{"resolution": "分期供地"})
	mustCode(t, code, http.StatusOK, body)

	// 公开意向。
	code, body = do(t, h, "POST", "/v1/intents/"+intentID+"/publish", "OP-A", nil)
	mustCode(t, code, http.StatusOK, body)

	// 转交 B 园区并由 B 接收。
	code, body = do(t, h, "POST", "/v1/intents/"+intentID+"/transfers", "OP-A",
		map[string]any{"to_park_ref": "PARK-B"})
	mustCode(t, code, http.StatusOK, body)
	receipt := body["receipt_no"].(string)
	code, body = do(t, h, "POST", "/v1/transfers/"+receipt+"/acknowledge", "OP-B",
		map[string]any{"accept": true})
	mustCode(t, code, http.StatusOK, body)

	// 接收后 B 可查时间线。
	code, body = do(t, h, "GET", "/v1/intents/"+intentID+"/timeline", "OP-B", nil)
	mustCode(t, code, http.StatusOK, body)
	if len(body["engagements"].([]any)) != 1 {
		t.Fatalf("时间线应含 1 次对接, 实际 %v", body["engagements"])
	}
	if len(body["events"].([]any)) < 8 {
		t.Fatalf("事件链过短: %d", len(body["events"].([]any)))
	}

	// 对外清单无需鉴权，仅含公开同意联系人。
	code, body = do(t, h, "GET", "/v1/public/intents", "", nil)
	mustCode(t, code, http.StatusOK, body)
	items := body["intents"].([]any)
	if len(items) != 1 {
		t.Fatalf("应恰有 1 条公开意向, 实际 %d", len(items))
	}
	contacts := items[0].(map[string]any)["contacts"].([]any)
	if len(contacts) != 1 || contacts[0].(map[string]any)["name"] != "公开人" {
		t.Fatalf("仅公开同意联系人应出现, 实际 %v", contacts)
	}
}
