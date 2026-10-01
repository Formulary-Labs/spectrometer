// spectrometer watches a program over time: control status, cadence, and the
// decision queue. It re-runs the monitoring pass on every checkpoint and
// reports a verdict. Read-only: no program file is ever written; output goes
// to --output or stdout. Exit codes follow the Formulary three-code contract:
// 0 = healthy, 1 = attention (overdue/at-risk/blocked/stale), 2 = tool error.
//
// Deterministic: identical --program + identical --now gives identical output.
// --now is the only external clock input.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Formulary-Labs/spectrometer/internal/monitor"
	"github.com/Formulary-Labs/substrate/exit"
	"github.com/Formulary-Labs/substrate/provenance"
)

const version = "0.1.0"

func main() {
	var (
		programFlag = flag.String("program", "", "Program JSON snapshot (required)")
		nowFlag     = flag.String("now", "", "Clock (ISO8601); default now")
		fmtFlag     = flag.String("format", "json", "Output format: json (default), md")
		outputFlag  = flag.String("output", "", "Output file path (stdout if empty)")
		quietFlag   = flag.Bool("quiet", false, "Suppress progress output")
		versionFlag = flag.Bool("version", false, "Print version and exit")
		doctorFlag  = flag.Bool("doctor", false, "Self-check and exit 0")
		programSlug = flag.String("program-slug", "", "Program slug for provenance logging")
	)
	flag.Usage = usage
	flag.Parse()

	if *versionFlag {
		fmt.Printf("spectrometer version %s\n", version)
		os.Exit(exit.OK)
	}
	if *doctorFlag {
		runDoctor()
		return
	}
	if *programFlag == "" {
		fmt.Fprintf(os.Stderr, `{"error": "--program is required", "code": 2}\n`)
		flag.Usage()
		os.Exit(exit.ToolError)
	}

	if !*quietFlag {
		fmt.Fprintln(os.Stderr, "[spectrometer] loading program:", *programFlag)
	}

	data, err := os.ReadFile(*programFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, `{"error": "%s", "code": 2}\n`, err.Error())
		os.Exit(exit.ToolError)
	}

	var program monitor.Program
	if err := json.Unmarshal(data, &program); err != nil {
		fmt.Fprintf(os.Stderr, `{"error": "parsing program: %v", "code": 2}\n`, err)
		os.Exit(exit.ToolError)
	}

	var now time.Time
	if *nowFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, `{"error": "bad --now: %q", "code": 2}\n`, *nowFlag)
			os.Exit(exit.Validation)
		}
		now = parsed
	} else {
		now = time.Now().UTC()
	}

	if !*quietFlag {
		fmt.Fprintf(os.Stderr, "[spectrometer] now: %s\n", now.Format(time.RFC3339))
	}

	report := monitor.Monitor(program, now)
	if !*quietFlag {
		fmt.Fprintf(os.Stderr, "[spectrometer] verdict %s (exit %d): %d overdue, %d at-risk, %d blocked, %d decisions (stale cadence=%v)\n",
			report.Verdict, report.ExitCode, report.Overdue, report.AtRisk, report.Blocked, len(report.Decisions), report.Cadence.Stale)
	}

	out := formatOutput(report, *fmtFlag)
	if *outputFlag != "" {
		if err := os.WriteFile(*outputFlag, []byte(out), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "error writing output: %v\n", err)
			os.Exit(exit.ToolError)
		}
		if !*quietFlag {
			fmt.Fprintf(os.Stderr, "wrote %s\n", *outputFlag)
		}
	} else {
		fmt.Print(out)
	}

	if *programSlug != "" {
		_ = provenance.Write("logs/provenance.jsonl", provenance.Entry{
			Spec:        "functions/program-monitoring-spec.md",
			Output:      *programFlag,
			OutputType:  "program_monitoring",
			Program:     *programSlug,
			Purpose:     fmt.Sprintf("spectrometer: verdict %s exit %d (overdue %d, at-risk %d, blocked %d, decisions %d)",
				report.Verdict, report.ExitCode, report.Overdue, report.AtRisk, report.Blocked, len(report.Decisions)),
			Reusability: provenance.Instance,
			QualityGate: provenance.Pass,
			Tool:        "spectrometer",
			ToolVersion: version,
		})
	}

	os.Exit(report.ExitCode)
}

func runDoctor() {
	fmt.Printf("spectrometer version %s\n", version)
	fmt.Println("substrate: required (github.com/Formulary-Labs/substrate v0.4.0)")
	fmt.Println("doctor: OK")
}

// formatOutput renders the report as JSON (default, parseable by jq) or MD.
func formatOutput(r monitor.Report, fmtStr string) string {
	if fmtStr == "md" {
		return toMarkdown(r)
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

// toMarkdown renders a human-readable monitoring report.
func toMarkdown(r monitor.Report) string {
	var b strings.Builder
	b.WriteString("## spectrometer — program monitoring\n\n")
	b.WriteString(fmt.Sprintf("**Program:** %s  |  **Verdict:** %s  |  **Now:** %s\n\n",
		r.Name, r.Verdict, r.Now))
	b.WriteString(fmt.Sprintf("**Counts:** %d overdue | %d at-risk | %d blocked | %d decisions (pending %d, blocked %d)\n\n",
		r.Overdue, r.AtRisk, r.Blocked, len(r.Decisions), r.PendingDecisions, r.BlockedDecisions))

	if r.Cadence.Enabled {
		b.WriteString("## Cadence\n\n")
		b.WriteString(fmt.Sprintf("Interval %s — last checkpoint %s. %s\n\n",
			r.Cadence.Cadence, r.Cadence.LastCheckpoint, cadenceStatus(r.Cadence)))
	}

	if len(r.Controls) > 0 {
		b.WriteString("## Controls\n\n")
		b.WriteString(fmt.Sprintf("| ID | Title | Declared | Computed | Due | Owner |\n") +
			fmt.Sprintf("|---|---|---|---|---|---|\n"))
		for _, c := range r.Controls {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				c.ID, c.Title, c.Declared, c.Computed, c.NextDue, c.Owner))
		}
		b.WriteString("\n")
	}

	if len(r.Decisions) > 0 {
		b.WriteString("## Decision Queue\n\n")
		for _, d := range r.Decisions {
			star := ""
			if d.Stale {
				star = " ⚠stale"
			}
			b.WriteString(fmt.Sprintf("- %s — %s (%s, age %s)%s\n",
				d.ID, d.Topic, d.Status, d.Age, star))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func cadenceStatus(c monitor.CadenceReport) string {
	if c.Stale {
		return "⚠ stale — more than one cadence behind"
	}
	if c.Behind {
		return "behind by " + c.BehindBy
	}
	return "on cadence"
}

func usage() {
	fmt.Fprintln(os.Stderr, `spectrometer — program monitoring (the watch)

Usage:
  spectrometer [flags] --program <program.json>

Flags:
  --program string      Program JSON snapshot (required)
  --now string          Clock (ISO8601); default now
  --format string       Output format: json (default), md
  --output string       Output file path (stdout if empty)
  --program-slug string Program slug for provenance logging
  --quiet               Suppress progress output
  --doctor              Self-check and exit 0
  --version             Print version and exit

Program JSON format:
  {
    "name": "iso27001",
    "cadence": "7d",
    "last_checkpoint": "2026-09-20T12:00:00Z",
    "controls": [
      { "id": "A.8.2", "title": "Access control", "owner": "sec-team",
        "status": "ok", "next_due": "2026-09-25T00:00:00Z", "category": "access" }
    ],
    "decisions": [
      { "id": "D-01", "topic": "Adopt zero-trust network", "status": "pending",
        "opened": "2026-09-01T00:00:00Z" }
    ]
  }

Exit codes: 0 healthy, 1 attention, 2 tool error.
Deterministic: identical --program + --now gives identical output.

Examples:
  spectrometer --program data/sample/program.json --now 2026-09-20T12:00:00Z
  spectrometer --program data/sample/program.json --format md
  spectrometer --doctor`)
}