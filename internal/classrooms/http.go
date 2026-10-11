package classrooms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/model"
)

// ActiveChecker lets the API keep the existing standby-node read-only
// behavior for classroom mutations without coupling this package to the
// server's role-file implementation.
type ActiveChecker func(http.ResponseWriter, *http.Request) bool

type HTTPHandler struct {
	service          *Service
	auth             *auth.Service
	requireActive    ActiveChecker
	invitationSender InvitationSender
	publicBaseURL    string
}

type InvitationSender interface {
	SendClassroomInvitation(context.Context, string, string, string, string) error
}

func NewHTTPHandler(service *Service, authService *auth.Service, requireActive ActiveChecker) *HTTPHandler {
	return &HTTPHandler{service: service, auth: authService, requireActive: requireActive}
}

func (h *HTTPHandler) SetInvitationDelivery(sender InvitationSender, publicBaseURL string) {
	if h == nil {
		return
	}
	h.invitationSender = sender
	h.publicBaseURL = strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil {
		detail(w, http.StatusServiceUnavailable, "classroom service unavailable")
		return
	}
	segments, ok := classroomPath(r.URL.Path)
	if !ok {
		detail(w, http.StatusNotFound, "classroom route not found")
		return
	}
	switch {
	case len(segments) == 0:
		h.collection(w, r)
	case len(segments) == 1 && segments[0] == "join":
		h.join(w, r, "")
	case len(segments) == 1:
		h.resource(w, r, segments[0])
	case len(segments) == 2 && segments[1] == "join":
		h.join(w, r, segments[0])
	case len(segments) == 2 && segments[1] == "leave":
		h.leave(w, r, segments[0])
	case len(segments) == 2 && segments[1] == "invitations":
		h.invitations(w, r, segments[0])
	case len(segments) == 3 && segments[1] == "invitations":
		h.invitation(w, r, segments[0], segments[2])
	case len(segments) == 2 && (segments[1] == "members" || segments[1] == "roster"):
		h.roster(w, r, segments[0])
	case len(segments) == 3 && (segments[1] == "members" || segments[1] == "roster"):
		h.member(w, r, segments[0], segments[2])
	default:
		detail(w, http.StatusNotFound, "classroom route not found")
	}
}

func (h *HTTPHandler) collection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := h.service.ListForUser(r.Context(), user, parseLimit(r))
		if err != nil {
			h.writeError(w, err)
			return
		}
		if rows == nil {
			rows = []model.Classroom{}
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}
	if !h.active(w, r) {
		return
	}
	var payload struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ClientID    string `json:"client_id"`
	}
	if !decode(w, r, &payload) {
		return
	}
	classroom, err := h.service.Create(r.Context(), user, CreateInput{Name: payload.Name, Description: payload.Description, ClientID: payload.ClientID})
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, classroom)
}

func (h *HTTPHandler) invitations(w http.ResponseWriter, r *http.Request, classroomID string) {
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := h.service.ListInvitations(r.Context(), user, classroomID)
		if err != nil {
			h.writeError(w, err)
			return
		}
		if rows == nil {
			rows = []model.ClassroomInvitation{}
		}
		writeJSON(w, http.StatusOK, rows)
	case http.MethodPost:
		if !h.active(w, r) {
			return
		}
		var payload struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if !decode(w, r, &payload) {
			return
		}
		invitation, err := h.service.Invite(r.Context(), user, classroomID, payload.Name, payload.Email)
		if err != nil {
			h.writeError(w, err)
			return
		}
		invitation.Delivery = "not_configured"
		if h.invitationSender != nil && h.publicBaseURL != "" {
			link := h.publicBaseURL + "/?join_class=" + url.QueryEscape(invitation.JoinCode)
			if err := h.invitationSender.SendClassroomInvitation(r.Context(), invitation.Email, invitation.Name, classroomName(r.Context(), h.service, user, classroomID), link); err != nil {
				invitation.Delivery = "failed"
			} else {
				invitation.Delivery = "sent"
			}
		}
		writeJSON(w, http.StatusCreated, invitation)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func classroomName(ctx context.Context, service *Service, user *model.User, classroomID string) string {
	classroom, err := service.GetForUser(ctx, user, classroomID)
	if err != nil || classroom == nil {
		return "your classroom"
	}
	return classroom.Name
}

func (h *HTTPHandler) invitation(w http.ResponseWriter, r *http.Request, classroomID, invitationID string) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !h.active(w, r) {
		return
	}
	if err := h.service.DeleteInvitation(r.Context(), user, classroomID, invitationID); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) leave(w http.ResponseWriter, r *http.Request, classroomID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !h.active(w, r) {
		return
	}
	if err := h.service.Leave(r.Context(), user, classroomID); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) resource(w http.ResponseWriter, r *http.Request, classroomID string) {
	switch r.Method {
	case http.MethodGet:
		user, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		classroom, err := h.service.GetForUser(r.Context(), user, classroomID)
		if err != nil {
			h.writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, classroom)
	case http.MethodPatch, http.MethodPut:
		user, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		if !h.active(w, r) {
			return
		}
		var payload struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decode(w, r, &payload) {
			return
		}
		classroom, err := h.service.Update(r.Context(), user, classroomID, UpdateInput{Name: payload.Name, Description: payload.Description})
		if err != nil {
			h.writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, classroom)
	case http.MethodDelete:
		user, ok := h.currentUser(w, r)
		if !ok {
			return
		}
		if !h.active(w, r) {
			return
		}
		if err := h.service.Delete(r.Context(), user, classroomID); err != nil {
			h.writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete)
	}
}

func (h *HTTPHandler) join(w http.ResponseWriter, r *http.Request, expectedClassroomID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !h.active(w, r) {
		return
	}
	var payload struct {
		JoinCode string `json:"join_code"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &payload) {
		return
	}
	code := payload.JoinCode
	if code == "" {
		code = payload.Code
	}
	classroom, err := h.service.Join(r.Context(), user, code, expectedClassroomID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, classroom)
}

func (h *HTTPHandler) roster(w http.ResponseWriter, r *http.Request, classroomID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	rows, err := h.service.ListMembers(r.Context(), user, classroomID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if rows == nil {
		rows = []model.ClassroomMember{}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *HTTPHandler) member(w http.ResponseWriter, r *http.Request, classroomID, memberID string) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, http.MethodDelete)
		return
	}
	user, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	if !h.active(w, r) {
		return
	}
	if err := h.service.RemoveMember(r.Context(), user, classroomID, memberID); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) currentUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	if h.auth == nil {
		detail(w, http.StatusServiceUnavailable, "authentication service unavailable")
		return nil, false
	}
	user, err := h.auth.CurrentUser(r.Context(), r.Header.Get("Authorization"))
	if err == nil {
		return user, true
	}
	switch {
	case errors.Is(err, auth.ErrAuthenticationRequired):
		detail(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, auth.ErrAccountDeleted):
		detail(w, http.StatusUnauthorized, "account no longer exists")
	case errors.Is(err, auth.ErrInvalidSession):
		detail(w, http.StatusUnauthorized, "invalid or expired session")
	default:
		detail(w, http.StatusServiceUnavailable, "data layer unavailable")
	}
	return nil, false
}

func (h *HTTPHandler) active(w http.ResponseWriter, r *http.Request) bool {
	if h.requireActive == nil {
		return true
	}
	return h.requireActive(w, r)
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrAuthenticationRequired):
		detail(w, http.StatusUnauthorized, "authentication required")
	case errors.Is(err, ErrTeacherRequired), errors.Is(err, ErrStudentRequired), errors.Is(err, ErrClassroomForbidden):
		detail(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrClassroomNotFound), errors.Is(err, ErrMemberNotFound):
		detail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrAlreadyMember), errors.Is(err, ErrJoinCodeTaken):
		detail(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrIdempotencyConflict):
		detail(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalidJoinCode), errors.Is(err, ErrInvalidInput):
		validation(w, err.Error())
	case errors.Is(err, ErrStoreUnavailable):
		detail(w, http.StatusServiceUnavailable, err.Error())
	default:
		detail(w, http.StatusServiceUnavailable, "data layer unavailable")
	}
}

func classroomPath(path string) ([]string, bool) {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	for _, prefix := range []string{"/api/classrooms", "/classrooms"} {
		if path == prefix {
			return nil, true
		}
		if strings.HasPrefix(path, prefix+"/") {
			rest := strings.TrimPrefix(path, prefix+"/")
			if rest == "" {
				return nil, true
			}
			parts := strings.Split(rest, "/")
			for _, part := range parts {
				if part == "" {
					return nil, false
				}
			}
			return parts, true
		}
	}
	return nil, false
}

func decode(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(destination); err != nil {
		validation(w, "invalid request body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		validation(w, "invalid request body")
		return false
	}
	return true
}

func parseLimit(r *http.Request) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	detail(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func detail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}

func validation(w http.ResponseWriter, message string) {
	detail(w, http.StatusUnprocessableEntity, message)
}
