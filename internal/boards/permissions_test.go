package boards

import (
	"context"
	"testing"
)

func TestSharedDrawingPermissionIsTeacherControlled(t *testing.T) {
	ctx := context.Background()
	manager := NewManagerWithPersistence(newMemoryBoardStore(), 0)
	teacherSocket, studentSocket := &testSocket{}, &testSocket{}
	teacher := NewSession(manager, "room", teacherSocket, Identity{Name: "Teacher", Role: "teacher"})
	student := NewSession(manager, "room", studentSocket, Identity{Name: "Student", Role: "student"})
	teacher.HandleFrame(ctx, `{"type":"join"}`, 4096)
	student.HandleFrame(ctx, `{"type":"join","role":"teacher"}`, 4096)
	student.HandleFrame(ctx, `{"type":"elements","elements":[{"id":"unauthorized","version":1}]}`, 4096)
	if len(manager.Snapshot("room").Elements) != 0 {
		t.Fatal("view-only student edited board")
	}
	student.HandleFrame(ctx, `{"type":"permissions","students_draw":true}`, 4096)
	if manager.CanDraw("room", studentSocket) {
		t.Fatal("student changed shared permission")
	}
	teacher.HandleFrame(ctx, `{"type":"permissions","students_draw":true}`, 4096)
	if !manager.CanDraw("room", studentSocket) {
		t.Fatal("teacher permission not shared")
	}
	messages := studentSocket.messages()
	if got := messages[len(messages)-1]; got["type"] != "permissions" || got["students_draw"] != true {
		t.Fatalf("no permission broadcast: %#v", got)
	}
	student.HandleFrame(ctx, `{"type":"elements","elements":[{"id":"allowed","version":1}]}`, 4096)
	if len(manager.Snapshot("room").Elements) != 1 {
		t.Fatal("permitted student could not draw")
	}
	teacher.HandleFrame(ctx, `{"type":"permissions","students_draw":false}`, 4096)
	student.HandleFrame(ctx, `{"type":"elements","elements":[{"id":"after-revoke","version":1}]}`, 4096)
	if len(manager.Snapshot("room").Elements) != 1 {
		t.Fatal("permission revocation was ignored")
	}
}

func TestDrawingPermissionSurvivesReconnect(t *testing.T) {
	ctx := context.Background()
	storage := newMemoryBoardStore()
	first := NewManagerWithPersistence(storage, 0)
	if err := first.SetStudentDrawing(ctx, "room", true); err != nil {
		t.Fatal(err)
	}
	second := NewManagerWithPersistence(storage, 0)
	socket := &testSocket{}
	student := NewSession(second, "room", socket, Identity{Role: "student"})
	if _, err := student.HandleFrame(ctx, `{"type":"join"}`, 4096); err != nil {
		t.Fatal(err)
	}
	init := socket.messages()[0]
	if init["room"].(map[string]any)["students_draw"] != true || !second.CanDraw("room", socket) {
		t.Fatal("persisted permission not resumed")
	}
}

func TestRestoreBroadcastReplacesRatherThanMerges(t *testing.T) {
	ctx := context.Background()
	manager := NewManagerWithPersistence(newMemoryBoardStore(), 0)
	socket := &testSocket{}
	manager.Connect("room", socket, "Teacher", "teacher")
	manager.Absorb("room", []map[string]any{{"id": "old", "version": 1}})
	if err := manager.Flush(ctx, "room"); err != nil {
		t.Fatal(err)
	}
	manager.Absorb("room", []map[string]any{{"id": "new", "version": 2}})
	if err := manager.Flush(ctx, "room"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(ctx, "room", 1); err != nil {
		t.Fatal(err)
	}
	messages := socket.messages()
	if got := messages[len(messages)-1]; got["type"] != "restored" || len(got["elements"].([]any)) != 1 {
		t.Fatalf("restore must replace scene: %#v", got)
	}
}
