package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"time"
)

// nowAt parses a fixed clock from "YYYY-MM-DD" (local, naive).
func nowAt(t *testing.T, s string) time.Time {
	n, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad clock %q: %v", s, err)
	}
	return n
}

// nowAtUTC parses a fixed clock from full RFC3339.
func nowAtUTC(t *testing.T, s string) time.Time {
	n, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad clock %q: %v", s, err)
	}
	return n
}

// moduleRoot walks up to the nearest go.mod.
func moduleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func sampleProgram() Program {
	return Program{
		Name:           "iso27001",
		Slug:           "iso27001-program",
		Cadence:        "7d",
		LastCheckpoint: "2026-09-15T12:00:00Z",
		Controls: []Control{
			{ID: "A.8.2", Title: "Access control", Owner: "sec", Declared: StatusOK, NextDue: "2026-09-30T00:00:00Z"},
			{ID: "A.5.9", Title: "Secure config", Owner: "plat", Declared: StatusAtRisk, NextDue: "2026-09-10T00:00:00Z"},
			{ID: "A.7.2", Title: "DLP", Owner: "dpo", Declared: StatusOK, NextDue: "2026-09-01T00:00:00Z"},
		},
		Decisions: []Decision{
			{ID: "D-01", Topic: "Zero-trust", AssignedTo: "sec", Opened: "2026-09-14T00:00:00Z", Status: DecisionPending},
			{ID: "D-03", Topic: "Migrate EU", AssignedTo: "plat", Opened: "2026-09-01T00:00:00Z", Status: DecisionBlocked},
		},
	}
}

func TestMonitor_Healthy(t *testing.T) {
	p := Program{
		Name:           "ok",
		Cadence:        "7d",
		LastCheckpoint: "2026-09-25T00:00:00Z",
		Controls:       []Control{{ID: "A.8.2", Title: "x", Declared: StatusOK, NextDue: "2026-12-01T00:00:00Z"}},
		Decisions:      []Decision{{ID: "D-01", Topic: "y", Opened: "2026-09-24T00:00:00Z", Status: DecisionPending}},
	}
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	if r.Verdict != VerdictHealthy {
		t.Errorf("verdict=%q exit=%d; want healthy/0", r.Verdict, r.ExitCode)
	}
	if r.ExitCode != 0 {
		t.Errorf("exit=%d; want 0", r.ExitCode)
	}
}

func TestMonitor_Attention_Overdue(t *testing.T) {
	p := Program{
		Cadence:        "7d",
		LastCheckpoint: "2026-09-25T00:00:00Z",
		Controls:       []Control{{ID: "A.7.2", Title: "DLP", Declared: StatusOK, NextDue: "2026-09-01T00:00:00Z"}},
	}
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	if r.Verdict != VerdictAttention {
		t.Errorf("verdict=%q; want attention", r.Verdict)
	}
	if r.Overdue != 1 {
		t.Errorf("overdue=%d; want 1", r.Overdue)
	}
	if r.ExitCode != 1 {
		t.Errorf("exit=%d; want 1", r.ExitCode)
	}
}

func TestMonitor_Attention_Blocked(t *testing.T) {
	p := Program{
		Controls: []Control{{ID: "B.1", Title: "Blocked ctrl", Declared: StatusBlocked, NextDue: "2026-12-01T00:00:00Z"}},
		Decisions: []Decision{{ID: "D-03", Topic: "Migrate EU", Opened: "2026-09-01T00:00:00Z", Status: DecisionBlocked}},
	}
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	if r.Verdict != VerdictAttention {
		t.Errorf("verdict=%q; want attention", r.Verdict)
	}
	if r.Blocked != 1 {
		t.Errorf("blocked controls=%d; want 1", r.Blocked)
	}
	if r.BlockedDecisions != 1 {
		t.Errorf("blocked decisions=%d; want 1", r.BlockedDecisions)
	}
}

func TestMonitor_CadenceStale(t *testing.T) {
	p := Program{
		Cadence:        "1d",
		LastCheckpoint: "2026-09-01T00:00:00Z",
		Controls:       []Control{{ID: "A.8.2", Title: "x", Declared: StatusOK, NextDue: "2026-12-01T00:00:00Z"}},
	}
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	if !r.Cadence.Stale {
		t.Errorf("cadence.stale=%v; want true (24 days behind, cadence 1d)", r.Cadence.Stale)
	}
	if r.Verdict != VerdictAttention {
		t.Errorf("verdict=%q; want attention (stale cadence)", r.Verdict)
	}
}

func TestMonitor_DecisionStaleness(t *testing.T) {
	p := Program{
		Cadence:        "7d",
		LastCheckpoint: "2026-09-25T00:00:00Z",
		Controls:       []Control{{ID: "A.8.2", Title: "x", Declared: StatusOK, NextDue: "2026-12-01T00:00:00Z"}},
		Decisions:      []Decision{{ID: "D-01", Topic: "old pending", Opened: "2026-09-01T00:00:00Z", Status: DecisionPending}},
	}
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	if len(r.Decisions) == 0 {
		t.Fatal("no decisions in report")
	}
	if !r.Decisions[0].Stale {
		t.Errorf("decision D-01 stale=%v; want true (24d pending > 7d cadence)", r.Decisions[0].Stale)
	}
}

func TestMonitor_Determinism(t *testing.T) {
	p := sampleProgram()
	a := Monitor(p, nowAtUTC(t, "2026-09-25T12:00:00Z"))
	b := Monitor(p, nowAtUTC(t, "2026-09-25T12:00:00Z"))
	if !bytes.Equal(marshal(a), marshal(b)) {
		t.Fatalf("determinism violated:\nA:\n%s\nB:\n%s", marshal(a), marshal(b))
	}
}

func marshal(r Report) []byte {
	b, _ := json.Marshal(r)
	return b
}

func loadProgram(t *testing.T) Program {
	p := Program{}
	data, _ := os.ReadFile(filepath.Join(moduleRoot(), "data/sample/program.json"))
	_ = json.Unmarshal(data, &p)
	return p
}

func TestMonitor_SampleProgram(t *testing.T) {
	p := loadProgram(t)
	now := nowAtUTC(t, "2026-09-25T12:00:00Z")
	r := Monitor(p, now)
	_ = fmt.Sprint(r.Verdict)
	if r.Overdue == 0 {
		t.Errorf("expected A.7.2 (due 09/01) to be overdue at 09/25")
	}
	if r.AtRisk == 0 {
		t.Errorf("expected at_risk count >=1 (A.5.9 declared at_risk)")
	}
}