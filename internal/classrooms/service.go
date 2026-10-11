// Package classrooms contains the classroom domain rules shared by the HTTP
// API and the persistence implementations. Classroom ownership is deliberately
// separate from the existing public exam and board contracts so those demo
// paths remain backward-compatible.
package classrooms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

var (
	ErrStoreUnavailable    = errors.New("classroom store unavailable")
	ErrClassroomNotFound   = errors.New("classroom not found")
	ErrClassroomForbidden  = errors.New("classroom access forbidden")
	ErrTeacherRequired     = auth.ErrTeacherRequired
	ErrStudentRequired     = errors.New("student role required")
	ErrAlreadyMember       = errors.New("student is already a classroom member")
	ErrMemberNotFound      = errors.New("classroom member not found")
	ErrInvalidJoinCode     = errors.New("invalid classroom join code")
	ErrJoinCodeTaken       = errors.New("classroom join code already in use")
	ErrIdempotencyConflict = errors.New("classroom client_id was reused with a different payload")
	ErrInvalidInput        = errors.New("invalid classroom input")
)

var readRandom = rand.Read

// Store is the persistence contract required by the classroom domain. Both
// the DynamoDB Store and MemoryStore implement it, which keeps handler tests
// deterministic without creating a second business path.
type Store interface {
	CreateClassroom(context.Context, *model.Classroom) error
	GetClassroom(context.Context, string) (*model.Classroom, error)
	ListClassroomsByOwner(context.Context, string, int) ([]model.Classroom, error)
	ListClassroomsForMember(context.Context, string, int) ([]model.Classroom, error)
	UpdateClassroom(context.Context, string, string, model.ClassroomPatch) (*model.Classroom, error)
	DeleteClassroom(context.Context, string, string) error
	FindClassroomByJoinCode(context.Context, string) (*model.Classroom, error)
	AddClassroomMember(context.Context, *model.ClassroomMember) error
	ListClassroomMembers(context.Context, string) ([]model.ClassroomMember, error)
	IsClassroomMember(context.Context, string, string) (bool, error)
	RemoveClassroomMember(context.Context, string, string, string) error
}

type idempotentCreateStore interface {
	CreateClassroomWithClientID(context.Context, *model.Classroom, string, string) (*model.Classroom, error)
}

type invitationStore interface {
	CreateClassroomInvitation(context.Context, string, *model.ClassroomInvitation) error
	ListClassroomInvitations(context.Context, string, string) ([]model.ClassroomInvitation, error)
	FindClassroomInvitationByJoinCode(context.Context, string) (*model.ClassroomInvitation, error)
	DeleteClassroomInvitation(context.Context, string, string, string) error
	AcceptClassroomInvitation(context.Context, string, string, *model.ClassroomMember) error
}

type leaveStore interface {
	LeaveClassroom(context.Context, string, string) error
}

type memberProfileStore interface {
	GetUser(context.Context, string) (*model.User, error)
}

type Service struct {
	Store Store
	Now   func() time.Time
}

func New(store Store) *Service {
	return &Service{Store: store, Now: time.Now}
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) available() error {
	if s == nil || s.Store == nil {
		return ErrStoreUnavailable
	}
	return nil
}

type CreateInput struct {
	Name        string
	Description string
	ClientID    string
}

type UpdateInput struct {
	Name        *string
	Description *string
}

func (s *Service) Create(ctx context.Context, user *model.User, input CreateInput) (*model.Classroom, error) {
	if err := requireTeacher(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	name, description, err := validateCreateInput(input)
	if err != nil {
		return nil, err
	}
	clientID := strings.TrimSpace(input.ClientID)
	if len(clientID) > 128 {
		return nil, fmt.Errorf("%w: client_id must be at most 128 characters", ErrInvalidInput)
	}
	payloadHash := ""
	if clientID != "" {
		sum := sha256.Sum256([]byte(name + "\x00" + description))
		payloadHash = hex.EncodeToString(sum[:])
	}
	// A code collision is extremely unlikely, but the persistence layer keeps
	// it conditional and the service retries without exposing the race.
	for attempt := 0; attempt < 5; attempt++ {
		now := s.now().UTC().Format(time.RFC3339Nano)
		classroom := &model.Classroom{
			ID:          "class-" + randomHex(12),
			OwnerID:     user.UserID,
			Name:        name,
			Description: description,
			JoinCode:    randomJoinCode(),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		var createErr error
		if clientID != "" {
			if store, ok := s.Store.(idempotentCreateStore); ok {
				classroom, createErr = store.CreateClassroomWithClientID(ctx, classroom, clientID, payloadHash)
			} else {
				createErr = ErrStoreUnavailable
			}
		} else {
			createErr = s.Store.CreateClassroom(ctx, classroom)
		}
		if createErr != nil {
			if errors.Is(createErr, store.ErrClassroomCodeTaken) {
				continue
			}
			return nil, translateStoreError(createErr)
		}
		return classroom, nil
	}
	return nil, ErrJoinCodeTaken
}

func (s *Service) ListForUser(ctx context.Context, user *model.User, limit int) ([]model.Classroom, error) {
	if user == nil {
		return nil, auth.ErrAuthenticationRequired
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	limit = clampLimit(limit)
	var (
		rows []model.Classroom
		err  error
	)
	switch user.Role {
	case auth.RoleTeacher:
		rows, err = s.Store.ListClassroomsByOwner(ctx, user.UserID, limit)
	case auth.RoleStudent:
		rows, err = s.Store.ListClassroomsForMember(ctx, user.UserID, limit)
	default:
		return nil, ErrClassroomForbidden
	}
	if err != nil {
		return nil, translateStoreError(err)
	}
	if user.Role != auth.RoleTeacher {
		for i := range rows {
			rows[i].JoinCode = ""
		}
	}
	return rows, nil
}

func (s *Service) GetForUser(ctx context.Context, user *model.User, classroomID string) (*model.Classroom, error) {
	if user == nil {
		return nil, auth.ErrAuthenticationRequired
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	classroom, err := s.Store.GetClassroom(ctx, strings.TrimSpace(classroomID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	if classroom == nil {
		return nil, ErrClassroomNotFound
	}
	if err := s.authorizeView(ctx, user, classroom); err != nil {
		return nil, err
	}
	return visibleToUser(classroom, user), nil
}

func (s *Service) Update(ctx context.Context, user *model.User, classroomID string, input UpdateInput) (*model.Classroom, error) {
	if err := requireTeacher(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	patch, err := validateUpdateInput(input)
	if err != nil {
		return nil, err
	}
	updated, err := s.Store.UpdateClassroom(ctx, strings.TrimSpace(classroomID), user.UserID, patch)
	if err != nil {
		return nil, translateStoreError(err)
	}
	if updated == nil {
		return nil, ErrClassroomNotFound
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, user *model.User, classroomID string) error {
	if err := requireTeacher(user); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	err := s.Store.DeleteClassroom(ctx, strings.TrimSpace(classroomID), user.UserID)
	return translateStoreError(err)
}

func (s *Service) Join(ctx context.Context, user *model.User, joinCode, expectedClassroomID string) (*model.Classroom, error) {
	if err := requireStudent(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	joinCode = strings.ToUpper(strings.TrimSpace(joinCode))
	if !validJoinCode(joinCode) {
		return nil, ErrInvalidJoinCode
	}
	if invitations, ok := s.Store.(invitationStore); ok {
		invitation, err := invitations.FindClassroomInvitationByJoinCode(ctx, joinCode)
		if err != nil {
			return nil, translateStoreError(err)
		}
		if invitation != nil {
			classroom, err := s.Store.GetClassroom(ctx, invitation.ClassroomID)
			if err != nil {
				return nil, translateStoreError(err)
			}
			if classroom == nil || (expectedClassroomID != "" && classroom.ID != strings.TrimSpace(expectedClassroomID)) {
				return nil, ErrClassroomNotFound
			}
			if invitation.Status == "accepted" && invitation.AcceptedBy == user.UserID {
				member, err := s.Store.IsClassroomMember(ctx, classroom.ID, user.UserID)
				if err != nil {
					return nil, translateStoreError(err)
				}
				if member {
					return visibleToUser(classroom, user), nil
				}
			}
			if invitation.Status != "pending" || !strings.EqualFold(invitation.Email, user.Email) {
				return nil, ErrInvalidJoinCode
			}
			member := &model.ClassroomMember{ClassroomID: classroom.ID, UserID: user.UserID, Name: user.Name, Email: user.Email, Role: auth.RoleStudent, JoinedAt: s.now().UTC().Format(time.RFC3339Nano)}
			if err := invitations.AcceptClassroomInvitation(ctx, classroom.ID, invitation.ID, member); err != nil {
				return nil, translateStoreError(err)
			}
			return visibleToUser(classroom, user), nil
		}
	}
	classroom, err := s.Store.FindClassroomByJoinCode(ctx, joinCode)
	if err != nil {
		return nil, translateStoreError(err)
	}
	if classroom == nil || (expectedClassroomID != "" && classroom.ID != strings.TrimSpace(expectedClassroomID)) {
		return nil, ErrClassroomNotFound
	}
	member := &model.ClassroomMember{ClassroomID: classroom.ID, UserID: user.UserID, Name: user.Name, Email: user.Email, Role: auth.RoleStudent, JoinedAt: s.now().UTC().Format(time.RFC3339Nano)}
	if err := s.Store.AddClassroomMember(ctx, member); err != nil {
		return nil, translateStoreError(err)
	}
	return visibleToUser(classroom, user), nil
}

func (s *Service) Invite(ctx context.Context, user *model.User, classroomID, name, email string) (*model.ClassroomInvitation, error) {
	if err := requireTeacher(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	invitations, ok := s.Store.(invitationStore)
	if !ok {
		return nil, ErrStoreUnavailable
	}
	name = strings.TrimSpace(name)
	email = auth.NormalizeEmail(email)
	parsed, err := mail.ParseAddress(email)
	if name == "" || len([]rune(name)) > 80 || err != nil || !strings.EqualFold(parsed.Address, email) || !auth.ValidEmail(email) {
		return nil, fmt.Errorf("%w: a name and valid email are required", ErrInvalidInput)
	}
	classroom, err := s.Store.GetClassroom(ctx, strings.TrimSpace(classroomID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	if classroom == nil {
		return nil, ErrClassroomNotFound
	}
	if classroom.OwnerID != user.UserID {
		return nil, ErrClassroomForbidden
	}
	rows, err := invitations.ListClassroomInvitations(ctx, user.UserID, classroom.ID)
	if err != nil {
		return nil, translateStoreError(err)
	}
	for _, existing := range rows {
		if existing.Status == "pending" && strings.EqualFold(existing.Email, email) {
			return &existing, nil
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		invitation := &model.ClassroomInvitation{ID: "invite-" + randomHex(12), ClassroomID: classroom.ID, Name: name, Email: email, Status: "pending", CreatedAt: s.now().UTC().Format(time.RFC3339Nano), JoinCode: randomJoinCode()}
		if err := invitations.CreateClassroomInvitation(ctx, user.UserID, invitation); err != nil {
			if errors.Is(err, store.ErrClassroomCodeTaken) {
				continue
			}
			return nil, translateStoreError(err)
		}
		return invitation, nil
	}
	return nil, ErrJoinCodeTaken
}

func (s *Service) ListInvitations(ctx context.Context, user *model.User, classroomID string) ([]model.ClassroomInvitation, error) {
	if err := requireTeacher(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	invitations, ok := s.Store.(invitationStore)
	if !ok {
		return nil, ErrStoreUnavailable
	}
	rows, err := invitations.ListClassroomInvitations(ctx, user.UserID, strings.TrimSpace(classroomID))
	return rows, translateStoreError(err)
}

func (s *Service) DeleteInvitation(ctx context.Context, user *model.User, classroomID, invitationID string) error {
	if err := requireTeacher(user); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	invitations, ok := s.Store.(invitationStore)
	if !ok {
		return ErrStoreUnavailable
	}
	return translateStoreError(invitations.DeleteClassroomInvitation(ctx, user.UserID, strings.TrimSpace(classroomID), strings.TrimSpace(invitationID)))
}

func (s *Service) Leave(ctx context.Context, user *model.User, classroomID string) error {
	if err := requireStudent(user); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	leaver, ok := s.Store.(leaveStore)
	if !ok {
		return ErrStoreUnavailable
	}
	return translateStoreError(leaver.LeaveClassroom(ctx, strings.TrimSpace(classroomID), user.UserID))
}

func (s *Service) ListMembers(ctx context.Context, user *model.User, classroomID string) ([]model.ClassroomMember, error) {
	if err := requireTeacher(user); err != nil {
		return nil, err
	}
	if err := s.available(); err != nil {
		return nil, err
	}
	classroom, err := s.Store.GetClassroom(ctx, strings.TrimSpace(classroomID))
	if err != nil {
		return nil, translateStoreError(err)
	}
	if classroom == nil {
		return nil, ErrClassroomNotFound
	}
	if classroom.OwnerID != user.UserID {
		return nil, ErrClassroomForbidden
	}
	rows, err := s.Store.ListClassroomMembers(ctx, classroom.ID)
	if err != nil {
		return nil, translateStoreError(err)
	}
	if profiles, ok := s.Store.(memberProfileStore); ok {
		for i := range rows {
			profile, err := profiles.GetUser(ctx, rows[i].UserID)
			if err != nil {
				return nil, translateStoreError(err)
			}
			if profile != nil && strings.TrimSpace(profile.Name) != "" {
				rows[i].Name = profile.Name
			}
		}
	}
	return rows, nil
}

func (s *Service) RemoveMember(ctx context.Context, user *model.User, classroomID, memberID string) error {
	if err := requireTeacher(user); err != nil {
		return err
	}
	if err := s.available(); err != nil {
		return err
	}
	classroom, err := s.Store.GetClassroom(ctx, strings.TrimSpace(classroomID))
	if err != nil {
		return translateStoreError(err)
	}
	if classroom == nil {
		return ErrClassroomNotFound
	}
	if classroom.OwnerID != user.UserID {
		return ErrClassroomForbidden
	}
	memberID = strings.TrimSpace(memberID)
	if memberID == "" || memberID == user.UserID {
		return ErrMemberNotFound
	}
	return translateStoreError(s.Store.RemoveClassroomMember(ctx, classroom.ID, user.UserID, memberID))
}

func (s *Service) authorizeView(ctx context.Context, user *model.User, classroom *model.Classroom) error {
	if user.Role == auth.RoleTeacher && classroom.OwnerID == user.UserID {
		return nil
	}
	if user.Role != auth.RoleStudent {
		return ErrClassroomForbidden
	}
	member, err := s.Store.IsClassroomMember(ctx, classroom.ID, user.UserID)
	if err != nil {
		return translateStoreError(err)
	}
	if !member {
		return ErrClassroomForbidden
	}
	return nil
}

func visibleToUser(classroom *model.Classroom, user *model.User) *model.Classroom {
	if classroom == nil {
		return nil
	}
	v := *classroom
	if user == nil || user.Role != auth.RoleTeacher || user.UserID != classroom.OwnerID {
		v.JoinCode = ""
	}
	return &v
}

func requireTeacher(user *model.User) error {
	if user == nil {
		return auth.ErrAuthenticationRequired
	}
	if user.Role != auth.RoleTeacher {
		return ErrTeacherRequired
	}
	return nil
}

func requireStudent(user *model.User) error {
	if user == nil {
		return auth.ErrAuthenticationRequired
	}
	if user.Role != auth.RoleStudent {
		return ErrStudentRequired
	}
	return nil
}

func validateCreateInput(input CreateInput) (string, string, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 100 {
		return "", "", fmt.Errorf("%w: name is required and must be at most 100 characters", ErrInvalidInput)
	}
	description := strings.TrimSpace(input.Description)
	if len([]rune(description)) > 1000 {
		return "", "", fmt.Errorf("%w: description must be at most 1000 characters", ErrInvalidInput)
	}
	return name, description, nil
}

func validateUpdateInput(input UpdateInput) (model.ClassroomPatch, error) {
	patch := model.ClassroomPatch{Name: input.Name, Description: input.Description}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len([]rune(name)) > 100 {
			return model.ClassroomPatch{}, fmt.Errorf("%w: name must be at most 100 characters", ErrInvalidInput)
		}
		patch.Name = &name
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if len([]rune(description)) > 1000 {
			return model.ClassroomPatch{}, fmt.Errorf("%w: description must be at most 1000 characters", ErrInvalidInput)
		}
		patch.Description = &description
	}
	if patch.Name == nil && patch.Description == nil {
		return model.ClassroomPatch{}, fmt.Errorf("%w: at least one field is required", ErrInvalidInput)
	}
	return patch, nil
}

func validJoinCode(code string) bool {
	if len(code) < 6 || len(code) > 12 {
		return false
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

const joinCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomJoinCode() string {
	bytes := make([]byte, 8)
	if _, err := readRandom(bytes); err != nil {
		return strings.ToUpper(randomHex(4))
	}
	for i, value := range bytes {
		bytes[i] = joinCodeAlphabet[int(value)%len(joinCodeAlphabet)]
	}
	return string(bytes)
}

func randomHex(bytes int) string {
	if bytes <= 0 {
		return ""
	}
	data := make([]byte, bytes)
	if _, err := readRandom(data); err == nil {
		return hex.EncodeToString(data)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func translateStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrClassroomNotFound
	case errors.Is(err, store.ErrClassroomForbidden):
		return ErrClassroomForbidden
	case errors.Is(err, store.ErrClassroomMember):
		return ErrAlreadyMember
	case errors.Is(err, store.ErrClassroomMemberNotFound):
		return ErrMemberNotFound
	case errors.Is(err, store.ErrClassroomInvitation), errors.Is(err, store.ErrClassroomInvitationUsed):
		return ErrInvalidJoinCode
	case errors.Is(err, store.ErrClassroomIdempotencyConflict):
		return ErrIdempotencyConflict
	case errors.Is(err, store.ErrClassroomCodeTaken):
		return ErrJoinCodeTaken
	default:
		return err
	}
}
