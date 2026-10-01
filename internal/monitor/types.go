package monitor

import (
	"strconv"
	"strings"
	"time"
)

// --- Program state: what a monitored program declares about itself --------

// ControlStatus is the declared/observed state of a monitored control.
type ControlStatus string

const (
	StatusOK      ControlStatus = "ok"
	StatusAtRisk  ControlStatus = "at_risk"
	StatusOverdue ControlStatus = "overdue"
	StatusBlocked ControlStatus = "blocked"
)

// Control is one monitored control.
type Control struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Owner      string        `json:"owner,omitempty"`
	Declared   ControlStatus `json:"status"` // declared status; may be overridden by due logic
	NextDue    string        `json:"next_due,omitempty"` // ISO8601
	LastActive string        `json:"last_active,omitempty"` // ISO8601
	Category   string        `json:"category,omitempty"`
}

// DecisionStatus is the state of a pending decision.
type DecisionStatus string

const (
	DecisionPending DecisionStatus = "pending"
	DecisionDecided DecisionStatus = "decided"
	DecisionBlocked DecisionStatus = "blocked"
)

// Decision is an item on the program's decision queue.
type Decision struct {
	ID         string             `json:"id"`
	Topic      string             `json:"topic"`
	AssignedTo string             `json:"assigned_to,omitempty"`
	Opened     string             `json:"opened,omitempty"` // ISO8601
	Status     DecisionStatus     `json:"status"`
	Reason     string             `json:"reason,omitempty"`
}

// Program is the monitored program's current state snapshot.
type Program struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug,omitempty"`
	Cadence     string  `json:"cadence,omitempty"`  // e.g. "7d", "24h"
	LastCheckpoint string `json:"last_checkpoint,omitempty"` // ISO8601; cadence measured from this
	Controls    []Control `json:"controls"`
	Decisions   []Decision `json:"decisions"`
}

// parseDur parses a duration string like "7d", "3w", "72h" into a
// time.Duration. Returns the value and whether it parsed. Go's
// time.ParseDuration has no day (d) or week (w) unit, so this expands those
// to hours before delegating to it for the rest.
func parseDur(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	if strings.HasSuffix(lower, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	}
	if strings.HasSuffix(lower, "w") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil || n <= 0 {
			return 0, false
		}
		return time.Duration(n) * 7 * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// parseTime parses an ISO8601 timestamp into a local-naive time.Time.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// --- Report: the monitoring verdict ---------------------------------------

// ProgramVerdict is the overall monitoring outcome. The house contract has
// only three exit codes (0 OK / 1 Validation / 2 ToolError); state verdicts
// map to 0/1, and 2 is reserved for bad input in main.
type Verdict string

const (
	VerdictHealthy   Verdict = "healthy"   // exit 0 — on cadence, nothing overdue/blocked
	VerdictAttention Verdict = "attention" // exit 1 — overdue, at-risk, blocked, or stale cadence
)

// ControlReport is the computed status for a single control.
type ControlReport struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Owner    string        `json:"owner,omitempty"`
	Declared ControlStatus `json:"declared"`
	Computed ControlStatus `json:"computed"`
	Overdue  bool          `json:"overdue,omitempty"`
	AtRisk   bool          `json:"at_risk,omitempty"`
	NextDue  string        `json:"next_due,omitempty"`
	Detail   string        `json:"detail,omitempty"`
}

// DecisionReport summarizes a decision on the queue.
type DecisionReport struct {
	ID       string            `json:"id"`
	Topic    string            `json:"topic"`
	Status   DecisionStatus    `json:"status"`
	Opened   string            `json:"opened,omitempty"`
	Age      string            `json:"age,omitempty"`
	Stale    bool              `json:"stale,omitempty"`
}

// Report is the full monitoring output. Byte-stable for identical input
// (Program, now): derived purely from state + clock, no randomness.
type Report struct {
	Name        string  `json:"name"`
	Verdict     Verdict `json:"verdict"`
	ExitCode    int     `json:"exit_code"`
	Now         string  `json:"now"`
	NowTime     time.Time
	Controls    []ControlReport `json:"controls"`
	Overdue     int             `json:"overdue"`
	AtRisk      int             `json:"at_risk"`
	Blocked     int             `json:"blocked"`
	Decisions   []DecisionReport `json:"decisions"`
	PendingDecisions int        `json:"pending_decisions"`
	BlockedDecisions int        `json:"blocked_decisions"`
	OldestPendingAge string    `json:"oldest_pending_age,omitempty"`
	Cadence     CadenceReport  `json:"cadence"`
}

// CadenceReport describes whether the program is on its monitoring cadence.
type CadenceReport struct {
	Enabled        bool     `json:"enabled"`
	Cadence        string   `json:"cadence,omitempty"`
	LastCheckpoint string   `json:"last_checkpoint,omitempty"`
	NextCheckpoint string   `json:"next_checkpoint,omitempty"`
	BehindBy       string   `json:"behind_by,omitempty"`
	Behind         bool     `json:"behind"`
	Stale          bool     `json:"stale"` // behind by more than one cadence
}