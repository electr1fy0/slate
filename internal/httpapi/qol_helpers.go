package httpapi

import (
	"errors"
	"net/http"

	"github.com/ayush/slate/internal/store"
)

var (
	errClassroomAssignmentTeacherRequired = errors.New("classroom assignment requires an owning teacher")
	errClassroomAssignmentNotFound        = errors.New("classroom not found")
	errClassroomAssignmentForbidden       = errors.New("classroom ownership required")
)

func effectiveQuestionPoints(points int) int {
	if points <= 0 {
		return 1
	}
	return points
}

func (s *Server) writeExamAssignmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errClassroomAssignmentTeacherRequired), errors.Is(err, errClassroomAssignmentForbidden):
		detail(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errClassroomAssignmentNotFound), errors.Is(err, store.ErrNotFound):
		detail(w, http.StatusNotFound, "classroom not found")
	default:
		detail(w, http.StatusServiceUnavailable, "classroom assignment could not be checked")
	}
}
