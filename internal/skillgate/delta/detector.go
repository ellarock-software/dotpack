package delta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// detectorResult is one detector run.
//
// Err is a STRING, not an error, and it is non-empty for every failure
// mode. The caller turns any non-empty Err into a BLOCKED verdict rather
// than an error, because a detector that could not inspect a package is
// a policy outcome -- the package is unapproved -- not a broken machine.
type detectorResult struct {
	Findings []Finding
	Err      string
}

// detectorReport is the subset of the scanner's JSON we consume.
type detectorReport struct {
	Findings []struct {
		RuleID      string `json:"rule_id"`
		Severity    string `json:"severity"`
		Analyzer    string `json:"analyzer"`
		FilePath    string `json:"file_path"`
		LineNumber  *int   `json:"line_number"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"findings"`
}

// detectorRunner is the test seam, so the suite can exercise every
// failure mode without a real Python detector on PATH.
type detectorRunner func(ctx context.Context, bin string, args ...string) error

var runDetector detectorRunner = func(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	// See internal/skillscanner: without WaitDelay a killed process can
	// still block Wait on pipes inherited by its children.
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// scanDetector runs the detector over the WHOLE package, runtime
// directories included -- that is what keeps a payload dropped into a
// hash-excluded logs/ directory visible.
//
// The detector's EXIT CODE IS IGNORED by design. The scanner exits
// non-zero when it finds something, and a finding is not an error here:
// the delta comparison decides what matters. Only the report matters, so
// a missing, unreadable or unparseable report is the failure.
//
// Every failure fails closed. An unscannable package is not an approved
// package.
func scanDetector(ctx context.Context, bin, pkgAbs string, timeout time.Duration) detectorResult {
	reportPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("skillgate-%d-%s.json", os.Getpid(), filepath.Base(pkgAbs)))
	defer func() { _ = os.Remove(reportPath) }()

	args := []string{"scan", pkgAbs, "--use-behavioral", "--format", "json", "--output-json", reportPath}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := runDetector(runCtx, bin, args...)
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return detectorResult{Err: fmt.Sprintf("detector timed out after %s", timeout)}
	case errors.Is(runCtx.Err(), context.Canceled):
		return detectorResult{Err: "detector canceled"}
	case err != nil && errors.Is(err, exec.ErrNotFound):
		return detectorResult{Err: fmt.Sprintf("detector not found: %s", bin)}
	case err != nil && os.IsNotExist(err):
		return detectorResult{Err: fmt.Sprintf("detector not found: %s", bin)}
	}

	raw, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		// No report. Either the binary is missing, or it crashed, or the
		// package is malformed enough that it could not be read. All are
		// the same outcome: nothing was inspected.
		detail := "detector produced no report (unreadable or malformed package)"
		if err != nil {
			detail = fmt.Sprintf("%s: %v", detail, err)
		}
		return detectorResult{Err: detail}
	}

	var report detectorReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return detectorResult{Err: fmt.Sprintf("unparseable detector report: %v", err)}
	}

	findings := make([]Finding, 0, len(report.Findings))
	for _, x := range report.Findings {
		severity := strings.ToUpper(strings.TrimSpace(x.Severity))
		if severity == "" {
			severity = "INFO"
		}
		findings = append(findings, Finding{
			RuleID:   x.RuleID,
			Severity: severity,
			Analyzer: x.Analyzer,
			File:     x.FilePath,
			Line:     x.LineNumber,
			Title:    x.Title,
			Why:      x.Description,
		})
	}
	return detectorResult{Findings: findings}
}
