package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

type classroomCreateReceipt struct {
	PayloadHash string `dynamodbav:"payload_hash"`
	ClassroomID string `dynamodbav:"classroom_id"`
}

type classroomInvitationCodeRef struct {
	PK           string `dynamodbav:"pk"`
	SK           string `dynamodbav:"sk"`
	ClassroomID  string `dynamodbav:"classroom_id"`
	InvitationID string `dynamodbav:"invitation_id"`
}

func (s *Store) UpdateUserName(ctx context.Context, userID, name string) (*model.User, error) {
	userID = strings.TrimSpace(userID)
	out, err := s.fencedUpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.TableName), Key: key("USER#"+userID, "PROFILE"),
		UpdateExpression:          aws.String("SET #name = :name"),
		ConditionExpression:       aws.String("attribute_exists(pk)"),
		ExpressionAttributeNames:  map[string]string{"#name": "name"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":name": &types.AttributeValueMemberS{Value: strings.TrimSpace(name)}},
		ReturnValues:              types.ReturnValueAllNew,
	})
	if err != nil {
		return nil, err
	}
	var user model.User
	if err := unmarshal(out.Attributes, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) ChangePassword(ctx context.Context, userID, expectedHash, newHash string) error {
	err := s.transactWrite(ctx, []types.TransactWriteItem{{Update: &types.Update{
		TableName: aws.String(s.TableName), Key: key("USER#"+strings.TrimSpace(userID), "PROFILE"),
		UpdateExpression:    aws.String("SET password_hash = :new ADD session_version :one"),
		ConditionExpression: aws.String("password_hash = :old"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":new": &types.AttributeValueMemberS{Value: newHash},
			":old": &types.AttributeValueMemberS{Value: expectedHash},
			":one": &types.AttributeValueMemberN{Value: "1"},
		},
	}}})
	if isTransactionConditional(err) {
		return ErrPasswordMismatch
	}
	return err
}

func passwordResetKey(tokenHash string) map[string]types.AttributeValue {
	return key("PASSWORD_RESET#"+strings.TrimSpace(tokenHash), "TOKEN")
}

func (s *Store) CreatePasswordReset(ctx context.Context, record model.PasswordResetRecord) error {
	if strings.TrimSpace(record.UserID) == "" || strings.TrimSpace(strings.TrimPrefix(record.PK, "PASSWORD_RESET#")) == "" || record.ExpiresAt <= s.now().Unix() {
		return fmt.Errorf("invalid password reset record")
	}
	if record.SK == "" {
		record.SK = "TOKEN"
	}
	item, err := marshal(record)
	if err != nil {
		return err
	}
	version := &types.AttributeValueMemberN{Value: fmt.Sprint(record.SessionVersion)}
	versionCondition := "session_version = :version"
	if record.SessionVersion == 0 {
		// Profiles created before session_version was introduced are at version 0.
		versionCondition = "(attribute_not_exists(session_version) OR session_version = :version)"
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{
			TableName: aws.String(s.TableName), Key: key("USER#"+strings.TrimSpace(record.UserID), "PROFILE"),
			ConditionExpression:       aws.String("attribute_exists(pk) AND " + versionCondition),
			ExpressionAttributeValues: map[string]types.AttributeValue{":version": version},
		}},
		{Put: &types.Put{
			TableName: aws.String(s.TableName), Item: item,
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		}},
	})
	if isConditional(err) || isTransactionConditional(err) {
		return ErrPasswordResetInvalid
	}
	return err
}

func (s *Store) CompletePasswordReset(ctx context.Context, tokenHash, passwordHash string, now int64) error {
	item, err := s.getConsistent(ctx, passwordResetKey(tokenHash))
	if err != nil {
		return err
	}
	if item == nil {
		return ErrPasswordResetInvalid
	}
	var reset model.PasswordResetRecord
	if err := unmarshal(item, &reset); err != nil {
		return err
	}
	if reset.UserID == "" || reset.ExpiresAt <= now {
		return ErrPasswordResetInvalid
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Delete: &types.Delete{
			TableName: aws.String(s.TableName), Key: passwordResetKey(tokenHash),
			ConditionExpression:       aws.String("expires_at > :now"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":now": &types.AttributeValueMemberN{Value: fmt.Sprint(now)}},
		}},
		{Update: &types.Update{
			TableName: aws.String(s.TableName), Key: key("USER#"+reset.UserID, "PROFILE"),
			UpdateExpression:    aws.String("SET password_hash = :password ADD session_version :one"),
			ConditionExpression: aws.String("attribute_exists(pk) AND " + passwordResetSessionVersionCondition(reset.SessionVersion)),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":password": &types.AttributeValueMemberS{Value: passwordHash},
				":one":      &types.AttributeValueMemberN{Value: "1"},
				":version":  &types.AttributeValueMemberN{Value: fmt.Sprint(reset.SessionVersion)},
			},
		}},
	})
	if isTransactionConditional(err) {
		return ErrPasswordResetInvalid
	}
	return err
}

func passwordResetSessionVersionCondition(version int64) string {
	if version == 0 {
		return "(attribute_not_exists(session_version) OR session_version = :version)"
	}
	return "session_version = :version"
}

func classroomCreateReceiptKey(ownerID, clientID string) map[string]types.AttributeValue {
	return key("CLASSROOM_CREATE#"+strings.TrimSpace(ownerID), "CLIENT#"+strings.TrimSpace(clientID))
}

// CreateClassroomWithClientID stores the classroom and retry receipt in one
// transaction so a lost POST response can be retried without creating a
// duplicate room.
func (s *Store) CreateClassroomWithClientID(ctx context.Context, classroom *model.Classroom, clientID, payloadHash string) (*model.Classroom, error) {
	if classroom == nil || strings.TrimSpace(clientID) == "" || strings.TrimSpace(payloadHash) == "" {
		return nil, fmt.Errorf("classroom and client_id are required")
	}
	v := *classroom
	prepareClassroom(&v, s.now())
	meta, err := marshal(v)
	if err != nil {
		return nil, err
	}
	codeRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_CODE#" + v.JoinCode, SK: "CLASSROOM", ClassroomID: v.ID})
	if err != nil {
		return nil, err
	}
	ownerRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_OWNER#" + v.OwnerID, SK: "CLASSROOM#" + v.ID, ClassroomID: v.ID})
	if err != nil {
		return nil, err
	}
	receipt, err := marshal(classroomCreateReceipt{PayloadHash: payloadHash, ClassroomID: v.ID})
	if err != nil {
		return nil, err
	}
	receipt["pk"] = &types.AttributeValueMemberS{Value: "CLASSROOM_CREATE#" + v.OwnerID}
	receipt["sk"] = &types.AttributeValueMemberS{Value: "CLIENT#" + strings.TrimSpace(clientID)}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: meta, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: codeRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: ownerRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: receipt, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	})
	if err == nil {
		*classroom = v
		return &v, nil
	}
	if !isTransactionConditional(err) {
		return nil, err
	}
	stored, readErr := s.getConsistent(ctx, classroomCreateReceiptKey(v.OwnerID, clientID))
	if readErr != nil {
		return nil, readErr
	}
	if stored != nil {
		var old classroomCreateReceipt
		if err := unmarshal(stored, &old); err != nil {
			return nil, err
		}
		if old.PayloadHash != payloadHash {
			return nil, ErrClassroomIdempotencyConflict
		}
		classroom, err := s.GetClassroom(ctx, old.ClassroomID)
		if err != nil {
			return nil, err
		}
		if classroom == nil {
			return nil, ErrClassroomIdempotencyConflict
		}
		return classroom, nil
	}
	return nil, ErrClassroomCodeTaken
}

func prepareClassroom(v *model.Classroom, now time.Time) {
	v.ID = strings.TrimSpace(v.ID)
	v.OwnerID = strings.TrimSpace(v.OwnerID)
	v.Name = strings.TrimSpace(v.Name)
	v.JoinCode = normaliseClassroomCode(v.JoinCode)
	if v.ID == "" {
		v.ID = newID()
	}
	if v.PK == "" {
		v.PK = "CLASSROOM#" + v.ID
	}
	if v.SK == "" {
		v.SK = "META"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = iso(now)
	}
	if v.UpdatedAt == "" {
		v.UpdatedAt = v.CreatedAt
	}
	if v.MemberCount < 0 {
		v.MemberCount = 0
	}
}

func classroomInvitationKey(classroomID, invitationID string) map[string]types.AttributeValue {
	return key("CLASSROOM#"+strings.TrimSpace(classroomID), "INVITATION#"+strings.TrimSpace(invitationID))
}

func invitationCodeKey(code string) map[string]types.AttributeValue {
	return key("CLASSROOM_INVITE_CODE#"+normaliseClassroomCode(code), "INVITATION")
}

func (s *Store) CreateClassroomInvitation(ctx context.Context, ownerID string, invitation *model.ClassroomInvitation) error {
	if invitation == nil || strings.TrimSpace(invitation.ClassroomID) == "" {
		return fmt.Errorf("classroom invitation is required")
	}
	classroom, err := s.GetClassroom(ctx, invitation.ClassroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	v := *invitation
	v.ID = strings.TrimSpace(v.ID)
	if v.ID == "" {
		v.ID = "invite-" + newID()
	}
	v.ClassroomID = classroom.ID
	v.Name = strings.TrimSpace(v.Name)
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	v.JoinCode = normaliseClassroomCode(v.JoinCode)
	if v.Status == "" {
		v.Status = "pending"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = iso(s.now())
	}
	v.PK, v.SK = "CLASSROOM#"+classroom.ID, "INVITATION#"+v.ID
	item, err := marshal(v)
	if err != nil {
		return err
	}
	codeRef, err := marshal(classroomInvitationCodeRef{PK: "CLASSROOM_INVITE_CODE#" + v.JoinCode, SK: "INVITATION", ClassroomID: classroom.ID, InvitationID: v.ID})
	if err != nil {
		return err
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{TableName: aws.String(s.TableName), Key: classroomMetaKey(classroom.ID), ConditionExpression: aws.String("owner_id = :owner AND attribute_not_exists(deleting)"), ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: strings.TrimSpace(ownerID)}}}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: codeRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
	})
	if isTransactionConditional(err) {
		return ErrClassroomCodeTaken
	}
	if err == nil {
		*invitation = v
	}
	return err
}

func (s *Store) ListClassroomInvitations(ctx context.Context, ownerID, classroomID string) ([]model.ClassroomInvitation, error) {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return nil, err
	}
	if classroom == nil {
		return nil, ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return nil, ErrClassroomForbidden
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :invitation)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "CLASSROOM#" + classroom.ID}, ":invitation": &types.AttributeValueMemberS{Value: "INVITATION#"}}})
	if err != nil {
		return nil, err
	}
	rows := make([]model.ClassroomInvitation, 0, len(items))
	for _, item := range items {
		var invitation model.ClassroomInvitation
		if err := unmarshal(item, &invitation); err != nil {
			return nil, err
		}
		invitation.ID = strings.TrimPrefix(invitation.SK, "INVITATION#")
		invitation.ClassroomID = classroom.ID
		rows = append(rows, invitation)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt < rows[j].CreatedAt })
	return rows, nil
}

func (s *Store) FindClassroomInvitationByJoinCode(ctx context.Context, joinCode string) (*model.ClassroomInvitation, error) {
	refItem, err := s.get(ctx, invitationCodeKey(joinCode))
	if err != nil || refItem == nil {
		return nil, err
	}
	var ref classroomInvitationCodeRef
	if err := unmarshal(refItem, &ref); err != nil {
		return nil, err
	}
	classroom, err := s.GetClassroom(ctx, ref.ClassroomID)
	if err != nil || classroom == nil {
		return nil, err
	}
	item, err := s.get(ctx, classroomInvitationKey(ref.ClassroomID, ref.InvitationID))
	if err != nil || item == nil {
		return nil, err
	}
	var invitation model.ClassroomInvitation
	if err := unmarshal(item, &invitation); err != nil {
		return nil, err
	}
	invitation.ID, invitation.ClassroomID = ref.InvitationID, ref.ClassroomID
	return &invitation, nil
}

func (s *Store) DeleteClassroomInvitation(ctx context.Context, ownerID, classroomID, invitationID string) error {
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return ErrClassroomForbidden
	}
	item, err := s.get(ctx, classroomInvitationKey(classroom.ID, invitationID))
	if err != nil {
		return err
	}
	if item == nil {
		return ErrNotFound
	}
	var invitation model.ClassroomInvitation
	if err := unmarshal(item, &invitation); err != nil {
		return err
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: classroomInvitationKey(classroom.ID, invitationID), ConditionExpression: aws.String("attribute_exists(pk)")}},
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: invitationCodeKey(invitation.JoinCode)}},
	})
	if isTransactionConditional(err) {
		return ErrNotFound
	}
	return err
}

func (s *Store) AcceptClassroomInvitation(ctx context.Context, classroomID, invitationID string, member *model.ClassroomMember) error {
	if member == nil {
		return fmt.Errorf("classroom member is required")
	}
	invitationItem, err := s.getConsistent(ctx, classroomInvitationKey(classroomID, invitationID))
	if err != nil {
		return err
	}
	if invitationItem == nil {
		return ErrClassroomInvitation
	}
	var invitation model.ClassroomInvitation
	if err := unmarshal(invitationItem, &invitation); err != nil {
		return err
	}
	if invitation.Status != "pending" {
		return ErrClassroomInvitationUsed
	}
	classroom, err := s.GetClassroom(ctx, classroomID)
	if err != nil {
		return err
	}
	if classroom == nil {
		return ErrNotFound
	}
	v := *member
	v.ClassroomID, v.PK, v.SK = classroom.ID, "CLASSROOM#"+classroom.ID, "MEMBER#"+strings.TrimSpace(v.UserID)
	if v.Role == "" {
		v.Role = "student"
	}
	if v.JoinedAt == "" {
		v.JoinedAt = iso(s.now())
	}
	memberItem, err := marshal(v)
	if err != nil {
		return err
	}
	memberRef, err := marshal(classroomIndexItem{PK: "CLASSROOM_MEMBER#" + v.UserID, SK: "CLASSROOM#" + classroom.ID, ClassroomID: classroom.ID})
	if err != nil {
		return err
	}
	now := iso(s.now())
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: memberItem, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Put: &types.Put{TableName: aws.String(s.TableName), Item: memberRef, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
		{Update: &types.Update{TableName: aws.String(s.TableName), Key: classroomMetaKey(classroom.ID), UpdateExpression: aws.String("SET #member_count = if_not_exists(#member_count, :zero) + :one, #updated_at = :now"), ConditionExpression: aws.String("attribute_exists(pk) AND attribute_not_exists(deleting)"), ExpressionAttributeNames: map[string]string{"#member_count": "member_count", "#updated_at": "updated_at"}, ExpressionAttributeValues: map[string]types.AttributeValue{":zero": &types.AttributeValueMemberN{Value: "0"}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":now": &types.AttributeValueMemberS{Value: now}}}},
		{Update: &types.Update{TableName: aws.String(s.TableName), Key: classroomInvitationKey(classroom.ID, invitationID), UpdateExpression: aws.String("SET #status = :accepted, accepted_by = :user, accepted_at = :now"), ConditionExpression: aws.String("#status = :pending AND email = :email"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":accepted": &types.AttributeValueMemberS{Value: "accepted"}, ":pending": &types.AttributeValueMemberS{Value: "pending"}, ":email": &types.AttributeValueMemberS{Value: strings.ToLower(strings.TrimSpace(v.Email))}, ":user": &types.AttributeValueMemberS{Value: v.UserID}, ":now": &types.AttributeValueMemberS{Value: now}}}},
	})
	if isTransactionConditional(err) {
		return ErrClassroomMember
	}
	if err == nil {
		*member = v
	}
	return err
}

func (s *Store) LeaveClassroom(ctx context.Context, classroomID, userID string) error {
	classroomID, userID = strings.TrimSpace(classroomID), strings.TrimSpace(userID)
	memberKey := classroomMemberKey(classroomID, userID)
	item, err := s.get(ctx, memberKey)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrClassroomMemberNotFound
	}
	err = s.transactWrite(ctx, []types.TransactWriteItem{
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: memberKey, ConditionExpression: aws.String("attribute_exists(pk)")}},
		{Delete: &types.Delete{TableName: aws.String(s.TableName), Key: classroomMemberIndexKey(userID, classroomID)}},
		{Update: &types.Update{TableName: aws.String(s.TableName), Key: classroomMetaKey(classroomID), UpdateExpression: aws.String("SET #member_count = if_not_exists(#member_count, :zero) - :one, #updated_at = :now"), ConditionExpression: aws.String("attribute_exists(pk) AND attribute_not_exists(deleting)"), ExpressionAttributeNames: map[string]string{"#member_count": "member_count", "#updated_at": "updated_at"}, ExpressionAttributeValues: map[string]types.AttributeValue{":zero": &types.AttributeValueMemberN{Value: "0"}, ":one": &types.AttributeValueMemberN{Value: "1"}, ":now": &types.AttributeValueMemberS{Value: iso(s.now())}}}},
	})
	if isTransactionConditional(err) {
		return ErrClassroomMemberNotFound
	}
	return err
}

func (s *Store) ListQuestionBank(ctx context.Context, ownerID string) ([]model.QuestionBankItem, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{TableName: aws.String(s.TableName), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :item)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "QUESTION_BANK#" + strings.TrimSpace(ownerID)}, ":item": &types.AttributeValueMemberS{Value: "ITEM#"}}})
	if err != nil {
		return nil, err
	}
	rows := make([]model.QuestionBankItem, 0, len(items))
	for _, item := range items {
		var row model.QuestionBankItem
		if err := unmarshal(item, &row); err != nil {
			return nil, err
		}
		row.ID = strings.TrimPrefix(row.SK, "ITEM#")
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SavedAt == rows[j].SavedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].SavedAt > rows[j].SavedAt
	})
	return rows, nil
}

func (s *Store) SaveQuestionBankItem(ctx context.Context, row *model.QuestionBankItem) (*model.QuestionBankItem, error) {
	if row == nil || strings.TrimSpace(row.OwnerID) == "" || strings.TrimSpace(row.Question) == "" || len(row.Options) < 2 || row.Answer < 0 || row.Answer >= len(row.Options) {
		return nil, fmt.Errorf("invalid question bank item")
	}
	v := *row
	if v.ID == "" {
		v.ID = "question-" + newID()
	}
	v.PK, v.SK = "QUESTION_BANK#"+strings.TrimSpace(v.OwnerID), "ITEM#"+strings.TrimSpace(v.ID)
	if v.SavedAt == "" {
		v.SavedAt = iso(s.now())
	}
	v.Question = strings.TrimSpace(v.Question)
	v.Options = append([]string(nil), v.Options...)
	item, err := marshal(v)
	if err != nil {
		return nil, err
	}
	err = s.fencedPutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk)")})
	if isConditional(err) || isTransactionConditional(err) {
		return nil, ErrQuizConflict
	}
	if err != nil {
		return nil, err
	}
	*row = v
	return &v, nil
}

func (s *Store) DeleteQuestionBankItem(ctx context.Context, ownerID, id string) error {
	err := s.transactWrite(ctx, []types.TransactWriteItem{{Delete: &types.Delete{
		TableName: aws.String(s.TableName), Key: key("QUESTION_BANK#"+strings.TrimSpace(ownerID), "ITEM#"+strings.TrimSpace(id)),
		ConditionExpression: aws.String("attribute_exists(pk)"),
	}}})
	if isTransactionConditional(err) {
		return ErrQuestionBankNotFound
	}
	return err
}

func (m *MemoryStore) UpdateUserName(ctx context.Context, userID, name string) (*model.User, error) {
	return withMemoryFence(m, ctx, func() (*model.User, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		user := m.users[strings.TrimSpace(userID)]
		if user == nil {
			return nil, ErrNotFound
		}
		user.Name = strings.TrimSpace(name)
		value := *user
		return &value, nil
	})
}

func (m *MemoryStore) ChangePassword(ctx context.Context, userID, expectedHash, newHash string) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		user := m.users[strings.TrimSpace(userID)]
		if user == nil || user.PasswordHash != expectedHash {
			return ErrPasswordMismatch
		}
		user.PasswordHash = newHash
		user.SessionVersion++
		return nil
	})
}

func (m *MemoryStore) CreatePasswordReset(ctx context.Context, record model.PasswordResetRecord) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		if strings.TrimSpace(record.UserID) == "" || strings.TrimSpace(strings.TrimPrefix(record.PK, "PASSWORD_RESET#")) == "" || record.ExpiresAt <= m.now().Unix() {
			return fmt.Errorf("invalid password reset record")
		}
		user := m.users[strings.TrimSpace(record.UserID)]
		if user == nil || user.SessionVersion != record.SessionVersion {
			return ErrPasswordResetInvalid
		}
		if m.passwordResets == nil {
			m.passwordResets = map[string]model.PasswordResetRecord{}
		}
		if _, exists := m.passwordResets[record.PK]; exists {
			return ErrPasswordResetInvalid
		}
		m.passwordResets[record.PK] = record
		return nil
	})
}

func (m *MemoryStore) CompletePasswordReset(ctx context.Context, tokenHash, passwordHash string, now int64) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		key := "PASSWORD_RESET#" + strings.TrimSpace(tokenHash)
		record, exists := m.passwordResets[key]
		if !exists || record.ExpiresAt <= now {
			return ErrPasswordResetInvalid
		}
		user := m.users[record.UserID]
		if user == nil || user.SessionVersion != record.SessionVersion {
			return ErrPasswordResetInvalid
		}
		user.PasswordHash = passwordHash
		user.SessionVersion++
		delete(m.passwordResets, key)
		return nil
	})
}

func memoryClassroomReceiptKey(ownerID, clientID string) string {
	return strings.TrimSpace(ownerID) + "\x00" + strings.TrimSpace(clientID)
}

func (m *MemoryStore) CreateClassroomWithClientID(ctx context.Context, classroom *model.Classroom, clientID, payloadHash string) (*model.Classroom, error) {
	if classroom == nil || strings.TrimSpace(clientID) == "" || strings.TrimSpace(payloadHash) == "" {
		return nil, fmt.Errorf("classroom and client_id are required")
	}
	return withMemoryFence(m, ctx, func() (*model.Classroom, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		ownerID := strings.TrimSpace(classroom.OwnerID)
		receiptKey := memoryClassroomReceiptKey(ownerID, clientID)
		if receipt, exists := m.classroomCreateReceipts[receiptKey]; exists {
			if receipt.PayloadHash != payloadHash {
				return nil, ErrClassroomIdempotencyConflict
			}
			classroom := m.classrooms[receipt.ClassroomID]
			if classroom == nil {
				return nil, ErrClassroomIdempotencyConflict
			}
			return cloneClassroom(classroom), nil
		}
		v := *classroom
		prepareClassroom(&v, m.now())
		if m.classrooms == nil {
			m.classrooms = map[string]*model.Classroom{}
		}
		if m.classroomCodes == nil {
			m.classroomCodes = map[string]string{}
		}
		if _, exists := m.classrooms[v.ID]; exists {
			return nil, ErrClassroomCodeTaken
		}
		if v.JoinCode == "" {
			return nil, fmt.Errorf("classroom join code is required")
		}
		if _, exists := m.classroomCodes[v.JoinCode]; exists {
			return nil, ErrClassroomCodeTaken
		}
		m.classrooms[v.ID] = cloneClassroom(&v)
		m.classroomCodes[v.JoinCode] = v.ID
		if m.classroomMembers == nil {
			m.classroomMembers = map[string]map[string]*model.ClassroomMember{}
		}
		if m.classroomMembers[v.ID] == nil {
			m.classroomMembers[v.ID] = map[string]*model.ClassroomMember{}
		}
		if m.classroomCreateReceipts == nil {
			m.classroomCreateReceipts = map[string]classroomCreateReceipt{}
		}
		m.classroomCreateReceipts[receiptKey] = classroomCreateReceipt{PayloadHash: payloadHash, ClassroomID: v.ID}
		*classroom = v
		return cloneClassroom(&v), nil
	})
}

func memoryInvitationKey(classroomID, invitationID string) string {
	return strings.TrimSpace(classroomID) + "\x00" + strings.TrimSpace(invitationID)
}

func (m *MemoryStore) CreateClassroomInvitation(ctx context.Context, ownerID string, invitation *model.ClassroomInvitation) error {
	if invitation == nil {
		return fmt.Errorf("classroom invitation is required")
	}
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		classroom := m.classrooms[strings.TrimSpace(invitation.ClassroomID)]
		if classroom == nil {
			return ErrNotFound
		}
		if classroom.OwnerID != strings.TrimSpace(ownerID) {
			return ErrClassroomForbidden
		}
		v := *invitation
		v.ID = strings.TrimSpace(v.ID)
		if v.ID == "" {
			v.ID = "invite-" + newID()
		}
		v.ClassroomID, v.Name = classroom.ID, strings.TrimSpace(v.Name)
		v.Email = strings.ToLower(strings.TrimSpace(v.Email))
		v.JoinCode = normaliseClassroomCode(v.JoinCode)
		if v.Status == "" {
			v.Status = "pending"
		}
		if v.CreatedAt == "" {
			v.CreatedAt = iso(m.now())
		}
		if v.JoinCode == "" {
			return fmt.Errorf("invitation join code is required")
		}
		if _, exists := m.classroomInvitationCodes[v.JoinCode]; exists {
			return ErrClassroomCodeTaken
		}
		key := memoryInvitationKey(classroom.ID, v.ID)
		if _, exists := m.classroomInvitations[key]; exists {
			return ErrClassroomCodeTaken
		}
		v.PK, v.SK = "CLASSROOM#"+classroom.ID, "INVITATION#"+v.ID
		if m.classroomInvitations == nil {
			m.classroomInvitations = map[string]*model.ClassroomInvitation{}
		}
		if m.classroomInvitationCodes == nil {
			m.classroomInvitationCodes = map[string]string{}
		}
		m.classroomInvitations[key] = cloneClassroomInvitation(&v)
		m.classroomInvitationCodes[v.JoinCode] = key
		*invitation = v
		return nil
	})
}

func cloneClassroomInvitation(invitation *model.ClassroomInvitation) *model.ClassroomInvitation {
	if invitation == nil {
		return nil
	}
	v := *invitation
	return &v
}

func (m *MemoryStore) ListClassroomInvitations(_ context.Context, ownerID, classroomID string) ([]model.ClassroomInvitation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	classroom := m.classrooms[strings.TrimSpace(classroomID)]
	if classroom == nil {
		return nil, ErrNotFound
	}
	if classroom.OwnerID != strings.TrimSpace(ownerID) {
		return nil, ErrClassroomForbidden
	}
	rows := make([]model.ClassroomInvitation, 0)
	for key, invitation := range m.classroomInvitations {
		if strings.HasPrefix(key, classroom.ID+"\x00") {
			rows = append(rows, *cloneClassroomInvitation(invitation))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].CreatedAt < rows[j].CreatedAt })
	return rows, nil
}

func (m *MemoryStore) FindClassroomInvitationByJoinCode(_ context.Context, joinCode string) (*model.ClassroomInvitation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := m.classroomInvitationCodes[normaliseClassroomCode(joinCode)]
	return cloneClassroomInvitation(m.classroomInvitations[key]), nil
}

func (m *MemoryStore) DeleteClassroomInvitation(ctx context.Context, ownerID, classroomID, invitationID string) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		classroom := m.classrooms[strings.TrimSpace(classroomID)]
		if classroom == nil {
			return ErrNotFound
		}
		if classroom.OwnerID != strings.TrimSpace(ownerID) {
			return ErrClassroomForbidden
		}
		key := memoryInvitationKey(classroom.ID, invitationID)
		invitation := m.classroomInvitations[key]
		if invitation == nil {
			return ErrNotFound
		}
		delete(m.classroomInvitations, key)
		delete(m.classroomInvitationCodes, normaliseClassroomCode(invitation.JoinCode))
		return nil
	})
}

func (m *MemoryStore) AcceptClassroomInvitation(ctx context.Context, classroomID, invitationID string, member *model.ClassroomMember) error {
	if member == nil {
		return fmt.Errorf("classroom member is required")
	}
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		classroom := m.classrooms[strings.TrimSpace(classroomID)]
		if classroom == nil {
			return ErrNotFound
		}
		key := memoryInvitationKey(classroom.ID, invitationID)
		invitation := m.classroomInvitations[key]
		if invitation == nil {
			return ErrClassroomInvitation
		}
		if invitation.Status != "pending" {
			return ErrClassroomInvitationUsed
		}
		if !strings.EqualFold(invitation.Email, strings.TrimSpace(member.Email)) {
			return ErrClassroomInvitation
		}
		userID := strings.TrimSpace(member.UserID)
		if userID == "" {
			return fmt.Errorf("classroom member user id is required")
		}
		if m.classroomMembers[classroom.ID] == nil {
			m.classroomMembers[classroom.ID] = map[string]*model.ClassroomMember{}
		}
		if m.classroomMembers[classroom.ID][userID] != nil {
			return ErrClassroomMember
		}
		v := *member
		v.ClassroomID, v.UserID = classroom.ID, userID
		v.PK, v.SK = "CLASSROOM#"+classroom.ID, "MEMBER#"+userID
		if v.Role == "" {
			v.Role = "student"
		}
		if v.JoinedAt == "" {
			v.JoinedAt = iso(m.now())
		}
		m.classroomMembers[classroom.ID][userID] = cloneClassroomMember(&v)
		classroom.MemberCount++
		classroom.UpdatedAt = iso(m.now())
		invitation.Status, invitation.AcceptedBy, invitation.AcceptedAt = "accepted", userID, iso(m.now())
		*member = v
		return nil
	})
}

func (m *MemoryStore) LeaveClassroom(ctx context.Context, classroomID, userID string) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		classroom := m.classrooms[strings.TrimSpace(classroomID)]
		if classroom == nil {
			return ErrNotFound
		}
		userID = strings.TrimSpace(userID)
		members := m.classroomMembers[classroom.ID]
		if members == nil || members[userID] == nil {
			return ErrClassroomMemberNotFound
		}
		delete(members, userID)
		if classroom.MemberCount > 0 {
			classroom.MemberCount--
		}
		classroom.UpdatedAt = iso(m.now())
		return nil
	})
}

func (m *MemoryStore) ListQuestionBank(_ context.Context, ownerID string) ([]model.QuestionBankItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]model.QuestionBankItem, 0, len(m.questionBank[strings.TrimSpace(ownerID)]))
	for _, item := range m.questionBank[strings.TrimSpace(ownerID)] {
		v := *item
		v.Options = append([]string(nil), item.Options...)
		rows = append(rows, v)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SavedAt == rows[j].SavedAt {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].SavedAt > rows[j].SavedAt
	})
	return rows, nil
}

func (m *MemoryStore) SaveQuestionBankItem(ctx context.Context, item *model.QuestionBankItem) (*model.QuestionBankItem, error) {
	if item == nil || strings.TrimSpace(item.OwnerID) == "" || strings.TrimSpace(item.Question) == "" || len(item.Options) < 2 || item.Answer < 0 || item.Answer >= len(item.Options) {
		return nil, fmt.Errorf("invalid question bank item")
	}
	return withMemoryFence(m, ctx, func() (*model.QuestionBankItem, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		v := *item
		v.OwnerID, v.Question = strings.TrimSpace(v.OwnerID), strings.TrimSpace(v.Question)
		v.ID = strings.TrimSpace(v.ID)
		if v.ID == "" {
			v.ID = "question-" + newID()
		}
		if v.SavedAt == "" {
			v.SavedAt = iso(m.now())
		}
		v.PK, v.SK = "QUESTION_BANK#"+v.OwnerID, "ITEM#"+v.ID
		v.Options = append([]string(nil), v.Options...)
		if m.questionBank == nil {
			m.questionBank = map[string]map[string]*model.QuestionBankItem{}
		}
		if m.questionBank[v.OwnerID] == nil {
			m.questionBank[v.OwnerID] = map[string]*model.QuestionBankItem{}
		}
		if _, exists := m.questionBank[v.OwnerID][v.ID]; exists {
			return nil, ErrQuizConflict
		}
		m.questionBank[v.OwnerID][v.ID] = &v
		*item = v
		return &v, nil
	})
}

func (m *MemoryStore) DeleteQuestionBankItem(ctx context.Context, ownerID, id string) error {
	return withMemoryFenceError(m, ctx, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()
		ownerID, id = strings.TrimSpace(ownerID), strings.TrimSpace(id)
		if m.questionBank[ownerID] == nil || m.questionBank[ownerID][id] == nil {
			return ErrQuestionBankNotFound
		}
		delete(m.questionBank[ownerID], id)
		return nil
	})
}
