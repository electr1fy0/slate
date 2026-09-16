package classrooms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

var classroomTestTime = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func classroomService(store *classroomStoreStub) *Service {
	service := New(nil)
	if store != nil {
		service.Store = store
	}
	service.Now = func() time.Time { return classroomTestTime }
	return service
}

func classroomFixture() *model.Classroom {
	return &model.Classroom{
		ID: "class-1", OwnerID: "teacher-1", Name: "Physics", Description: "Motion",
		JoinCode: "ABC234", CreatedAt: classroomTestTime.Format(time.RFC3339Nano), UpdatedAt: classroomTestTime.Format(time.RFC3339Nano), MemberCount: 1,
	}
}

func TestClassroomServiceValidationAndHelpers(t *testing.T) {
	if New(nil) == nil || (&Service{}).now().IsZero() {
		t.Fatal("service constructor or clock failed")
	}
	var nilService *Service
	if !errors.Is(nilService.available(), ErrStoreUnavailable) || !errors.Is((&Service{}).available(), ErrStoreUnavailable) {
		t.Fatal("unavailable service was not rejected")
	}
	teacher := &model.User{UserID: "teacher-1", Role: auth.RoleTeacher}
	student := &model.User{UserID: "student-1", Role: auth.RoleStudent}
	for _, tc := range []struct {
		user *model.User
		want error
	}{
		{nil, auth.ErrAuthenticationRequired},
		{student, ErrTeacherRequired},
	} {
		if err := requireTeacher(tc.user); !errors.Is(err, tc.want) {
			t.Fatalf("requireTeacher(%#v) = %v", tc.user, err)
		}
	}
	if requireTeacher(teacher) != nil {
		t.Fatal("teacher rejected")
	}
	for _, tc := range []struct {
		user *model.User
		want error
	}{
		{nil, auth.ErrAuthenticationRequired},
		{teacher, ErrStudentRequired},
	} {
		if err := requireStudent(tc.user); !errors.Is(err, tc.want) {
			t.Fatalf("requireStudent(%#v) = %v", tc.user, err)
		}
	}
	if requireStudent(student) != nil {
		t.Fatal("student rejected")
	}
	if visibleToUser(nil, teacher) != nil {
		t.Fatal("nil classroom should remain nil")
	}
	classroom := classroomFixture()
	if visibleToUser(classroom, teacher).JoinCode == "" || visibleToUser(classroom, student).JoinCode != "" || visibleToUser(classroom, &model.User{UserID: "other", Role: auth.RoleTeacher}).JoinCode != "" {
		t.Fatal("classroom visibility/redaction failed")
	}
	if classroom.JoinCode == "" {
		t.Fatal("visibility mutated source classroom")
	}

	if _, _, err := validateCreateInput(CreateInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty create input accepted")
	}
	if _, _, err := validateCreateInput(CreateInput{Name: strings.Repeat("x", 101)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("long create name accepted")
	}
	if _, _, err := validateCreateInput(CreateInput{Name: "ok", Description: strings.Repeat("x", 1001)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("long create description accepted")
	}
	name, description, err := validateCreateInput(CreateInput{Name: "  Class ", Description: "  Subject "})
	if err != nil || name != "Class" || description != "Subject" {
		t.Fatalf("valid create input = %q %q %v", name, description, err)
	}
	trimmed := "  Updated "
	desc := "  Description "
	patch, err := validateUpdateInput(UpdateInput{Name: &trimmed, Description: &desc})
	if err != nil || *patch.Name != "Updated" || *patch.Description != "Description" {
		t.Fatalf("valid update input = %#v, %v", patch, err)
	}
	empty := " "
	if _, err := validateUpdateInput(UpdateInput{Name: &empty}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty update name accepted")
	}
	if _, err := validateUpdateInput(UpdateInput{Name: ptrString(strings.Repeat("x", 101))}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("long update name accepted")
	}
	if _, err := validateUpdateInput(UpdateInput{Description: ptrString(strings.Repeat("x", 1001))}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("long update description accepted")
	}
	if _, err := validateUpdateInput(UpdateInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty update accepted")
	}
	for _, code := range []string{"", "ABC", "ABC2345678901", "ABC-234", "abc234"} {
		if validJoinCode(code) {
			t.Fatalf("invalid join code accepted: %q", code)
		}
	}
	if !validJoinCode("ABC234") || !validJoinCode("ABC234567890") {
		t.Fatal("valid join code rejected")
	}
	if clampLimit(0) != 50 || clampLimit(-1) != 50 || clampLimit(101) != 100 || clampLimit(12) != 12 {
		t.Fatal("limit clamping failed")
	}

	originalRandom := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("random unavailable") }
	if randomJoinCode() == "" || randomHex(4) == "" || randomHex(0) != "" || randomHex(-1) != "" {
		t.Fatal("random fallbacks failed")
	}
	readRandom = originalRandom

	for _, tc := range []struct {
		input error
		want  error
	}{
		{nil, nil},
		{store.ErrNotFound, ErrClassroomNotFound},
		{store.ErrClassroomForbidden, ErrClassroomForbidden},
		{store.ErrClassroomMember, ErrAlreadyMember},
		{store.ErrClassroomMemberNotFound, ErrMemberNotFound},
		{store.ErrClassroomCodeTaken, ErrJoinCodeTaken},
		{errors.New("other"), errors.New("other")},
	} {
		got := translateStoreError(tc.input)
		if tc.want == nil && got != nil {
			t.Fatalf("translate nil = %v", got)
		}
		if tc.want != nil && got.Error() != tc.want.Error() {
			t.Fatalf("translate %v = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestClassroomServiceCRUDAndMembershipEdges(t *testing.T) {
	ctx := context.Background()
	teacher := &model.User{UserID: "teacher-1", Name: "Teacher", Email: "teacher@example.com", Role: auth.RoleTeacher}
	student := &model.User{UserID: "student-1", Name: "Student", Email: "student@example.com", Role: auth.RoleStudent}
	otherStudent := &model.User{UserID: "student-2", Name: "Other", Role: auth.RoleStudent}
	classroom := classroomFixture()

	if _, err := classroomService(nil).Create(ctx, teacher, CreateInput{Name: "Class"}); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("create unavailable = %v", err)
	}
	if _, err := classroomService(&classroomStoreStub{}).Create(ctx, student, CreateInput{Name: "Class"}); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("create student = %v", err)
	}
	if _, err := classroomService(&classroomStoreStub{}).Create(ctx, teacher, CreateInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("create invalid = %v", err)
	}
	createdStore := &classroomStoreStub{}
	created, err := classroomService(createdStore).Create(ctx, teacher, CreateInput{Name: " New class ", Description: " Subject "})
	if err != nil || created.Name != "New class" || created.Description != "Subject" || created.OwnerID != teacher.UserID || created.JoinCode == "" {
		t.Fatalf("create success = %#v, %v", created, err)
	}
	collisionStore := &classroomStoreStub{createErrs: []error{store.ErrClassroomCodeTaken}}
	if _, err := classroomService(collisionStore).Create(ctx, teacher, CreateInput{Name: "Retry"}); err != nil {
		t.Fatalf("create collision retry = %v", err)
	}
	for i := 0; i < 5; i++ {
		collisionStore.createErrs = append(collisionStore.createErrs, store.ErrClassroomCodeTaken)
	}
	if _, err := classroomService(collisionStore).Create(ctx, teacher, CreateInput{Name: "Exhausted"}); !errors.Is(err, ErrJoinCodeTaken) {
		t.Fatalf("create collision exhaustion = %v", err)
	}
	for _, storeErr := range []error{store.ErrNotFound, errors.New("write failure")} {
		if _, err := classroomService(&classroomStoreStub{createErrs: []error{storeErr}}).Create(ctx, teacher, CreateInput{Name: "Failure"}); err == nil {
			t.Fatal("create store error was ignored")
		}
	}

	listStore := &classroomStoreStub{ownerRows: []model.Classroom{*classroom}, memberRows: []model.Classroom{*classroom}}
	if _, err := classroomService(listStore).ListForUser(ctx, nil, 0); !errors.Is(err, auth.ErrAuthenticationRequired) {
		t.Fatalf("list nil user = %v", err)
	}
	if _, err := classroomService(nil).ListForUser(ctx, teacher, 0); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("list unavailable = %v", err)
	}
	rows, err := classroomService(listStore).ListForUser(ctx, teacher, 0)
	if err != nil || len(rows) != 1 || rows[0].JoinCode == "" {
		t.Fatalf("teacher list = %#v, %v", rows, err)
	}
	rows, err = classroomService(listStore).ListForUser(ctx, student, 101)
	if err != nil || len(rows) != 1 || rows[0].JoinCode != "" {
		t.Fatalf("student list = %#v, %v", rows, err)
	}
	if _, err := classroomService(&classroomStoreStub{ownerRows: []model.Classroom{}, listErr: store.ErrNotFound}).ListForUser(ctx, teacher, 1); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("list store error = %v", err)
	}
	if _, err := classroomService(listStore).ListForUser(ctx, &model.User{Role: "admin"}, 1); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("list unknown role = %v", err)
	}

	getStore := &classroomStoreStub{classroom: classroom}
	if _, err := classroomService(getStore).GetForUser(ctx, nil, "class-1"); !errors.Is(err, auth.ErrAuthenticationRequired) {
		t.Fatalf("get nil user = %v", err)
	}
	if _, err := classroomService(nil).GetForUser(ctx, teacher, "class-1"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("get unavailable = %v", err)
	}
	getStore.getErr = store.ErrNotFound
	if _, err := classroomService(getStore).GetForUser(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("get store error = %v", err)
	}
	getStore.getErr = nil
	getStore.classroom = nil
	if _, err := classroomService(getStore).GetForUser(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("get missing = %v", err)
	}
	getStore.classroom = classroom
	if got, err := classroomService(getStore).GetForUser(ctx, teacher, " class-1 "); err != nil || got.JoinCode == "" {
		t.Fatalf("owner get = %#v, %v", got, err)
	}
	getStore.member = true
	if got, err := classroomService(getStore).GetForUser(ctx, student, "class-1"); err != nil || got.JoinCode != "" {
		t.Fatalf("member get = %#v, %v", got, err)
	}
	getStore.member = false
	if _, err := classroomService(getStore).GetForUser(ctx, otherStudent, "class-1"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("outsider get = %v", err)
	}
	getStore.memberErr = store.ErrNotFound
	if _, err := classroomService(getStore).GetForUser(ctx, student, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("member lookup error = %v", err)
	}
	if _, err := classroomService(getStore).GetForUser(ctx, &model.User{Role: "admin"}, "class-1"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("admin get = %v", err)
	}

	updated := *classroom
	updated.Name = "Updated"
	updateStore := &classroomStoreStub{updated: &updated}
	if _, err := classroomService(updateStore).Update(ctx, student, "class-1", UpdateInput{Name: ptrString("x")}); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student update = %v", err)
	}
	if _, err := classroomService(nil).Update(ctx, teacher, "class-1", UpdateInput{Name: ptrString("x")}); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("update unavailable = %v", err)
	}
	if _, err := classroomService(updateStore).Update(ctx, teacher, "class-1", UpdateInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update invalid = %v", err)
	}
	updateStore.updateErr = store.ErrNotFound
	if _, err := classroomService(updateStore).Update(ctx, teacher, " class-1 ", UpdateInput{Name: ptrString("x")}); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("update store error = %v", err)
	}
	updateStore.updateErr = nil
	updateStore.updated = nil
	if _, err := classroomService(updateStore).Update(ctx, teacher, "class-1", UpdateInput{Name: ptrString("x")}); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("update missing = %v", err)
	}
	updateStore.updated = &updated
	if got, err := classroomService(updateStore).Update(ctx, teacher, "class-1", UpdateInput{Description: ptrString("desc")}); err != nil || got.Name != "Updated" {
		t.Fatalf("update success = %#v, %v", got, err)
	}

	deleteStore := &classroomStoreStub{}
	if err := classroomService(deleteStore).Delete(ctx, student, "class-1"); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student delete = %v", err)
	}
	if err := classroomService(nil).Delete(ctx, teacher, "class-1"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("delete unavailable = %v", err)
	}
	deleteStore.deleteErr = store.ErrNotFound
	if err := classroomService(deleteStore).Delete(ctx, teacher, " class-1 "); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("delete error = %v", err)
	}
	deleteStore.deleteErr = nil
	if err := classroomService(deleteStore).Delete(ctx, teacher, "class-1"); err != nil {
		t.Fatal(err)
	}

	joinStore := &classroomStoreStub{findClassroom: classroom}
	if _, err := classroomService(joinStore).Join(ctx, teacher, "ABC234", ""); !errors.Is(err, ErrStudentRequired) {
		t.Fatalf("teacher join = %v", err)
	}
	if _, err := classroomService(nil).Join(ctx, student, "ABC234", ""); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("join unavailable = %v", err)
	}
	if _, err := classroomService(joinStore).Join(ctx, student, "bad", ""); !errors.Is(err, ErrInvalidJoinCode) {
		t.Fatalf("invalid join code = %v", err)
	}
	joinStore.findErr = store.ErrNotFound
	if _, err := classroomService(joinStore).Join(ctx, student, "ABC234", ""); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("join lookup error = %v", err)
	}
	joinStore.findErr = nil
	joinStore.findClassroom = nil
	if _, err := classroomService(joinStore).Join(ctx, student, "ABC234", ""); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("join missing = %v", err)
	}
	joinStore.findClassroom = classroom
	if _, err := classroomService(joinStore).Join(ctx, student, "ABC234", "other"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("join expected ID mismatch = %v", err)
	}
	joinStore.addErr = store.ErrClassroomMember
	if _, err := classroomService(joinStore).Join(ctx, student, " abc234 ", " class-1 "); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("join add error = %v", err)
	}
	joinStore.addErr = nil
	if got, err := classroomService(joinStore).Join(ctx, student, " abc234 ", " class-1 "); err != nil || got.JoinCode != "" {
		t.Fatalf("join success = %#v, %v", got, err)
	}

	memberStore := &classroomStoreStub{classroom: classroom, rosterRows: []model.ClassroomMember{{UserID: "student-1"}}}
	if _, err := classroomService(memberStore).ListMembers(ctx, student, "class-1"); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student roster = %v", err)
	}
	if _, err := classroomService(nil).ListMembers(ctx, teacher, "class-1"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("roster unavailable = %v", err)
	}
	memberStore.getErr = store.ErrNotFound
	if _, err := classroomService(memberStore).ListMembers(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("roster get error = %v", err)
	}
	memberStore.getErr = nil
	memberStore.classroom = nil
	if _, err := classroomService(memberStore).ListMembers(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("roster missing class = %v", err)
	}
	memberStore.classroom = classroom
	memberStore.classroom.OwnerID = "other-teacher"
	if _, err := classroomService(memberStore).ListMembers(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("roster owner check = %v", err)
	}
	memberStore.classroom.OwnerID = teacher.UserID
	memberStore.listErr = store.ErrNotFound
	if _, err := classroomService(memberStore).ListMembers(ctx, teacher, "class-1"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("roster list error = %v", err)
	}
	memberStore.listErr = nil
	if rows, err := classroomService(memberStore).ListMembers(ctx, teacher, "class-1"); err != nil || len(rows) != 1 {
		t.Fatalf("roster success = %#v, %v", rows, err)
	}

	removeStore := &classroomStoreStub{classroom: classroom}
	if err := classroomService(removeStore).RemoveMember(ctx, student, "class-1", "member"); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student remove = %v", err)
	}
	if err := classroomService(nil).RemoveMember(ctx, teacher, "class-1", "member"); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("remove unavailable = %v", err)
	}
	removeStore.getErr = store.ErrNotFound
	if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", "member"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("remove get error = %v", err)
	}
	removeStore.getErr = nil
	removeStore.classroom = nil
	if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", "member"); !errors.Is(err, ErrClassroomNotFound) {
		t.Fatalf("remove missing = %v", err)
	}
	removeStore.classroom = classroom
	removeStore.classroom.OwnerID = "other"
	if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", "member"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("remove owner check = %v", err)
	}
	removeStore.classroom.OwnerID = teacher.UserID
	for _, memberID := range []string{"", " ", "teacher-1"} {
		if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", memberID); !errors.Is(err, ErrMemberNotFound) {
			t.Fatalf("remove invalid member %q = %v", memberID, err)
		}
	}
	removeStore.removeErr = store.ErrClassroomMemberNotFound
	if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", "student-1"); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("remove store error = %v", err)
	}
	removeStore.removeErr = nil
	if err := classroomService(removeStore).RemoveMember(ctx, teacher, "class-1", " student-1 "); err != nil {
		t.Fatal(err)
	}
}

type classroomStoreStub struct {
	createErrs    []error
	classroom     *model.Classroom
	getErr        error
	ownerRows     []model.Classroom
	memberRows    []model.Classroom
	rosterRows    []model.ClassroomMember
	listErr       error
	updated       *model.Classroom
	updateErr     error
	deleteErr     error
	findClassroom *model.Classroom
	findErr       error
	addErr        error
	member        bool
	memberErr     error
	removeErr     error
}

func (s *classroomStoreStub) CreateClassroom(context.Context, *model.Classroom) error {
	if len(s.createErrs) == 0 {
		return nil
	}
	err := s.createErrs[0]
	s.createErrs = s.createErrs[1:]
	return err
}
func (s *classroomStoreStub) GetClassroom(context.Context, string) (*model.Classroom, error) {
	return s.classroom, s.getErr
}
func (s *classroomStoreStub) ListClassroomsByOwner(context.Context, string, int) ([]model.Classroom, error) {
	return s.ownerRows, s.listErr
}
func (s *classroomStoreStub) ListClassroomsForMember(context.Context, string, int) ([]model.Classroom, error) {
	return s.memberRows, s.listErr
}
func (s *classroomStoreStub) UpdateClassroom(context.Context, string, string, model.ClassroomPatch) (*model.Classroom, error) {
	return s.updated, s.updateErr
}
func (s *classroomStoreStub) DeleteClassroom(context.Context, string, string) error {
	return s.deleteErr
}
func (s *classroomStoreStub) FindClassroomByJoinCode(context.Context, string) (*model.Classroom, error) {
	return s.findClassroom, s.findErr
}
func (s *classroomStoreStub) AddClassroomMember(context.Context, *model.ClassroomMember) error {
	return s.addErr
}
func (s *classroomStoreStub) ListClassroomMembers(context.Context, string) ([]model.ClassroomMember, error) {
	return s.rosterRows, s.listErr
}
func (s *classroomStoreStub) IsClassroomMember(context.Context, string, string) (bool, error) {
	return s.member, s.memberErr
}
func (s *classroomStoreStub) RemoveClassroomMember(context.Context, string, string, string) error {
	return s.removeErr
}

func ptrString(value string) *string { return &value }
