package continuum

import (
	"encoding/json"
	"os"
	"testing"
)

type aslDefinition struct {
	StartAt        string                     `json:"StartAt"`
	TimeoutSeconds int                        `json:"TimeoutSeconds"`
	States         map[string]json.RawMessage `json:"States"`
}

func TestFailoverStateMachineHasCompleteTransitionsAndPageAggregation(t *testing.T) {
	data, err := os.ReadFile("../../statemachine/demo-failover.asl.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition aslDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		t.Fatal(err)
	}
	if _, ok := definition.States[definition.StartAt]; !ok {
		t.Fatalf("StartAt %q does not exist", definition.StartAt)
	}
	if definition.TimeoutSeconds != 840 {
		t.Fatalf("workflow timeout=%d, want 840s to remain inside the 900s incident lease", definition.TimeoutSeconds)
	}
	for name, raw := range definition.States {
		var state map[string]json.RawMessage
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatalf("decode state %s: %v", name, err)
		}
		for _, rawTarget := range stateTargets(state) {
			var target string
			if err := json.Unmarshal(rawTarget, &target); err != nil {
				t.Fatalf("decode target from %s: %v", name, err)
			}
			if _, ok := definition.States[target]; !ok {
				t.Errorf("state %s targets missing state %s", name, target)
			}
		}
	}
	for _, required := range []string{"AcquireLock", "VerifyIncident", "Promote", "PublicSmoke", "Rollback", "ReconcileFirstPage", "ReconcileNextPage", "AccumulateReconciliationPage", "ReconciliationConflictCheck", "RecordReconciliationCompleted", "RecordReconciliationFailed", "ReleaseOnError", "ReleaseSuccess"} {
		if _, ok := definition.States[required]; !ok {
			t.Errorf("required workflow state %s is missing", required)
		}
	}
	if !jsonContainsString(data, "States.MathAdd($.summary.conflict_count, States.ArrayLength($.reconciliation.Payload.conflicts))") || !jsonContainsString(data, "$.reconciliation.Payload.cursor") {
		t.Fatal("workflow no longer aggregates conflict counts or loops over the reconciler cursor")
	}
	if jsonContainsString(data, "States.ArrayAppend") {
		t.Fatal("workflow uses an unsupported ArrayAppend intrinsic")
	}
	var lockState map[string]any
	if err := json.Unmarshal(definition.States["LockAcquired"], &lockState); err != nil {
		t.Fatal(err)
	}
	choices, _ := lockState["Choices"].([]any)
	reconcileBranch := false
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		if choice["Next"] != "InitializeReconciliation" {
			continue
		}
		terms, _ := choice["And"].([]any)
		if len(terms) != 2 {
			continue
		}
		var hasLock, hasOperation bool
		for _, rawTerm := range terms {
			term, _ := rawTerm.(map[string]any)
			if term["Variable"] == "$.lock.lock_acquired" && term["BooleanEquals"] == true {
				hasLock = true
			}
			if term["Variable"] == "$.operation" && term["StringEquals"] == "reconcile" {
				hasOperation = true
			}
		}
		reconcileBranch = hasLock && hasOperation
	}
	if !reconcileBranch {
		t.Fatal("manual reconciliation must require an acquired lock and bypass route verification")
	}
	var keptPromoted map[string]json.RawMessage
	if err := json.Unmarshal(definition.States["KeptPromotedRoute"], &keptPromoted); err != nil {
		t.Fatal(err)
	}
	var terminalType string
	if err := json.Unmarshal(keptPromoted["Type"], &terminalType); err != nil || terminalType != "Fail" {
		t.Fatalf("unverified promoted route terminal type=%q err=%v, want Fail", terminalType, err)
	}
}

func stateTargets(state map[string]json.RawMessage) []json.RawMessage {
	targets := make([]json.RawMessage, 0)
	if raw := state["Next"]; len(raw) > 0 {
		targets = append(targets, raw)
	}
	if raw := state["Choices"]; len(raw) > 0 {
		var choices []map[string]json.RawMessage
		_ = json.Unmarshal(raw, &choices)
		for _, choice := range choices {
			if next := choice["Next"]; len(next) > 0 {
				targets = append(targets, next)
			}
		}
	}
	if raw := state["Catch"]; len(raw) > 0 {
		var catches []map[string]json.RawMessage
		_ = json.Unmarshal(raw, &catches)
		for _, entry := range catches {
			if next := entry["Next"]; len(next) > 0 {
				targets = append(targets, next)
			}
		}
	}
	return targets
}

func jsonContainsString(data []byte, needle string) bool {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	return containsJSONString(value, needle)
}

func containsJSONString(value any, needle string) bool {
	switch item := value.(type) {
	case string:
		return item == needle
	case []any:
		for _, child := range item {
			if containsJSONString(child, needle) {
				return true
			}
		}
	case map[string]any:
		for _, child := range item {
			if containsJSONString(child, needle) {
				return true
			}
		}
	}
	return false
}
