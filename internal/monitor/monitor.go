package monitor

import (
	"fmt"
	"time"
)

// --- per-control computation ------------------------------------------------

// computeControl derives the computed status for one control from its declared
// status and due dates. This is the "watch over time" logic: declared status is
// a floor, but a control that has passed its next_due is overdue regardless of
// declaration.
func computeControl(c Control, now time.Time) ControlReport {
	report := ControlReport{
		ID:       c.ID,
		Title:    c.Title,
		Owner:    c.Owner,
		Declared: c.Declared,
		NextDue:  c.NextDue,
	}

	// start from declared status as the baseline.
	report.Computed = c.Declared

	// overdue: next_due in the past.
	due, ok := parseTime(c.NextDue)
	if ok && now.After(due) {
		report.Overdue = true
		report.Detail = fmt.Sprintf("next due %s — overdue by %s", c.NextDue, now.Sub(due))
		if rankStatus(report.Computed) < rankStatus(StatusOverdue) {
			report.Computed = StatusOverdue
		}
	}

	// blocked / at-risk declarations are preserved (worst wins).
	switch c.Declared {
	case StatusBlocked:
		report.AtRisk = true
		report.Detail = "control is blocked"
		if rankStatus(report.Computed) < rankStatus(StatusBlocked) {
			report.Computed = StatusBlocked
		}
	}

	return report
}

// rankStatus gives an order for computed status so "worst wins" is trivial.
func rankStatus(s ControlStatus) int {
	switch s {
	case StatusOK:
		return 0
	case StatusAtRisk:
		return 1
	case StatusOverdue:
		return 2
	case StatusBlocked:
		return 3
	default:
		return -1
	}
}

// --- decision queue -----------------------------------------------------------

// computeDecisions ages each decision and flags stale ones. A decision is stale
// if it has been pending longer than the program's cadence.
func computeDecisions(p Program, now time.Time) []DecisionReport {
	var reps []DecisionReport
	cad, _ := parseDur(p.Cadence)
	for _, d := range p.Decisions {
		rep := DecisionReport{
			ID:     d.ID,
			Topic:  d.Topic,
			Status: d.Status,
			Opened: d.Opened,
		}
		if a, ok := ageOf(d.Opened, now); ok {
			rep.Age = fmt.Sprintf("%s", a)
		}
		if d.Status == DecisionPending && cad > 0 {
			if a, ok := ageOf(d.Opened, now); ok && a > cad {
				rep.Stale = true
			}
		}
		reps = append(reps, rep)
	}
	return reps
}

// ageOf returns the elapsed time since opened (ISO8601) to now.
func ageOf(opened string, now time.Time) (time.Duration, bool) {
	t, ok := parseTime(opened)
	if !ok {
		return 0, false
	}
	return now.Sub(t), true
}

// --- cadence ------------------------------------------------------------------

// computeCadence reports whether the program's last checkpoint is within its
// declared cadence of "now". A program with no cadence has nothing to check.
func computeCadence(p Program, now time.Time) CadenceReport {
	rep := CadenceReport{}
	if p.Cadence == "" {
		rep.Enabled = false
		return rep
	}
	rep.Enabled = true
	rep.Cadence = p.Cadence
	rep.LastCheckpoint = p.LastCheckpoint
	cad, ok := parseDur(p.Cadence)
	if !ok || cad <= 0 {
		rep.Stale = true
		rep.BehindBy = "invalid cadence"
		return rep
	}
	last, ok := parseTime(p.LastCheckpoint)
	if !ok {
		rep.Behind = true
		rep.Stale = true
		rep.BehindBy = "unknown (no checkpoint)"
		return rep
	}
	since := now.Sub(last)
	if since > cad {
		rep.Behind = true
		rep.Stale = since > (cad * 2)
		rep.BehindBy = fmt.Sprintf("%s", since-cad)
	} else {
		rep.BehindBy = fmt.Sprintf("(%s ahead)", cad-since)
	}
	return rep
}

// --- top-level Monitor --------------------------------------------------------

// Monitor runs the full monitoring pass: per-control status, decision queue,
// and cadence. It is pure — only the clock (now) is an external input.
func Monitor(p Program, now time.Time) Report {
	controls := make([]ControlReport, len(p.Controls))
	overdue, atRisk, blocked := 0, 0, 0
	for i, c := range p.Controls {
		rep := computeControl(c, now)
		controls[i] = rep
		switch rep.Computed {
		case StatusOverdue:
			overdue++
		case StatusBlocked:
			blocked++
		}
		if rep.AtRisk || rep.Computed == StatusAtRisk {
			atRisk++
		}
	}

	decisions := computeDecisions(p, now)
	pending, blockedDec := 0, 0
	oldestPending := time.Duration(0)
	for _, d := range decisions {
		switch d.Status {
		case DecisionPending:
			pending++
			if a, ok := ageOf(d.Opened, now); ok && a > oldestPending {
				oldestPending = a
			}
		case DecisionBlocked:
			blockedDec++
		}
	}

	cadence := computeCadence(p, now)
	healthy := overdue == 0 && atRisk == 0 && blocked == 0 && blockedDec == 0 && !cadence.Stale

	var verdict Verdict
	if healthy {
		verdict = VerdictHealthy
	} else {
		verdict = VerdictAttention
	}

	report := Report{
		Name:             p.Name,
		Verdict:          verdict,
		ExitCode:         exitCodeFor(verdict),
		Now:              now.Format(time.RFC3339),
		NowTime:          now,
		Controls:         controls,
		Overdue:          overdue,
		AtRisk:           atRisk,
		Blocked:          blocked,
		Decisions:        decisions,
		PendingDecisions: pending,
		BlockedDecisions: blockedDec,
	}
	if oldestPending > 0 {
		report.OldestPendingAge = fmt.Sprintf("%s", oldestPending)
	}
	report.Cadence = cadence
	return report
}

// exitCodeFor maps verdict to the house three-code contract.
func exitCodeFor(v Verdict) int {
	switch v {
	case VerdictHealthy:
		return 0
	default:
		return 1 // Validation
	}
}