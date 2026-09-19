// Package httpapi 暴露合作线索归口的 HTTP 接口。
// 经办人通过 X-Operator-ID 头标识身份，服务端按其所属园区做数据隔离；
// /v1/public 下的对外清单不鉴权，但只返回已公开且获联系人同意的数据。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/domain"
)

const operatorHeader = "X-Operator-ID"

type server struct {
	svc *domain.Service
}

// Router 构建全部路由；db 为已迁移的数据库连接。
func Router(db *sql.DB) http.Handler {
	s := &server{svc: domain.New(db)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.health)

	// 基础目录登记（联调/初始化用）
	mux.HandleFunc("POST /admin/parks", s.ensurePark)
	mux.HandleFunc("POST /admin/enterprises", s.ensureEnterprise)
	mux.HandleFunc("POST /admin/operators", s.registerOperator)
	mux.HandleFunc("POST /admin/policy-revisions", s.createPolicy)

	// 对外合作清单（不鉴权，只含公开数据）
	mux.HandleFunc("GET /v1/public/intents", s.publicIntents)

	// 经办人接口
	mux.HandleFunc("POST /v1/sources/{source}", s.ingest)
	mux.HandleFunc("GET /v1/intents", s.listIntents)
	mux.HandleFunc("GET /v1/intents/{id}", s.getIntent)
	mux.HandleFunc("PUT /v1/intents/{id}/conditions", s.updateConditions)
	mux.HandleFunc("POST /v1/intents/{id}/conflicts/resolve", s.resolveConflicts)
	mux.HandleFunc("POST /v1/intents/{id}/feedback", s.addFeedback)

	mux.HandleFunc("POST /v1/authorizations", s.submitAuthorization)
	mux.HandleFunc("PUT /v1/intents/{id}/consent", s.attachConsent)
	mux.HandleFunc("POST /v1/intents/{id}/scope-adjustments", s.adjustScope)

	mux.HandleFunc("POST /v1/intents/{id}/policy-confirmations", s.confirmPolicy)
	mux.HandleFunc("GET /v1/intents/{id}/readiness", s.readiness)
	mux.HandleFunc("POST /v1/intents/{id}/sign", s.signIntent)
	mux.HandleFunc("POST /v1/intents/{id}/publish", s.publishIntent)

	mux.HandleFunc("POST /v1/intents/{id}/transfers", s.transfer)
	mux.HandleFunc("GET /v1/transfers", s.listTransfers)
	mux.HandleFunc("GET /v1/transfers/{receipt}", s.getTransfer)
	mux.HandleFunc("POST /v1/transfers/{receipt}/acknowledge", s.acknowledgeTransfer)

	mux.HandleFunc("POST /v1/intents/{id}/engagements", s.addEngagement)
	mux.HandleFunc("POST /v1/intents/{id}/disputes", s.addDispute)
	mux.HandleFunc("POST /v1/disputes/{id}/resolve", s.resolveDispute)
	mux.HandleFunc("GET /v1/intents/{id}/timeline", s.timeline)

	return loggingMiddleware(mux)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- 基础目录 ----

type parkRequest struct {
	ParkRef string `json:"park_ref"`
	Name    string `json:"name"`
}

func (s *server) ensurePark(w http.ResponseWriter, r *http.Request) {
	var req parkRequest
	if !decode(w, r, &req) {
		return
	}
	park, err := s.svc.EnsurePark(r.Context(), req.ParkRef, req.Name)
	writeDomainResult(w, park, err)
}

type enterpriseRequest struct {
	EnterpriseRef string `json:"enterprise_ref"`
	Name          string `json:"name"`
}

func (s *server) ensureEnterprise(w http.ResponseWriter, r *http.Request) {
	var req enterpriseRequest
	if !decode(w, r, &req) {
		return
	}
	ent, err := s.svc.EnsureEnterprise(r.Context(), req.EnterpriseRef, req.Name)
	writeDomainResult(w, ent, err)
}

type operatorRequest struct {
	OperatorID string `json:"operator_id"`
	ParkRef    string `json:"park_ref"`
	Name       string `json:"name"`
}

func (s *server) registerOperator(w http.ResponseWriter, r *http.Request) {
	var req operatorRequest
	if !decode(w, r, &req) {
		return
	}
	op, err := s.svc.RegisterOperator(r.Context(), req.OperatorID, req.ParkRef, req.Name)
	writeDomainResult(w, op, err)
}

type policyRequest struct {
	Version     string         `json:"version"`
	Title       string         `json:"title"`
	Detail      map[string]any `json:"detail"`
	EffectiveAt string         `json:"effective_at,omitempty"`
}

func (s *server) createPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if !decode(w, r, &req) {
		return
	}
	revision, err := s.svc.CreatePolicyRevision(r.Context(), req.Version, req.Title,
		req.Detail, parseTime(req.EffectiveAt))
	writeDomainResult(w, revision, err)
}

// ---- 线索归口 ----

func (s *server) ingest(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req domain.IngestRequest
	if !decode(w, r, &req) {
		return
	}
	intent, sources, conflicts, err := s.svc.IngestRecord(r.Context(),
		operatorID, r.PathValue("source"), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"intent": intent, "sources": sources, "conflicts": conflicts,
	})
}

func (s *server) listIntents(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	intents, err := s.svc.ListIntents(r.Context(), operatorID, r.URL.Query().Get("status"))
	writeDomainResult(w, intents, err)
}

func (s *server) getIntent(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	intent, sources, err := s.svc.GetIntent(r.Context(), operatorID, r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent": intent, "sources": sources})
}

type conditionsRequest struct {
	Title      string         `json:"title"`
	Conditions map[string]any `json:"conditions"`
}

func (s *server) updateConditions(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req conditionsRequest
	if !decode(w, r, &req) {
		return
	}
	intent, err := s.svc.UpdateConditions(r.Context(), operatorID, r.PathValue("id"),
		req.Title, req.Conditions)
	writeDomainResult(w, intent, err)
}

func (s *server) resolveConflicts(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	err := s.svc.ReviewConflicts(r.Context(), operatorID, r.PathValue("id"))
	writeActionResult(w, map[string]string{"status": "reviewed"}, err)
}

type feedbackRequest struct {
	Content string `json:"content"`
}

func (s *server) addFeedback(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req feedbackRequest
	if !decode(w, r, &req) {
		return
	}
	feedback, err := s.svc.AddFeedback(r.Context(), operatorID, r.PathValue("id"), req.Content)
	writeDomainResult(w, feedback, err)
}

// ---- 授权与范围 ----

func (s *server) submitAuthorization(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req struct {
		ConsentRef string `json:"consent_ref"`
		domain.AuthorizationRequest
	}
	if !decode(w, r, &req) {
		return
	}
	auth, err := s.svc.SubmitAuthorization(r.Context(), operatorID, req.ConsentRef, req.AuthorizationRequest)
	writeDomainResult(w, auth, err)
}

type consentRequest struct {
	ConsentRef string `json:"consent_ref"`
}

func (s *server) attachConsent(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req consentRequest
	if !decode(w, r, &req) {
		return
	}
	err := s.svc.AttachConsent(r.Context(), operatorID, r.PathValue("id"), req.ConsentRef)
	writeActionResult(w, map[string]string{"consent_ref": req.ConsentRef}, err)
}

func (s *server) adjustScope(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req struct {
		ConsentRef string `json:"consent_ref"`
		domain.AuthorizationRequest
	}
	if !decode(w, r, &req) {
		return
	}
	auth, err := s.svc.AdjustScope(r.Context(), operatorID, r.PathValue("id"),
		req.ConsentRef, req.AuthorizationRequest)
	writeDomainResult(w, auth, err)
}

// ---- 政策确认、签约、公开 ----

type confirmPolicyRequest struct {
	ConsentRef string `json:"consent_ref,omitempty"`
}

func (s *server) confirmPolicy(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	req := confirmPolicyRequest{}
	if r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	confirmation, err := s.svc.ConfirmPolicy(r.Context(), operatorID,
		r.PathValue("id"), req.ConsentRef)
	writeDomainResult(w, confirmation, err)
}

func (s *server) readiness(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	readiness, err := s.svc.Readiness(r.Context(), operatorID, r.PathValue("id"))
	writeDomainResult(w, readiness, err)
}

func (s *server) signIntent(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	err := s.svc.SignIntent(r.Context(), operatorID, r.PathValue("id"))
	writeActionResult(w, map[string]string{"status": domain.StatusSigned}, err)
}

func (s *server) publishIntent(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	publishedAt, err := s.svc.PublishIntent(r.Context(), operatorID, r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "published", "published_at": publishedAt,
	})
}

// ---- 跨园转交 ----

func (s *server) transfer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req domain.TransferRequest
	if !decode(w, r, &req) {
		return
	}
	transfer, err := s.svc.Transfer(r.Context(), operatorID, r.PathValue("id"), req)
	writeDomainResult(w, transfer, err)
}

func (s *server) acknowledgeTransfer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req struct {
		Accept       bool   `json:"accept"`
		RejectReason string `json:"reject_reason,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	transfer, err := s.svc.AcknowledgeTransfer(r.Context(), operatorID,
		r.PathValue("receipt"), req.Accept, req.RejectReason)
	writeDomainResult(w, transfer, err)
}

func (s *server) getTransfer(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	transfer, err := s.svc.GetTransferForOperator(r.Context(), operatorID, r.PathValue("receipt"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transfer)
}

func (s *server) listTransfers(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("view") != "inbox" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "仅支持 view=inbox 查询待接收转交"})
		return
	}
	transfers, err := s.svc.ListInboxTransfers(r.Context(), operatorID)
	writeDomainResult(w, transfers, err)
}

// ---- 实地对接、分歧、时间线 ----

func (s *server) addEngagement(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req domain.EngagementRequest
	if !decode(w, r, &req) {
		return
	}
	engagement, err := s.svc.AddEngagement(r.Context(), operatorID, r.PathValue("id"), req)
	writeDomainResult(w, engagement, err)
}

type disputeRequest struct {
	EngagementID string `json:"engagement_id,omitempty"`
	RaisedBy     string `json:"raised_by"`
	Issue        string `json:"issue"`
}

func (s *server) addDispute(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req disputeRequest
	if !decode(w, r, &req) {
		return
	}
	dispute, err := s.svc.AddDispute(r.Context(), operatorID, r.PathValue("id"),
		req.EngagementID, req.RaisedBy, req.Issue)
	writeDomainResult(w, dispute, err)
}

type resolveDisputeRequest struct {
	Resolution string `json:"resolution"`
}

func (s *server) resolveDispute(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	var req resolveDisputeRequest
	if !decode(w, r, &req) {
		return
	}
	dispute, err := s.svc.ResolveDispute(r.Context(), operatorID, r.PathValue("id"), req.Resolution)
	writeDomainResult(w, dispute, err)
}

func (s *server) timeline(w http.ResponseWriter, r *http.Request) {
	operatorID, ok := requireOperator(w, r)
	if !ok {
		return
	}
	timeline, err := s.svc.Timeline(r.Context(), operatorID, r.PathValue("id"))
	writeDomainResult(w, timeline, err)
}

// ---- 对外清单 ----

func (s *server) publicIntents(w http.ResponseWriter, r *http.Request) {
	entries, err := s.svc.PublicListing(r.Context(), r.URL.Query().Get("park_ref"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if entries == nil {
		entries = []domain.PublicEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"intents": entries})
}

// ---- 辅助 ----

func requireOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.Header.Get(operatorHeader)
	if id == "" {
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"error": "缺少经办人标识头 X-Operator-ID"})
		return "", false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON: " + err.Error()})
		return false
	}
	return true
}

func writeDomainResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeActionResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrParkScope), errors.Is(err, domain.ErrCrossPark):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrDuplicateSource),
		errors.Is(err, domain.ErrPublished), errors.Is(err, domain.ErrFinalized),
		errors.Is(err, domain.ErrTransferState), errors.Is(err, domain.ErrPolicyChanged),
		errors.Is(err, domain.ErrNoAuthorization), errors.Is(err, domain.ErrNoPolicy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
