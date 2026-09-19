package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/store"
)

// fixture 封装一套两园区、两经办人的最小环境。
type fixture struct {
	t   *testing.T
	svc *Service
	ctx context.Context

	parkA, parkB, entRef, visitRef string
	opA, opB, opB2                 string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), "")
	if err != nil {
		t.Fatalf("打开内存库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := New(db)
	f := &fixture{
		t: t, svc: svc, ctx: context.Background(),
		parkA: "PARK-A", parkB: "PARK-B", entRef: "ORG-1", visitRef: "VISIT-1",
		opA: "OP-A", opB: "OP-B", opB2: "OP-B2",
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("初始化数据: %v", err)
		}
	}
	_, err = svc.EnsurePark(f.ctx, f.parkA, "沈阳A产业园")
	must(err)
	_, err = svc.EnsurePark(f.ctx, f.parkB, "沈阳B产业园")
	must(err)
	_, err = svc.EnsureEnterprise(f.ctx, f.entRef, "台企一号")
	must(err)
	_, err = svc.RegisterOperator(f.ctx, f.opA, f.parkA, "甲经办")
	must(err)
	_, err = svc.RegisterOperator(f.ctx, f.opB, f.parkB, "乙经办")
	must(err)
	_, err = svc.RegisterOperator(f.ctx, f.opB2, f.parkB, "乙经办二号")
	must(err)
	_, err = svc.CreatePolicyRevision(f.ctx, "POL-V1", "首版优惠", map[string]any{"tax": "减免"}, time.Time{})
	must(err)
	return f
}

func (f *fixture) ingest(op, source, recRef string, payload map[string]any) (*Intent, []SourceRecord, []string) {
	f.t.Helper()
	intent, sources, conflicts, err := f.svc.IngestRecord(f.ctx, op, source, IngestRequest{
		SourceRecordRef: recRef,
		EnterpriseRef:   f.entRef,
		VisitRef:        f.visitRef,
		SubmitterRef:    op,
		Payload:         payload,
	})
	if err != nil {
		f.t.Fatalf("接收来源记录 %s: %v", recRef, err)
	}
	return intent, sources, conflicts
}

func confirmAndCheck(t *testing.T, f *fixture, intentID, op, consent string) {
	t.Helper()
	if _, err := f.svc.ConfirmPolicy(f.ctx, op, intentID, consent); err != nil {
		t.Fatalf("政策确认: %v", err)
	}
	r, err := f.svc.Readiness(f.ctx, op, intentID)
	if err != nil {
		t.Fatalf("就绪检查: %v", err)
	}
	if !r.Ready {
		t.Fatalf("期望就绪，实际 %+v", r)
	}
}

// 同一企业同一次走访的台商、台青、区县三条记录归口为一条意向，
// 互斥字段进入冲突清单而不是新建项目。
func TestIngest_MergesConflictingRecords(t *testing.T) {
	f := newFixture(t)

	i1, sources, conflicts := f.ingest(f.opA, SourceEnterprise, "REC-E",
		map[string]any{"title": "精密制造落地", "conditions": map[string]any{"land": "50亩"}})
	if len(sources) != 1 || len(conflicts) != 0 {
		t.Fatalf("首条记录不应有冲突, sources=%d conflicts=%v", len(sources), conflicts)
	}

	// 台青口径：项目名一致、条件不同。
	_, _, conflicts2 := f.ingest(f.opA, SourceYouth, "REC-Y",
		map[string]any{"title": "精密制造落地", "conditions": map[string]any{"land": "80亩"}})
	if len(conflicts2) != 1 || conflicts2[0] != "conditions" {
		t.Fatalf("期望 conditions 冲突, 实际 %v", conflicts2)
	}

	// 区县招商口径：园区指向不同。
	_, _, conflicts3 := f.ingest(f.opA, SourceDistrict, "REC-D",
		map[string]any{"title": "别的名字", "park_ref": f.parkB})
	if len(conflicts3) < 2 {
		t.Fatalf("期望 title/park_ref 冲突, 实际 %v", conflicts3)
	}

	// 仍为同一条意向，三条来源全部挂在其下。
	got, sources, err := f.svc.GetIntent(f.ctx, f.opA, i1.ID)
	if err != nil {
		t.Fatalf("读取意向: %v", err)
	}
	if !got.HasConflicts {
		t.Fatal("意向应标记存在待研判冲突")
	}
	if len(sources) != 3 {
		t.Fatalf("期望 3 条来源记录, 实际 %d", len(sources))
	}

	if err := f.svc.ReviewConflicts(f.ctx, f.opA, i1.ID); err != nil {
		t.Fatalf("冲突研判: %v", err)
	}
	got, _, err = f.svc.GetIntent(f.ctx, f.opA, i1.ID)
	if err != nil {
		t.Fatalf("重读意向: %v", err)
	}
	if got.HasConflicts {
		t.Fatal("研判后冲突标记应清除，但原始来源记录保留")
	}
}

// 同一来源记录重复提交应被拒绝（按 来源+来源编号 判重）。
func TestIngest_DuplicateSourceRejected(t *testing.T) {
	f := newFixture(t)
	f.ingest(f.opA, SourceEnterprise, "DUP", map[string]any{"title": "x"})
	_, _, _, err := f.svc.IngestRecord(f.ctx, f.opA, SourceEnterprise, IngestRequest{
		SourceRecordRef: "DUP", EnterpriseRef: f.entRef, VisitRef: f.visitRef,
		Payload: map[string]any{"title": "x"},
	})
	if !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("期望 ErrDuplicateSource, 实际 %v", err)
	}
}

// 经办人只能处理所属园区材料：归口在 A 园区的意向，B 园区经办人不可读、不可改。
func TestOperatorParkScope(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-1", map[string]any{"title": "范围测试"})

	if _, _, err := f.svc.GetIntent(f.ctx, f.opB, intent.ID); !errors.Is(err, ErrCrossPark) {
		t.Fatalf("跨园区读取应拒绝, 实际 %v", err)
	}
	if err := f.svc.ReviewConflicts(f.ctx, f.opB, intent.ID); !errors.Is(err, ErrCrossPark) {
		t.Fatalf("跨园区操作应拒绝, 实际 %v", err)
	}
	if _, err := f.svc.Timeline(f.ctx, f.opB, intent.ID); !errors.Is(err, ErrCrossPark) {
		t.Fatalf("跨园区时间线应拒绝, 实际 %v", err)
	}
	// 列表也只看本园区。
	list, err := f.svc.ListIntents(f.ctx, f.opB, "")
	if err != nil {
		t.Fatalf("列表: %v", err)
	}
	for _, item := range list {
		if item.ID == intent.ID {
			t.Fatal("B 园区列表不应出现 A 园区意向")
		}
	}
}

// 跨园转交：A 发起、B 凭回执接收，接收前归属不变，接收后归属 B 且回执可查。
func TestTransferWithReceipt(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-T", map[string]any{"title": "转交项目"})

	transfer, err := f.svc.Transfer(f.ctx, f.opA, intent.ID, TransferRequest{ToParkRef: f.parkB, Note: "产业更匹配"})
	if err != nil {
		t.Fatalf("发起转交: %v", err)
	}
	if transfer.ReceiptNo == "" {
		t.Fatal("应生成接收回执号")
	}

	// 待接收期间归属仍是 A。
	got, _, _ := f.svc.GetIntent(f.ctx, f.opA, intent.ID)
	if got.CurrentParkRef != f.parkA {
		t.Fatal("接收前归属不应改变")
	}

	// B 园区的另一位经办人不能凭同一回执接收（同一园区身份合法，这里改测非 B 园区拒绝）。
	if _, err := f.svc.AcknowledgeTransfer(f.ctx, f.opA, transfer.ReceiptNo, true, ""); !errors.Is(err, ErrParkScope) {
		t.Fatalf("非接收园区确认应拒绝, 实际 %v", err)
	}

	// B 园区在收件箱看到待办，凭回执接收。
	inbox, err := f.svc.ListInboxTransfers(f.ctx, f.opB)
	if err != nil {
		t.Fatalf("收件箱: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ReceiptNo != transfer.ReceiptNo {
		t.Fatalf("收件箱应含该转交, 实际 %+v", inbox)
	}
	ack, err := f.svc.AcknowledgeTransfer(f.ctx, f.opB, transfer.ReceiptNo, true, "")
	if err != nil {
		t.Fatalf("接收转交: %v", err)
	}
	if ack.Status != TransferAccepted || ack.AckOperatorID != f.opB {
		t.Fatalf("回执状态异常 %+v", ack)
	}

	// 归属转为 B，A 不再可见，B 可读，且双方都能查回执。
	if _, _, err := f.svc.GetIntent(f.ctx, f.opA, intent.ID); !errors.Is(err, ErrCrossPark) {
		t.Fatalf("接收后 A 不应再可见, 实际 %v", err)
	}
	if _, _, err := f.svc.GetIntent(f.ctx, f.opB, intent.ID); err != nil {
		t.Fatalf("接收后 B 应可见: %v", err)
	}
	if _, err := f.svc.GetTransferForOperator(f.ctx, f.opA, transfer.ReceiptNo); err != nil {
		t.Fatalf("发起方应能查回执: %v", err)
	}
	if _, err := f.svc.GetTransferForOperator(f.ctx, f.opB2, transfer.ReceiptNo); err != nil {
		t.Fatalf("接收方应能查回执: %v", err)
	}
}

// 企业可在未公开前调整合作范围；公开后调整被拒绝。
func TestScopeAdjustmentLockedAfterPublish(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-S", map[string]any{"title": "范围调整"})

	auth, err := f.svc.SubmitAuthorization(f.ctx, f.opA, "C-V1", AuthorizationRequest{
		EnterpriseRef: f.entRef,
		Scope:         map[string]any{"sectors": []any{"电子"}},
		Contacts: []Contact{
			{Name: "王经理", Channel: "a@x", PublicConsent: true},
			{Name: "李助理", Channel: "b@x", PublicConsent: false},
		},
	})
	if err != nil {
		t.Fatalf("提交授权: %v", err)
	}
	if auth.ConsentRef != "C-V1" {
		t.Fatalf("授权编号异常 %+v", auth)
	}

	// 未公开前可调整范围。
	if _, err := f.svc.AdjustScope(f.ctx, f.opA, intent.ID, "C-V2", AuthorizationRequest{
		Scope:    map[string]any{"sectors": []any{"电子", "精密机械"}},
		Contacts: []Contact{{Name: "王经理", Channel: "a@x", PublicConsent: true}},
	}); err != nil {
		t.Fatalf("调整范围: %v", err)
	}
	got, _, _ := f.svc.GetIntent(f.ctx, f.opA, intent.ID)
	if got.ConsentRef != "C-V2" {
		t.Fatalf("应挂载新授权版本, 实际 %s", got.ConsentRef)
	}

	confirmAndCheck(t, f, intent.ID, f.opA, "C-V2")
	if _, err := f.svc.PublishIntent(f.ctx, f.opA, intent.ID); err != nil {
		t.Fatalf("公开意向: %v", err)
	}

	// 公开后调整范围被锁定。
	if _, err := f.svc.AdjustScope(f.ctx, f.opA, intent.ID, "C-V3", AuthorizationRequest{
		Scope: map[string]any{"sectors": []any{"化工"}},
	}); !errors.Is(err, ErrPublished) {
		t.Fatalf("公开后应拒绝调整, 实际 %v", err)
	}
}

// 签约前政策版本变化必须重新确认，否则拒绝签约。
func TestPolicyChangeRequiresReconfirmation(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-P", map[string]any{"title": "政策门槛"})

	if _, err := f.svc.SubmitAuthorization(f.ctx, f.opA, "C-1", AuthorizationRequest{
		EnterpriseRef: f.entRef, Scope: map[string]any{"ok": true},
	}); err != nil {
		t.Fatalf("授权: %v", err)
	}
	confirmAndCheck(t, f, intent.ID, f.opA, "C-1")

	// 发布新版本：原确认失效，签约被拒。
	if _, err := f.svc.CreatePolicyRevision(f.ctx, "POL-V2", "二版优惠",
		map[string]any{"tax": "加倍减免"}, time.Time{}); err != nil {
		t.Fatalf("新版本: %v", err)
	}
	if err := f.svc.SignIntent(f.ctx, f.opA, intent.ID); !errors.Is(err, ErrPolicyChanged) {
		t.Fatalf("版本变化后签约应拒绝, 实际 %v", err)
	}
	r, _ := f.svc.Readiness(f.ctx, f.opA, intent.ID)
	if !r.ReconfirmationNeeded || r.Ready {
		t.Fatalf("应提示需重新确认, 实际 %+v", r)
	}

	// 企业按新版本重新确认后方可签约。
	if _, err := f.svc.ConfirmPolicy(f.ctx, f.opA, intent.ID, "C-1"); err != nil {
		t.Fatalf("重新确认: %v", err)
	}
	if err := f.svc.SignIntent(f.ctx, f.opA, intent.ID); err != nil {
		t.Fatalf("重新确认后签约应成功, 实际 %v", err)
	}
	got, _, _ := f.svc.GetIntent(f.ctx, f.opA, intent.ID)
	if got.Status != StatusSigned {
		t.Fatalf("状态应为签约, 实际 %s", got.Status)
	}
}

// 未获公开同意的联系人不得出现在对外合作清单；未公开意向也不出现。
func TestPublicListingFiltersConsent(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-PUB", map[string]any{"title": "公开项目"})

	if _, err := f.svc.SubmitAuthorization(f.ctx, f.opA, "C-PUB", AuthorizationRequest{
		EnterpriseRef: f.entRef,
		Contacts: []Contact{
			{Name: "可公开", Channel: "ok@x", PublicConsent: true},
			{Name: "不公开", Channel: "secret@x", PublicConsent: false},
		},
	}); err != nil {
		t.Fatalf("授权: %v", err)
	}
	confirmAndCheck(t, f, intent.ID, f.opA, "C-PUB")

	// 公开前清单为空。
	if entries, err := f.svc.PublicListing(f.ctx, ""); err != nil || len(entries) != 0 {
		t.Fatalf("公开前清单应为空, entries=%d err=%v", len(entries), err)
	}
	if _, err := f.svc.PublishIntent(f.ctx, f.opA, intent.ID); err != nil {
		t.Fatalf("公开: %v", err)
	}

	entries, err := f.svc.PublicListing(f.ctx, f.parkA)
	if err != nil {
		t.Fatalf("清单: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应恰有 1 条, 实际 %d", len(entries))
	}
	if len(entries[0].Contacts) != 1 || entries[0].Contacts[0].Name != "可公开" {
		t.Fatalf("仅公开同意的联系人应出现, 实际 %+v", entries[0].Contacts)
	}
}

// 时间线：从项目状态可追溯到每次实地对接及分歧处理，含转交与政策确认。
func TestTimelineTracesEngagementsAndDisputes(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-TL", map[string]any{"title": "追溯项目"})
	intentID := intent.ID

	if _, err := f.svc.SubmitAuthorization(f.ctx, f.opA, "C-TL", AuthorizationRequest{
		EnterpriseRef: f.entRef,
	}); err != nil {
		t.Fatalf("授权: %v", err)
	}
	if _, err := f.svc.ConfirmPolicy(f.ctx, f.opA, intentID, "C-TL"); err != nil {
		t.Fatalf("确认: %v", err)
	}
	if _, err := f.svc.AddFeedback(f.ctx, f.opA, intentID, "园区欢迎落地"); err != nil {
		t.Fatalf("反馈: %v", err)
	}

	eng, err := f.svc.AddEngagement(f.ctx, f.opA, intentID, EngagementRequest{
		Location: "A园区厂房", Summary: "首次实地勘察",
	})
	if err != nil {
		t.Fatalf("实地对接: %v", err)
	}
	dispute, err := f.svc.AddDispute(f.ctx, f.opA, intentID, eng.EngagementID, "企业", "用地面积分歧")
	if err != nil {
		t.Fatalf("登记分歧: %v", err)
	}
	if _, err := f.svc.ResolveDispute(f.ctx, f.opA, dispute.DisputeID, "按60亩分期供地"); err != nil {
		t.Fatalf("处理分歧: %v", err)
	}

	// 跨园转交并接收后，时间线对 B 园区可见且完整。
	transfer, err := f.svc.Transfer(f.ctx, f.opA, intentID, TransferRequest{ToParkRef: f.parkB})
	if err != nil {
		t.Fatalf("转交: %v", err)
	}
	if _, err := f.svc.AcknowledgeTransfer(f.ctx, f.opB, transfer.ReceiptNo, true, ""); err != nil {
		t.Fatalf("接收: %v", err)
	}

	tl, err := f.svc.Timeline(f.ctx, f.opB, intentID)
	if err != nil {
		t.Fatalf("时间线: %v", err)
	}
	if len(tl.Sources) != 1 {
		t.Fatalf("应含 1 条来源, 实际 %d", len(tl.Sources))
	}
	if len(tl.Confirmations) != 1 {
		t.Fatalf("应含政策确认, 实际 %d", len(tl.Confirmations))
	}
	if len(tl.Transfers) != 1 || tl.Transfers[0].Status != TransferAccepted {
		t.Fatalf("应含已接收转交, 实际 %+v", tl.Transfers)
	}
	if len(tl.Feedbacks) != 1 {
		t.Fatalf("应含园区反馈, 实际 %d", len(tl.Feedbacks))
	}
	if len(tl.Engagements) != 1 {
		t.Fatalf("应含 1 次实地对接, 实际 %d", len(tl.Engagements))
	}
	if len(tl.Engagements[0].Disputes) != 1 {
		t.Fatalf("对接应挂 1 条分歧, 实际 %+v", tl.Engagements[0].Disputes)
	}
	d := tl.Engagements[0].Disputes[0]
	if d.Status != DisputeResolved || d.Resolution != "按60亩分期供地" || d.ResolverID != f.opA {
		t.Fatalf("分歧处理结果异常 %+v", d)
	}

	// 事件链覆盖完整生命周期类型。
	types := map[string]bool{}
	for _, e := range tl.Events {
		types[e.Type] = true
	}
	for _, want := range []string{
		"INTENT_CREATED", "SOURCE_RECORDED", "CONSENT_ATTACHED", "POLICY_CONFIRMED",
		"FEEDBACK_ADDED", "ENGAGEMENT", "DISPUTE_OPENED", "DISPUTE_RESOLVED",
		"TRANSFER_REQUESTED", "TRANSFER_ACCEPTED",
	} {
		if !types[want] {
			t.Fatalf("时间线缺少事件 %s，现有 %v", want, types)
		}
	}
}

// 未授权不得签约或公开。
func TestRequiresAuthorization(t *testing.T) {
	f := newFixture(t)
	intent, _, _ := f.ingest(f.opA, SourceEnterprise, "REC-NA", map[string]any{"title": "无授权"})
	if err := f.svc.SignIntent(f.ctx, f.opA, intent.ID); !errors.Is(err, ErrNoAuthorization) {
		t.Fatalf("无授权签约应拒绝, 实际 %v", err)
	}
	if _, err := f.svc.PublishIntent(f.ctx, f.opA, intent.ID); !errors.Is(err, ErrNoAuthorization) {
		t.Fatalf("无授权公开应拒绝, 实际 %v", err)
	}
}
