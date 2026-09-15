package diagnostics_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/miloszkolber/pixie/internal/diagnostics"
)

func TestSupportSnapshotSanitizesSessionSchemaSummary(t *testing.T) {
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		SessionSchema: &diagnostics.SessionSchemaSummary{
			WrittenByNewerRuntime: true,
			Version:               9,
			UnknownRecords:        3,
			InvalidRecords:        -4,
			RepairedToolCalls:     1_000_001,
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot diagnostics.SupportSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	schema := snapshot.Runtime.SessionSchema
	if schema == nil {
		t.Fatal("session schema summary was dropped")
	}
	if !schema.WrittenByNewerRuntime || schema.Version != 9 || schema.UnknownRecords != 3 {
		t.Fatalf("session schema summary = %#v", schema)
	}
	if schema.InvalidRecords != 0 || schema.RepairedToolCalls != 0 {
		t.Fatalf("unbounded counters were exported: %#v", schema)
	}
}

func TestSupportSnapshotDropsHealthySessionSchema(t *testing.T) {
	payload, err := diagnostics.MarshalSupportSnapshot(diagnostics.SupportSnapshotRuntime{
		SessionSchema: &diagnostics.SessionSchemaSummary{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "sessionSchema") {
		t.Fatalf("healthy session schema was exported: %s", payload)
	}
}
