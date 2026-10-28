package model

import (
	"encoding/json"
	"testing"
)

func TestAuthoringJSONKeepsZeroAndLearnerRedaction(t *testing.T) {
	for _, index := range []int{0, 1} {
		question := Question{Question: "Choose", Options: []string{"A", "B"}, Answer: index}
		data, err := json.Marshal(QuizDraft{Questions: []Question{question}})
		if err != nil {
			t.Fatal(err)
		}
		var draft struct {
			Questions []map[string]any `json:"questions"`
		}
		if err := json.Unmarshal(data, &draft); err != nil {
			t.Fatal(err)
		}
		if draft.Questions[0]["answer"] != float64(index) {
			t.Fatalf("answer disappeared: %s", data)
		}
		question.AnswerHidden = true
		data, err = json.Marshal(question)
		if err != nil {
			t.Fatal(err)
		}
		var learner map[string]any
		_ = json.Unmarshal(data, &learner)
		if _, exists := learner["answer"]; exists {
			t.Fatalf("answer leaked: %s", data)
		}
	}
	data, _ := json.Marshal(ExamMeta{AttemptStatus: "submitted", Score: 0, Total: 2})
	var meta map[string]any
	_ = json.Unmarshal(data, &meta)
	if meta["score"] != float64(0) {
		t.Fatalf("zero result disappeared: %s", data)
	}
}
