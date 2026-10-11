package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

func TestMemoryQOLWritesArePersistentAndOwnerScoped(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	if err := m.CreateUser(ctx, &model.User{UserID: "teacher", Email: "teacher@example.test", Name: "Teacher"}, "old-hash"); err != nil {
		t.Fatal(err)
	}
	classroom, err := m.CreateClassroomWithClientID(ctx, &model.Classroom{ID: "class-1", OwnerID: "teacher", Name: "Science", JoinCode: "CLASS123"}, "request-1", "payload-hash")
	if err != nil || classroom == nil {
		t.Fatalf("create idempotent classroom = %#v, %v", classroom, err)
	}
	retry, err := m.CreateClassroomWithClientID(ctx, &model.Classroom{ID: "generated-again", OwnerID: "teacher", Name: "Science", JoinCode: "OTHER123"}, "request-1", "payload-hash")
	if err != nil || retry == nil || retry.ID != classroom.ID {
		t.Fatalf("classroom retry = %#v, %v", retry, err)
	}
	if _, err := m.CreateClassroomWithClientID(ctx, &model.Classroom{OwnerID: "teacher", Name: "Changed", JoinCode: "OTHER234"}, "request-1", "different-hash"); !errors.Is(err, ErrClassroomIdempotencyConflict) {
		t.Fatalf("idempotency key reuse = %v", err)
	}

	invitation := &model.ClassroomInvitation{ID: "invite-1", ClassroomID: classroom.ID, Name: "Student", Email: "student@example.test", JoinCode: "INVITE12", Status: "pending", CreatedAt: iso(m.now())}
	if err := m.CreateClassroomInvitation(ctx, "teacher", invitation); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListClassroomInvitations(ctx, "teacher", classroom.ID)
	if err != nil || len(rows) != 1 || rows[0].Status != "pending" || rows[0].JoinCode != "INVITE12" {
		t.Fatalf("invitation list = %#v, %v", rows, err)
	}
	if _, err := m.ListClassroomInvitations(ctx, "other-teacher", classroom.ID); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("foreign invitation list = %v", err)
	}
	if found, err := m.FindClassroomInvitationByJoinCode(ctx, "invite12"); err != nil || found == nil || found.ID != invitation.ID {
		t.Fatalf("invitation lookup = %#v, %v", found, err)
	}
	wrongEmail := &model.ClassroomMember{UserID: "wrong", Email: "wrong@example.test"}
	if err := m.AcceptClassroomInvitation(ctx, classroom.ID, invitation.ID, wrongEmail); !errors.Is(err, ErrClassroomInvitation) {
		t.Fatalf("wrong-email acceptance = %v", err)
	}
	member := &model.ClassroomMember{UserID: "student", Name: "Student", Email: "student@example.test", Role: "student"}
	if err := m.AcceptClassroomInvitation(ctx, classroom.ID, invitation.ID, member); err != nil {
		t.Fatal(err)
	}
	if err := m.AcceptClassroomInvitation(ctx, classroom.ID, invitation.ID, member); !errors.Is(err, ErrClassroomInvitationUsed) {
		t.Fatalf("invite accepted twice = %v", err)
	}
	rows, err = m.ListClassroomInvitations(ctx, "teacher", classroom.ID)
	if err != nil || rows[0].Status != "accepted" {
		t.Fatalf("accepted status was not persisted: %#v, %v", rows, err)
	}
	if err := m.LeaveClassroom(ctx, classroom.ID, member.UserID); err != nil {
		t.Fatal(err)
	}
	if isMember, err := m.IsClassroomMember(ctx, classroom.ID, member.UserID); err != nil || isMember {
		t.Fatalf("leave did not remove membership: member=%v err=%v", isMember, err)
	}

	item, err := m.SaveQuestionBankItem(ctx, &model.QuestionBankItem{OwnerID: "teacher", Question: "What is 2+2?", Options: []string{"3", "4"}, Answer: 1, Points: 2, Feedback: "Add two."})
	if err != nil || item == nil || item.ID == "" || item.SavedAt == "" {
		t.Fatalf("save question bank item = %#v, %v", item, err)
	}
	items, err := m.ListQuestionBank(ctx, "teacher")
	if err != nil || len(items) != 1 || items[0].Feedback != "Add two." || items[0].Points != 2 {
		t.Fatalf("question bank read = %#v, %v", items, err)
	}
	if foreign, err := m.ListQuestionBank(ctx, "other-teacher"); err != nil || len(foreign) != 0 {
		t.Fatalf("foreign question bank = %#v, %v", foreign, err)
	}
	if err := m.DeleteQuestionBankItem(ctx, "other-teacher", item.ID); !errors.Is(err, ErrQuestionBankNotFound) {
		t.Fatalf("foreign deletion = %v", err)
	}
	if err := m.DeleteQuestionBankItem(ctx, "teacher", item.ID); err != nil {
		t.Fatal(err)
	}

	resetToken := "raw-reset-token-never-stored"
	digest := sha256.Sum256([]byte(resetToken))
	hash := hex.EncodeToString(digest[:])
	record := model.PasswordResetRecord{PK: "PASSWORD_RESET#" + hash, SK: "TOKEN", UserID: "teacher", ExpiresAt: m.now().Add(time.Hour).Unix(), CreatedAt: iso(m.now())}
	if err := m.CreatePasswordReset(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, found := m.passwordResets["PASSWORD_RESET#"+resetToken]; found {
		t.Fatal("raw reset token was stored")
	}
	if err := m.CompletePasswordReset(ctx, resetToken, "new-hash", m.now().Unix()); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("raw token was accepted by storage: %v", err)
	}
	if err := m.CompletePasswordReset(ctx, hash, "new-hash", m.now().Unix()); err != nil {
		t.Fatal(err)
	}
	user, err := m.GetUser(ctx, "teacher")
	if err != nil || user.PasswordHash != "new-hash" || user.SessionVersion != 1 {
		t.Fatalf("reset password persistence = %#v, %v", user, err)
	}
	if err := m.CompletePasswordReset(ctx, hash, "reused-hash", m.now().Unix()); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("single-use reset token reused: %v", err)
	}
	deleted, err := m.CreateClassroomWithClientID(ctx, &model.Classroom{ID: "class-deleted", OwnerID: "teacher", Name: "Deleted", JoinCode: "DEL12345"}, "deleted-client", "deleted-hash")
	if err != nil || deleted == nil {
		t.Fatalf("create classroom before delete = %#v, %v", deleted, err)
	}
	if err := m.DeleteClassroom(ctx, deleted.ID, "teacher"); err != nil {
		t.Fatal(err)
	}
	if classroom, err := m.CreateClassroomWithClientID(ctx, &model.Classroom{ID: "retry", OwnerID: "teacher", Name: "Deleted", JoinCode: "RETRY123"}, "deleted-client", "deleted-hash"); classroom != nil || !errors.Is(err, ErrClassroomIdempotencyConflict) {
		t.Fatalf("retry after idempotent classroom was deleted = %#v, %v", classroom, err)
	}
}

func TestMemoryQOLMutationsRejectStaleWriters(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	if err := m.CreateUser(ctx, &model.User{UserID: "teacher", Email: "teacher@example.test", Name: "Teacher"}, "old-hash"); err != nil {
		t.Fatal(err)
	}
	classroom := &model.Classroom{ID: "class-fence", OwnerID: "teacher", Name: "Fenced", JoinCode: "FENCE123"}
	if err := m.CreateClassroom(ctx, classroom); err != nil {
		t.Fatal(err)
	}
	invitation := &model.ClassroomInvitation{ID: "invite-fence", ClassroomID: classroom.ID, Name: "Student", Email: "student@example.test", JoinCode: "INVITE34", Status: "pending"}
	if err := m.CreateClassroomInvitation(ctx, "teacher", invitation); err != nil {
		t.Fatal(err)
	}
	initialBank, err := m.SaveQuestionBankItem(ctx, &model.QuestionBankItem{OwnerID: "teacher", ID: "existing", Question: "Q", Options: []string{"a", "b"}, Answer: 0})
	if err != nil {
		t.Fatal(err)
	}
	guard := &memoryFenceStub{active: clusterstate.Writer{Backend: "A", Epoch: 21}}
	m.SetFenceGuard(guard)
	active := clusterstate.WithWriter(ctx, guard.active)
	if err := m.CreatePasswordReset(active, model.PasswordResetRecord{PK: "PASSWORD_RESET#active", SK: "TOKEN", UserID: "teacher", ExpiresAt: m.now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("active reset write = %v", err)
	}
	guard.active = clusterstate.Writer{Backend: "B", Epoch: 22}
	stale := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "A", Epoch: 21})

	checks := []struct {
		name string
		call func() error
	}{
		{"update profile", func() error { _, err := m.UpdateUserName(stale, "teacher", "Stale"); return err }},
		{"change password", func() error { return m.ChangePassword(stale, "teacher", "old-hash", "new-hash") }},
		{"create reset", func() error {
			return m.CreatePasswordReset(stale, model.PasswordResetRecord{PK: "PASSWORD_RESET#stale", SK: "TOKEN", UserID: "teacher", ExpiresAt: m.now().Add(time.Hour).Unix()})
		}},
		{"complete reset", func() error { return m.CompletePasswordReset(stale, "active", "new-hash", m.now().Unix()) }},
		{"create classroom retry", func() error {
			_, err := m.CreateClassroomWithClientID(stale, &model.Classroom{OwnerID: "teacher", Name: "Stale", JoinCode: "STALE123"}, "stale-client", "hash")
			return err
		}},
		{"create invitation", func() error {
			return m.CreateClassroomInvitation(stale, "teacher", &model.ClassroomInvitation{ID: "stale", ClassroomID: classroom.ID, Name: "Student", Email: "student@example.test", JoinCode: "STALE234"})
		}},
		{"delete invitation", func() error { return m.DeleteClassroomInvitation(stale, "teacher", classroom.ID, invitation.ID) }},
		{"leave classroom", func() error { return m.LeaveClassroom(stale, classroom.ID, "student") }},
		{"save question bank item", func() error {
			_, err := m.SaveQuestionBankItem(stale, &model.QuestionBankItem{OwnerID: "teacher", ID: "stale", Question: "Stale", Options: []string{"a", "b"}, Answer: 0})
			return err
		}},
		{"delete question bank item", func() error { return m.DeleteQuestionBankItem(stale, "teacher", initialBank.ID) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, clusterstate.ErrFenced) {
				t.Fatalf("stale mutation = %v, want ErrFenced", err)
			}
		})
	}
	user, _ := m.GetUser(ctx, "teacher")
	if user.Name != "Teacher" || user.PasswordHash != "old-hash" {
		t.Fatalf("stale profile/password write committed: %#v", user)
	}
	if _, found := m.passwordResets["PASSWORD_RESET#stale"]; found {
		t.Fatal("stale reset token was stored")
	}
	if _, found := m.classroomCreateReceipts[memoryClassroomReceiptKey("teacher", "stale-client")]; found {
		t.Fatal("stale classroom retry receipt was stored")
	}
	if _, err := m.FindClassroomInvitationByJoinCode(ctx, invitation.JoinCode); err != nil {
		t.Fatal(err)
	} else if found, _ := m.FindClassroomInvitationByJoinCode(ctx, invitation.JoinCode); found == nil || found.Status != "pending" {
		t.Fatalf("stale invitation delete committed: %#v", found)
	}
	if rows, _ := m.ListQuestionBank(ctx, "teacher"); len(rows) != 1 || rows[0].ID != initialBank.ID {
		t.Fatalf("stale bank mutation committed: %#v", rows)
	}
}

func TestDynamoQOLDurableWritesCarryWriterFence(t *testing.T) {
	cfg := config.Defaults()
	cfg.FencingEnabled = true
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	s.Now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	for _, value := range []any{
		model.User{PK: "USER#teacher", SK: "PROFILE", UserID: "teacher", Email: "teacher@example.test", Name: "Teacher", PasswordHash: "old-hash"},
		model.Classroom{PK: "CLASSROOM#class-1", SK: "META", OwnerID: "teacher", Name: "Science", JoinCode: "CLASS123"},
	} {
		item, err := marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		f.items[itemKey(item)] = item
	}
	ctx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 31})
	if _, err := s.UpdateUserName(ctx, "teacher", "New name"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, "teacher", "old-hash", "new-hash"); err != nil {
		t.Fatal(err)
	}
	resetHash := "digest"
	reset := model.PasswordResetRecord{PK: "PASSWORD_RESET#" + resetHash, SK: "TOKEN", UserID: "teacher", ExpiresAt: s.now().Add(time.Hour).Unix(), CreatedAt: iso(s.now())}
	if err := s.CreatePasswordReset(ctx, reset); err != nil {
		t.Fatal(err)
	}
	if err := s.CompletePasswordReset(ctx, resetHash, "reset-hash", s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveQuestionBankItem(ctx, &model.QuestionBankItem{OwnerID: "teacher", ID: "question-1", Question: "Q", Options: []string{"a", "b"}, Answer: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQuestionBankItem(ctx, "teacher", "question-1"); err != nil {
		t.Fatal(err)
	}
	invitation := &model.ClassroomInvitation{ID: "invite-1", ClassroomID: "class-1", Name: "Student", Email: "student@example.test", JoinCode: "INVITE12", Status: "pending"}
	if err := s.CreateClassroomInvitation(ctx, "teacher", invitation); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteClassroomInvitation(ctx, "teacher", "class-1", "invite-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateClassroomWithClientID(ctx, &model.Classroom{ID: "class-2", OwnerID: "teacher", Name: "History", JoinCode: "CLASS234"}, "client-1", "hash"); err != nil {
		t.Fatal(err)
	}
	if len(f.transactions) != 9 {
		t.Fatalf("quality-of-life durable transactions = %d, want 9", len(f.transactions))
	}
	for index, tx := range f.transactions {
		if len(tx.TransactItems) == 0 {
			t.Fatalf("transaction %d is empty", index)
		}
		fence := tx.TransactItems[len(tx.TransactItems)-1].ConditionCheck
		if fence == nil || avString(fence.Key, "pk") != clusterstate.ActivePK || avString(fence.Key, "sk") != clusterstate.ActiveSK || fence.ExpressionAttributeValues[":backend"].(*types.AttributeValueMemberS).Value != "A" || fence.ExpressionAttributeValues[":epoch"].(*types.AttributeValueMemberN).Value != "31" {
			t.Fatalf("transaction %d is missing atomic writer fence: %#v", index, tx.TransactItems)
		}
	}
}
