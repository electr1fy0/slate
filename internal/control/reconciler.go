package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Reconciler struct {
	Config  ReconcilerConfig
	Store   ReconcilerStore
	Metrics MetricsPublisher
	Now     func() time.Time
	Region  func() string
	Log     io.Writer
}

func NewReconciler(cfg ReconcilerConfig, store ReconcilerStore, metrics MetricsPublisher) *Reconciler {
	return &Reconciler{
		Config:  cfg,
		Store:   store,
		Metrics: metrics,
		Now:     time.Now,
		Region:  func() string { return os.Getenv("AWS_REGION") },
		Log:     os.Stdout,
	}
}

type ReconcilerSummary struct {
	Scanned           int              `json:"scanned"`
	Repaired          int              `json:"repaired"`
	AlreadyReplicated int              `json:"already_replicated"`
	Conflicts         []map[string]any `json:"conflicts"`
	RepairedIDs       []string         `json:"repaired_ids"`
}

type journalEntry struct {
	TargetPK   string
	TargetSK   string
	Version    int
	RequestID  string
	CreatedAt  int64
	Attributes map[string]types.AttributeValue
}

// Handle replays journal entries with a conditional version write. A
// conditional conflict is reported in the successful summary; scan, read,
// non-conditional write, and metric failures are returned to Lambda so the
// async invocation's DLQ receives them.
func (r *Reconciler) Handle(ctx context.Context, _ map[string]any) (ReconcilerSummary, error) {
	cutoff := r.Now().Add(-time.Duration(r.Config.LookbackMinutes) * time.Minute).UnixMilli()
	entries := make([]map[string]types.AttributeValue, 0)
	var startKey map[string]types.AttributeValue
	for {
		page, err := r.Store.Scan(ctx, startKey, cutoff)
		if err != nil {
			return ReconcilerSummary{}, err
		}
		entries = append(entries, page.Items...)
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		startKey = page.LastEvaluatedKey
	}

	summary := ReconcilerSummary{Conflicts: []map[string]any{}, RepairedIDs: []string{}}
	summary.Scanned = len(entries)
	for _, attrs := range entries {
		entry, err := decodeJournalEntry(attrs)
		if err != nil {
			return ReconcilerSummary{}, err
		}
		key := map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: entry.TargetPK},
			"sk": &types.AttributeValueMemberS{Value: entry.TargetSK},
		}
		current, err := r.Store.GetItem(ctx, key)
		if err != nil {
			return ReconcilerSummary{}, err
		}
		if requestID, ok := attributeString(current["request_id"]); ok && requestID == entry.RequestID {
			summary.AlreadyReplicated++
			continue
		}
		currentVersion := 0
		if value, ok := attributeInt(current["version"]); ok {
			currentVersion = value
		} else if current["version"] != nil {
			return ReconcilerSummary{}, fmt.Errorf("invalid current version for %s", entry.RequestID)
		}
		if currentVersion >= entry.Version {
			summary.Conflicts = append(summary.Conflicts, map[string]any{
				"request_id":      entry.RequestID,
				"reason":          "newer version present",
				"journal_version": entry.Version,
				"current_version": currentVersion,
			})
			continue
		}

		reconstructed := map[string]types.AttributeValue{
			"pk":                key["pk"],
			"sk":                key["sk"],
			"version":           &types.AttributeValueMemberN{Value: strconv.Itoa(entry.Version)},
			"request_id":        &types.AttributeValueMemberS{Value: entry.RequestID},
			"updated_at":        &types.AttributeValueMemberN{Value: strconv.FormatInt(entry.CreatedAt, 10)},
			"reconciled":        &types.AttributeValueMemberBOOL{Value: true},
			"written_by_region": &types.AttributeValueMemberS{Value: r.Region()},
		}
		for _, field := range []string{"answers", "student_name", "score", "total"} {
			if value, ok := entry.Attributes[field]; ok {
				reconstructed[field] = value
			}
		}
		if err := r.Store.PutReconciled(ctx, reconstructed, entry.Version); err != nil {
			if errors.Is(err, ErrConditionalCheckFailed) {
				summary.Conflicts = append(summary.Conflicts, map[string]any{
					"request_id": entry.RequestID,
					"reason":     "lost race",
				})
				continue
			}
			return ReconcilerSummary{}, err
		}
		summary.Repaired++
		summary.RepairedIDs = append(summary.RepairedIDs, entry.RequestID)
	}

	if r.Metrics != nil {
		metrics := []Metric{
			{Name: "ReconcilerRepaired", Value: float64(summary.Repaired)},
			{Name: "ReconcilerScanned", Value: float64(summary.Scanned)},
		}
		var err error
		if batch, ok := r.Metrics.(BatchMetricsPublisher); ok {
			err = batch.PutBatch(ctx, r.Config.MetricNamespace, metrics)
		} else {
			for _, metric := range metrics {
				if err = r.Metrics.Put(ctx, r.Config.MetricNamespace, metric); err != nil {
					break
				}
			}
		}
		if err != nil {
			return ReconcilerSummary{}, err
		}
	}
	r.printJSON(summary)
	return summary, nil
}

func decodeJournalEntry(attrs map[string]types.AttributeValue) (journalEntry, error) {
	targetPK, ok := attributeString(attrs["target_pk"])
	if !ok {
		return journalEntry{}, fmt.Errorf("journal entry missing target_pk")
	}
	targetSK, ok := attributeString(attrs["target_sk"])
	if !ok {
		return journalEntry{}, fmt.Errorf("journal entry missing target_sk")
	}
	requestID, ok := attributeString(attrs["request_id"])
	if !ok {
		return journalEntry{}, fmt.Errorf("journal entry missing request_id")
	}
	version, ok := attributeInt(attrs["version"])
	if !ok {
		return journalEntry{}, fmt.Errorf("journal entry missing or invalid version")
	}
	createdAt, ok := attributeInt64(attrs["created_at"])
	if !ok {
		return journalEntry{}, fmt.Errorf("journal entry missing or invalid created_at")
	}
	return journalEntry{TargetPK: targetPK, TargetSK: targetSK, Version: version, RequestID: requestID, CreatedAt: createdAt, Attributes: attrs}, nil
}

func attributeString(value types.AttributeValue) (string, bool) {
	result, ok := value.(*types.AttributeValueMemberS)
	if !ok || result == nil {
		return "", false
	}
	return result.Value, true
}

func attributeInt(value types.AttributeValue) (int, bool) {
	n, ok := attributeNumberString(value)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.Atoi(n)
	return parsed, err == nil
}

func attributeInt64(value types.AttributeValue) (int64, bool) {
	n, ok := attributeNumberString(value)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(n, 10, 64)
	return parsed, err == nil
}

func attributeNumberString(value types.AttributeValue) (string, bool) {
	n, ok := value.(*types.AttributeValueMemberN)
	if !ok || n == nil {
		return "", false
	}
	return n.Value, true
}

func (r *Reconciler) printJSON(value any) {
	if r.Log == nil {
		return
	}
	data, err := json.Marshal(value)
	if err == nil {
		_, _ = r.Log.Write(append(data, '\n'))
	}
}
