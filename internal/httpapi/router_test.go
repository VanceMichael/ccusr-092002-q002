package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/leads"
	"github.com/vancemichael/092002-industrial-visit-intent/internal/sqlitedb"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := sqlitedb.Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := leads.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	server := httptest.NewServer(Router(leads.NewService(db, nil)))
	t.Cleanup(func() {
		server.Close()
		_ = db.Close()
	})
	return server
}

type request struct {
	method    string
	path      string
	body      any
	actorKind string
	actorID   string
	actorPark string
	actorOrg  string
}

func call(t *testing.T, server *httptest.Server, req request) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if req.body != nil {
		payload, err := json.Marshal(req.body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	httpReq, err := http.NewRequest(req.method, server.URL+req.path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if req.actorKind != "" {
		httpReq.Header.Set("X-Actor-Kind", req.actorKind)
	}
	if req.actorID != "" {
		httpReq.Header.Set("X-Actor-Id", req.actorID)
	}
	if req.actorPark != "" {
		httpReq.Header.Set("X-Actor-Park", req.actorPark)
	}
	if req.actorOrg != "" {
		httpReq.Header.Set("X-Actor-Enterprise", req.actorOrg)
	}
	resp, err := server.Client().Do(httpReq)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp.StatusCode, decoded
}

func asHandler(actorID, park string) request {
	return request{actorKind: "handler", actorID: actorID, actorPark: park}
}

func enterprise(org string) request {
	return request{actorKind: "enterprise", actorID: "联系人甲", actorOrg: org}
}

func merge(base request, method, path string, body any) request {
	base.method, base.path, base.body = method, path, body
	return base
}

func mustCreateVisit(t *testing.T, server *httptest.Server, visitRef, org, park string) {
	t.Helper()
	status, resp := call(t, server, request{
		method: "POST", path: "/visits",
		body: map[string]any{
			"visit_ref": visitRef, "enterprise_ref": org, "park_ref": park,
			"visited_at": "2026-09-12T09:30:00+08:00", "summary": "交流周园区走访",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("登记参访行程失败: %d %v", status, resp)
	}
}

func mustSubmitIntent(t *testing.T, server *httptest.Server, visitRef, org, source string, conditions map[string]any) map[string]any {
	t.Helper()
	status, resp := call(t, server, request{
		method: "POST", path: "/intents",
		body: map[string]any{
			"visit_ref": visitRef, "enterprise_ref": org, "source": source,
			"submitted_by": "提交人", "title": "精密零部件项目", "scope": "一期厂房",
			"conditions": conditions,
		},
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("提交合作意向失败: %d %v", status, resp)
	}
	return resp
}

func intentOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	intent, ok := resp["intent"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 intent: %v", resp)
	}
	return intent
}

func TestHealth(t *testing.T) {
	server := newServer(t)
	status, _ := call(t, server, request{method: "GET", path: "/health"})
	if status != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", status)
	}
}

// 同一企业同一走访的多来源提交归并到同一意向，冲突字段自动生成分歧。
func TestMergeConflictingSources(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-1", "ORG-1", "PARK-A")

	first := mustSubmitIntent(t, server, "VISIT-1", "ORG-1", "推介会", map[string]any{
		"investment": "5000万", "land_mu": "30",
	})
	intentID := intentOf(t, first)["id"].(string)

	second := mustSubmitIntent(t, server, "VISIT-1", "ORG-1", "园区记录", map[string]any{
		"investment": "3000万", "land_mu": "30",
	})
	if second["created"].(bool) {
		t.Fatalf("同一企业同一走访的第二次提交不应新建意向")
	}
	if intentOf(t, second)["id"].(string) != intentID {
		t.Fatalf("归口后意向编号不一致: %v", second)
	}
	disputes, ok := second["disputes"].([]any)
	if !ok || len(disputes) != 1 {
		t.Fatalf("应产生 1 条分歧，实际 %v", second["disputes"])
	}
	dispute := disputes[0].(map[string]any)
	if dispute["field"].(string) != "investment" {
		t.Fatalf("分歧字段应为 investment，实际 %v", dispute["field"])
	}
	values := dispute["values"].(map[string]any)
	if values["推介会"] != "5000万" || values["园区记录"] != "3000万" {
		t.Fatalf("分歧应记录各来源取值: %v", values)
	}
}

// 企业在公开前可调整合作范围，公开后调整被拒绝。
func TestScopeAdjustableOnlyBeforePublish(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-2", "ORG-2", "PARK-A")
	resp := mustSubmitIntent(t, server, "VISIT-2", "ORG-2", "台青代表团", map[string]any{"jobs": "120"})
	intentID := intentOf(t, resp)["id"].(string)

	status, body := call(t, server, merge(enterprise("ORG-2"), "PATCH", "/intents/"+intentID+"/scope",
		map[string]any{"scope": "一期厂房与研发中心"}))
	if status != http.StatusOK {
		t.Fatalf("公开前调整合作范围应成功: %d %v", status, body)
	}

	status, body = call(t, server, merge(enterprise("ORG-9"), "PATCH", "/intents/"+intentID+"/scope",
		map[string]any{"scope": "他人篡改"}))
	if status != http.StatusForbidden {
		t.Fatalf("非意向企业调整范围应被拒绝: %d %v", status, body)
	}

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/publish", nil))
	if status != http.StatusOK {
		t.Fatalf("公开意向失败: %d %v", status, body)
	}

	status, body = call(t, server, merge(enterprise("ORG-2"), "PATCH", "/intents/"+intentID+"/scope",
		map[string]any{"scope": "公开后调整"}))
	if status != http.StatusConflict {
		t.Fatalf("公开后调整合作范围应返回 409: %d %v", status, body)
	}
}

// 经办人只能处理所属园区的材料。
func TestHandlerRestrictedToOwnPark(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-3", "ORG-3", "PARK-A")
	resp := mustSubmitIntent(t, server, "VISIT-3", "ORG-3", "区县招商", map[string]any{"jobs": "50"})
	intentID := intentOf(t, resp)["id"].(string)

	status, body := call(t, server, merge(asHandler("经办人B", "PARK-B"), "POST", "/intents/"+intentID+"/feedback",
		map[string]any{"content": "跨园区反馈"}))
	if status != http.StatusForbidden {
		t.Fatalf("跨园区反馈应返回 403: %d %v", status, body)
	}

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/feedback",
		map[string]any{"content": "本园区反馈"}))
	if status != http.StatusCreated {
		t.Fatalf("本园区反馈应成功: %d %v", status, body)
	}
}

// 跨区转交须留下接收回执，接收后原园区经办人失去处理权限。
func TestTransferRequiresReceipt(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-4", "ORG-4", "PARK-A")
	resp := mustSubmitIntent(t, server, "VISIT-4", "ORG-4", "推介会", map[string]any{"jobs": "80"})
	intentID := intentOf(t, resp)["id"].(string)

	status, body := call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/transfers",
		map[string]any{"to_park": "PARK-B", "note": "企业意向落位 B 区"}))
	if status != http.StatusCreated {
		t.Fatalf("发起转交失败: %d %v", status, body)
	}
	transferID := body["id"].(string)

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/transfers/"+transferID+"/accept",
		map[string]any{"note": "原园区不能代接收"}))
	if status != http.StatusForbidden {
		t.Fatalf("非接收园区确认应返回 403: %d %v", status, body)
	}

	status, body = call(t, server, merge(asHandler("经办人B", "PARK-B"), "POST", "/transfers/"+transferID+"/accept",
		map[string]any{"note": "同意接收"}))
	if status != http.StatusOK {
		t.Fatalf("接收转交失败: %d %v", status, body)
	}
	receipt, ok := body["receipt"].(map[string]any)
	if !ok || receipt["received_by"].(string) != "经办人B" || receipt["park_ref"].(string) != "PARK-B" {
		t.Fatalf("转交须留下接收回执: %v", body)
	}

	status, body = call(t, server, request{method: "GET", path: "/intents/" + intentID})
	if status != http.StatusOK || body["intent"].(map[string]any)["park_ref"].(string) != "PARK-B" {
		t.Fatalf("接收后意向应归属 PARK-B: %d %v", status, body)
	}

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/feedback",
		map[string]any{"content": "原园区再反馈"}))
	if status != http.StatusForbidden {
		t.Fatalf("转交后原园区经办人不应再处理该意向: %d %v", status, body)
	}
}

// 签约前优惠政策版本变化须重新确认。
func TestPolicyRevisionRequiresReconfirmBeforeSign(t *testing.T) {
	server := newServer(t)
	status, resp := call(t, server, request{
		method: "POST", path: "/parks/PARK-A/policies",
		body: map[string]any{"revision": "2026-R1", "content": "租金三免两减半", "effective_at": "2026-06-01T00:00:00+08:00"},
	})
	if status != http.StatusCreated {
		t.Fatalf("登记政策失败: %d %v", status, resp)
	}

	mustCreateVisit(t, server, "VISIT-5", "ORG-5", "PARK-A")
	resp = mustSubmitIntent(t, server, "VISIT-5", "ORG-5", "推介会", map[string]any{"jobs": "200"})
	intentID := intentOf(t, resp)["id"].(string)
	if intentOf(t, resp)["policy_confirmed_revision"].(string) != "2026-R1" {
		t.Fatalf("意向创建时应确认当前政策版本: %v", resp)
	}

	status, resp = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/publish", nil))
	if status != http.StatusOK {
		t.Fatalf("公开失败: %d %v", status, resp)
	}

	status, resp = call(t, server, request{
		method: "POST", path: "/parks/PARK-A/policies",
		body: map[string]any{"revision": "2026-R2", "content": "设备补贴上浮", "effective_at": "2026-09-01T00:00:00+08:00"},
	})
	if status != http.StatusCreated {
		t.Fatalf("登记新政策失败: %d %v", status, resp)
	}

	status, resp = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/sign", nil))
	if status != http.StatusConflict {
		t.Fatalf("政策版本变化后签约应返回 409: %d %v", status, resp)
	}
	if resp["error"].(map[string]any)["code"].(string) != "policy_reconfirm_required" {
		t.Fatalf("错误码应为 policy_reconfirm_required: %v", resp)
	}

	status, resp = call(t, server, merge(enterprise("ORG-5"), "POST", "/intents/"+intentID+"/policy-reconfirmations", nil))
	if status != http.StatusOK {
		t.Fatalf("重新确认政策失败: %d %v", status, resp)
	}
	if resp["policy_confirmed_revision"].(string) != "2026-R2" {
		t.Fatalf("确认后应记录新版本: %v", resp)
	}

	status, resp = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/sign", nil))
	if status != http.StatusOK || resp["status"].(string) != "signed" {
		t.Fatalf("重新确认后签约应成功: %d %v", status, resp)
	}
}

// 未获企业同意的联系人不能出现在对外合作清单中。
func TestPublicListExcludesUnconsentedContacts(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-6", "ORG-6", "PARK-A")
	resp := mustSubmitIntent(t, server, "VISIT-6", "ORG-6", "推介会", map[string]any{"jobs": "60"})
	intentID := intentOf(t, resp)["id"].(string)

	var consentedID, withheldID float64
	status, body := call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/contacts",
		map[string]any{"name": "陈经理", "role": "企业代表"}))
	if status != http.StatusCreated {
		t.Fatalf("登记联系人失败: %d %v", status, body)
	}
	consentedID = body["id"].(float64)

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/contacts",
		map[string]any{"name": "林助理", "role": "随行人员"}))
	if status != http.StatusCreated {
		t.Fatalf("登记联系人失败: %d %v", status, body)
	}
	withheldID = body["id"].(float64)

	status, body = call(t, server, merge(enterprise("ORG-6"), "POST",
		fmt.Sprintf("/intents/%s/contacts/%.0f/consent", intentID, consentedID),
		map[string]any{"consent_ref": "CONSENT-1"}))
	if status != http.StatusOK {
		t.Fatalf("授予联系人同意失败: %d %v", status, body)
	}
	_ = withheldID

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/publish", nil))
	if status != http.StatusOK {
		t.Fatalf("公开失败: %d %v", status, body)
	}

	status, body = call(t, server, request{method: "GET", path: "/public/cooperations"})
	if status != http.StatusOK {
		t.Fatalf("获取对外清单失败: %d %v", status, body)
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("对外清单应含 1 项: %v", items)
	}
	contacts := items[0].(map[string]any)["contacts"].([]any)
	if len(contacts) != 1 || contacts[0].(map[string]any)["name"].(string) != "陈经理" {
		t.Fatalf("对外清单只能包含已授权联系人: %v", contacts)
	}
}

// 未公开的意向不出现在对外清单。
func TestPublicListExcludesDrafts(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-7", "ORG-7", "PARK-A")
	mustSubmitIntent(t, server, "VISIT-7", "ORG-7", "推介会", map[string]any{"jobs": "10"})

	status, body := call(t, server, request{method: "GET", path: "/public/cooperations"})
	if status != http.StatusOK {
		t.Fatalf("获取对外清单失败: %d %v", status, body)
	}
	if items := body["items"].([]any); len(items) != 0 {
		t.Fatalf("未公开意向不应出现在对外清单: %v", items)
	}
}

// 从项目状态可追到每次实地对接及分歧处理。
func TestTraceCoversEngagementsAndDisputes(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-8", "ORG-8", "PARK-A")
	mustSubmitIntent(t, server, "VISIT-8", "ORG-8", "推介会", map[string]any{"investment": "1亿"})
	resp := mustSubmitIntent(t, server, "VISIT-8", "ORG-8", "园区记录", map[string]any{"investment": "8000万"})
	intentID := intentOf(t, resp)["id"].(string)
	disputeID := resp["disputes"].([]any)[0].(map[string]any)["id"].(string)

	status, body := call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/engagements",
		map[string]any{"location": "浑南厂区", "summary": "实地踏勘厂房", "occurred_at": "2026-09-15T14:00:00+08:00"}))
	if status != http.StatusCreated {
		t.Fatalf("记录实地对接失败: %d %v", status, body)
	}

	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST",
		"/intents/"+intentID+"/disputes/"+disputeID+"/resolve",
		map[string]any{"resolution": "以企业盖章确认函 8000 万为准"}))
	if status != http.StatusOK {
		t.Fatalf("处理分歧失败: %d %v", status, body)
	}

	status, body = call(t, server, request{method: "GET", path: "/intents/" + intentID + "/trace"})
	if status != http.StatusOK {
		t.Fatalf("获取追溯失败: %d %v", status, body)
	}
	if body["intent"].(map[string]any)["status"].(string) != "draft" {
		t.Fatalf("追溯应包含当前项目状态: %v", body["intent"])
	}
	if body["visit"].(map[string]any)["visit_ref"].(string) != "VISIT-8" {
		t.Fatalf("追溯应关联参访行程: %v", body["visit"])
	}
	if records := body["source_records"].([]any); len(records) != 2 {
		t.Fatalf("追溯应包含两条来源记录: %v", records)
	}
	disputes := body["disputes"].([]any)
	if len(disputes) != 1 || disputes[0].(map[string]any)["status"].(string) != "resolved" {
		t.Fatalf("追溯应包含已处理的分歧: %v", disputes)
	}

	kinds := map[string]bool{}
	for _, raw := range body["events"].([]any) {
		kinds[raw.(map[string]any)["kind"].(string)] = true
	}
	for _, want := range []string{"intent_created", "source_appended", "dispute_raised", "engagement_recorded", "dispute_resolved"} {
		if !kinds[want] {
			t.Fatalf("事件流缺少 %s: %v", want, kinds)
		}
	}
}

// 分歧未处理时不能签约。
func TestSignBlockedByOpenDisputes(t *testing.T) {
	server := newServer(t)
	mustCreateVisit(t, server, "VISIT-9", "ORG-9", "PARK-A")
	mustSubmitIntent(t, server, "VISIT-9", "ORG-9", "推介会", map[string]any{"investment": "1亿"})
	resp := mustSubmitIntent(t, server, "VISIT-9", "ORG-9", "园区记录", map[string]any{"investment": "6000万"})
	intentID := intentOf(t, resp)["id"].(string)

	status, body := call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/publish", nil))
	if status != http.StatusOK {
		t.Fatalf("公开失败: %d %v", status, body)
	}
	status, body = call(t, server, merge(asHandler("经办人A", "PARK-A"), "POST", "/intents/"+intentID+"/sign", nil))
	if status != http.StatusConflict || body["error"].(map[string]any)["code"].(string) != "disputes_open" {
		t.Fatalf("存在未处理分歧时签约应返回 409 disputes_open: %d %v", status, body)
	}
}
