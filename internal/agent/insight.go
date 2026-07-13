// Package agent implements the AI agent service: a periodic cluster review
// loop that produces structured insights, and an on-demand query engine that
// answers questions using read-only Kubernetes tools.
package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Status is the overall cluster verdict of one review.
type Status string

const (
	StatusHealthy  Status = "healthy"
	StatusWarning  Status = "warning"
	StatusCritical Status = "critical"
	// StatusError means the review itself failed (LLM or cluster unreachable).
	StatusError Status = "error"
)

// Insight is the result of one periodic cluster review.
type Insight struct {
	Status      Status    `json:"status"`
	Summary     string    `json:"summary"`
	Findings    []Finding `json:"findings"`
	CreatedAt   time.Time `json:"createdAt"`
	DurationMs  int64     `json:"durationMs"`
	Provider    string    `json:"provider"`
	Model       string    `json:"model"`
	ReviewError string    `json:"reviewError,omitempty"`
}

// Finding is one problem (or notable observation) in the cluster.
type Finding struct {
	Severity       string `json:"severity"` // "info" | "warning" | "critical"
	Resource       string `json:"resource"` // e.g. "pod app/demo-app-abc123"
	Title          string `json:"title"`
	Detail         string `json:"detail"`
	Recommendation string `json:"recommendation,omitempty"`
}

// parseInsight extracts the structured review from an LLM response. Models
// sometimes wrap JSON in prose or code fences, so parsing is lenient: it
// takes the first top-level JSON object found in the text.
func parseInsight(text string) (*Insight, error) {
	jsonText := extractJSONObject(text)
	if jsonText == "" {
		return nil, fmt.Errorf("no JSON object found in model response")
	}

	var parsed struct {
		Status   string    `json:"status"`
		Summary  string    `json:"summary"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		return nil, fmt.Errorf("model response is not valid insight JSON: %w", err)
	}

	status := Status(strings.ToLower(parsed.Status))
	switch status {
	case StatusHealthy, StatusWarning, StatusCritical:
	default:
		return nil, fmt.Errorf("model returned unknown status %q", parsed.Status)
	}
	if parsed.Summary == "" {
		return nil, fmt.Errorf("model response is missing a summary")
	}

	// Smaller models sometimes report status "healthy" while listing warning
	// or critical findings. The findings are the ground truth — escalate the
	// overall status to match the worst finding severity.
	status = escalate(status, parsed.Findings)

	return &Insight{Status: status, Summary: parsed.Summary, Findings: parsed.Findings}, nil
}

// escalate returns the more severe of the reported status and the worst
// finding severity. It never downgrades a status.
func escalate(status Status, findings []Finding) Status {
	rank := map[Status]int{StatusHealthy: 0, StatusWarning: 1, StatusCritical: 2}
	worst := status
	for _, f := range findings {
		s := Status(strings.ToLower(f.Severity))
		if s != StatusWarning && s != StatusCritical {
			continue // "info" and anything unknown never escalate
		}
		if rank[s] > rank[worst] {
			worst = s
		}
	}
	return worst
}

var codeFence = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// extractJSONObject finds the first plausible top-level JSON object.
func extractJSONObject(text string) string {
	if m := codeFence.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	start := strings.Index(text, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			if inString {
				escaped = true
			}
		case '"':
			inString = !inString
		case '{':
			if !inString {
				depth++
			}
		case '}':
			if !inString {
				depth--
				if depth == 0 {
					return text[start : i+1]
				}
			}
		}
	}
	return ""
}

// The insight store (in-memory ring + optional SQLite persistence) lives in
// store.go.
