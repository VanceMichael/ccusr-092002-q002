// Package httpapi 暴露合作线索归口服务的 HTTP 接口。
// 身份通过请求头声明：X-Actor-Kind（handler/enterprise）、X-Actor-Id、
// X-Actor-Park（经办人所属园区）、X-Actor-Enterprise（企业编号）。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/leads"
)

// Router 返回挂载全部接口的处理器。
func Router(svc *leads.Service) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /visits", h.createVisit)
	mux.HandleFunc("POST /intents", h.submitIntent)
	mux.HandleFunc("GET /intents/{id}", h.getIntent)
	mux.HandleFunc("PATCH /intents/{id}/scope", h.adjustScope)
	mux.HandleFunc("POST /intents/{id}/publish", h.publish)
	mux.HandleFunc("POST /intents/{id}/feedback", h.addFeedback)
	mux.HandleFunc("POST /intents/{id}/contacts", h.addContact)
	mux.HandleFunc("POST /intents/{id}/contacts/{contactId}/consent", h.grantConsent)
	mux.HandleFunc("POST /intents/{id}/engagements", h.recordEngagement)
	mux.HandleFunc("POST /intents/{id}/transfers", h.initiateTransfer)
	mux.HandleFunc("POST /transfers/{id}/accept", h.acceptTransfer)
	mux.HandleFunc("POST /intents/{id}/disputes/{disputeId}/resolve", h.resolveDispute)
	mux.HandleFunc("POST /parks/{park}/policies", h.registerPolicy)
	mux.HandleFunc("POST /intents/{id}/policy-reconfirmations", h.reconfirmPolicy)
	mux.HandleFunc("POST /intents/{id}/sign", h.sign)
	mux.HandleFunc("GET /intents/{id}/trace", h.trace)
	mux.HandleFunc("GET /public/cooperations", h.publicList)
	return mux
}

type handler struct {
	svc *leads.Service
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func actorOf(r *http.Request) leads.Actor {
	return leads.Actor{
		Kind:          r.Header.Get("X-Actor-Kind"),
		ID:            r.Header.Get("X-Actor-Id"),
		ParkRef:       r.Header.Get("X-Actor-Park"),
		EnterpriseRef: r.Header.Get("X-Actor-Enterprise"),
	}
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, leads.ErrValidation, "请求体不是合法的 JSON")
		return false
	}
	return true
}

func (h *handler) createVisit(w http.ResponseWriter, r *http.Request) {
	var body leads.Visit
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.CreateVisit(body); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"visit_ref": body.VisitRef})
}

func (h *handler) submitIntent(w http.ResponseWriter, r *http.Request) {
	var body leads.SubmitIntentInput
	if !decode(w, r, &body) {
		return
	}
	result, err := h.svc.SubmitIntent(body)
	if err != nil {
		writeError(w, err, "")
		return
	}
	status := http.StatusCreated
	if !result.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (h *handler) getIntent(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.GetIntent(r.PathValue("id"))
	if err != nil {
		writeError(w, err, "")
		return
	}
	contacts, err := h.svc.ListContacts(intent.ID)
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intent": intent, "contacts": contacts})
}

func (h *handler) adjustScope(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.AdjustScope(r.PathValue("id"), actorOf(r), body.Scope); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "scope_adjusted"})
}

func (h *handler) publish(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Publish(r.PathValue("id"), actorOf(r)); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "published"})
}

func (h *handler) addFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.AddFeedback(r.PathValue("id"), actorOf(r), body.Content); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "feedback_recorded"})
}

func (h *handler) addContact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	contact, err := h.svc.AddContact(r.PathValue("id"), actorOf(r), body.Name, body.Role)
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, contact)
}

func (h *handler) grantConsent(w http.ResponseWriter, r *http.Request) {
	contactID, err := strconv.ParseInt(r.PathValue("contactId"), 10, 64)
	if err != nil {
		writeError(w, leads.ErrValidation, "contactId 不是合法编号")
		return
	}
	var body struct {
		ConsentRef string `json:"consent_ref"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.GrantConsent(r.PathValue("id"), contactID, actorOf(r), body.ConsentRef); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "consent_granted"})
}

func (h *handler) recordEngagement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Location   string `json:"location"`
		Summary    string `json:"summary"`
		OccurredAt string `json:"occurred_at"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.RecordEngagement(r.PathValue("id"), actorOf(r), body.Location, body.Summary, body.OccurredAt); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "engagement_recorded"})
}

func (h *handler) initiateTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToPark string `json:"to_park"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	transfer, err := h.svc.InitiateTransfer(r.PathValue("id"), actorOf(r), body.ToPark, body.Note)
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, transfer)
}

func (h *handler) acceptTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	transfer, err := h.svc.AcceptTransfer(r.PathValue("id"), actorOf(r), body.Note)
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, transfer)
}

func (h *handler) resolveDispute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Resolution string `json:"resolution"`
	}
	if !decode(w, r, &body) {
		return
	}
	err := h.svc.ResolveDispute(r.PathValue("id"), r.PathValue("disputeId"), actorOf(r), body.Resolution)
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "dispute_resolved"})
}

func (h *handler) registerPolicy(w http.ResponseWriter, r *http.Request) {
	var body leads.Policy
	if !decode(w, r, &body) {
		return
	}
	body.ParkRef = r.PathValue("park")
	if err := h.svc.RegisterPolicy(body); err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"park_ref": body.ParkRef, "revision": body.Revision})
}

func (h *handler) reconfirmPolicy(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.ReconfirmPolicy(r.PathValue("id"), actorOf(r))
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (h *handler) sign(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.Sign(r.PathValue("id"), actorOf(r))
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (h *handler) trace(w http.ResponseWriter, r *http.Request) {
	trace, err := h.svc.Trace(r.PathValue("id"))
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (h *handler) publicList(w http.ResponseWriter, _ *http.Request) {
	items, err := h.svc.PublicList()
	if err != nil {
		writeError(w, err, "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError 把领域错误映射为 HTTP 状态码。
func writeError(w http.ResponseWriter, err error, message string) {
	if message == "" {
		message = err.Error()
	}
	code := "internal"
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, leads.ErrValidation):
		code, status = "validation", http.StatusBadRequest
	case errors.Is(err, leads.ErrNotFound):
		code, status = "not_found", http.StatusNotFound
	case errors.Is(err, leads.ErrForbidden):
		code, status = "forbidden", http.StatusForbidden
	case errors.Is(err, leads.ErrPolicyStale):
		code, status = "policy_reconfirm_required", http.StatusConflict
	case errors.Is(err, leads.ErrDisputesOpen):
		code, status = "disputes_open", http.StatusConflict
	case errors.Is(err, leads.ErrConflict):
		code, status = "conflict", http.StatusConflict
	}
	if status == http.StatusInternalServerError && !strings.HasPrefix(message, "内部") {
		message = "内部错误: " + message
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
