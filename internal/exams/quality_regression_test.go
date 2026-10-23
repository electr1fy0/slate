package exams

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ayush/slate/internal/model"
)

func TestQualityRejectAttemptInputsBeforeStorage(t *testing.T) {
	service := New(nil)
	user := &model.User{UserID: "student", Role: "student"}
	for _, id := range []string{strings.Repeat("x", 257), "request\nlog", "request\x00key"} {
		if _, err := service.Submit(context.Background(), "exam", user, map[string]int{}, id); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("submission ID: %v", err)
		}
		if _, err := service.SubmitSaved(context.Background(), "exam", user, id); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("saved submission ID: %v", err)
		}
	}
	if _, err := service.Get(context.Background(), "exam", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("missing user")
	}
	if _, err := service.Save(context.Background(), "exam", user, map[string]int{}, -1, "request"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("negative version")
	}
	if _, err := service.Save(context.Background(), "exam", user, map[string]int{}, 0, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("missing request ID")
	}
}
