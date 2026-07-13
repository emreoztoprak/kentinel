package agent

import (
	"testing"
)

func TestParseInsightPlainJSON(t *testing.T) {
	insight, err := parseInsight(`{"status":"warning","summary":"One pod is crash looping.","findings":[{"severity":"warning","resource":"pod app/demo","title":"CrashLoopBackOff","detail":"restarting","recommendation":"check logs"}]}`)
	if err != nil {
		t.Fatalf("parseInsight failed: %v", err)
	}
	if insight.Status != StatusWarning {
		t.Errorf("status = %s, want warning", insight.Status)
	}
	if len(insight.Findings) != 1 || insight.Findings[0].Title != "CrashLoopBackOff" {
		t.Errorf("unexpected findings: %+v", insight.Findings)
	}
}

func TestParseInsightFencedJSON(t *testing.T) {
	text := "Here is my analysis:\n```json\n{\"status\": \"healthy\", \"summary\": \"All good.\", \"findings\": []}\n```\nLet me know if you need more."
	insight, err := parseInsight(text)
	if err != nil {
		t.Fatalf("parseInsight failed on fenced JSON: %v", err)
	}
	if insight.Status != StatusHealthy || insight.Summary != "All good." {
		t.Errorf("unexpected insight: %+v", insight)
	}
}

func TestParseInsightProseWrappedJSON(t *testing.T) {
	text := `Sure! {"status":"critical","summary":"Node down.","findings":[]} — that's the situation.`
	insight, err := parseInsight(text)
	if err != nil {
		t.Fatalf("parseInsight failed on prose-wrapped JSON: %v", err)
	}
	if insight.Status != StatusCritical {
		t.Errorf("status = %s, want critical", insight.Status)
	}
}

func TestParseInsightNestedBraces(t *testing.T) {
	text := `{"status":"warning","summary":"Message with {braces} and \"quotes\".","findings":[]}`
	insight, err := parseInsight(text)
	if err != nil {
		t.Fatalf("parseInsight failed on nested braces: %v", err)
	}
	if insight.Summary != `Message with {braces} and "quotes".` {
		t.Errorf("summary = %q", insight.Summary)
	}
}

func TestParseInsightRejectsGarbage(t *testing.T) {
	for _, text := range []string{
		"no json here at all",
		`{"status":"sideways","summary":"?"}`, // unknown status
		`{"status":"healthy"}`,                // missing summary
	} {
		if _, err := parseInsight(text); err == nil {
			t.Errorf("parseInsight(%q) should fail", text)
		}
	}
}

func TestParseInsightEscalatesStatusToWorstFinding(t *testing.T) {
	// Small models sometimes say "healthy" while listing warnings.
	insight, err := parseInsight(`{"status":"healthy","summary":"Mostly fine.","findings":[
		{"severity":"info","resource":"a","title":"t","detail":"d"},
		{"severity":"critical","resource":"b","title":"t","detail":"d"}]}`)
	if err != nil {
		t.Fatalf("parseInsight failed: %v", err)
	}
	if insight.Status != StatusCritical {
		t.Errorf("status = %s, want critical (escalated from findings)", insight.Status)
	}

	// Info-only findings must not escalate.
	insight, err = parseInsight(`{"status":"healthy","summary":"Fine.","findings":[
		{"severity":"info","resource":"a","title":"t","detail":"d"}]}`)
	if err != nil {
		t.Fatalf("parseInsight failed: %v", err)
	}
	if insight.Status != StatusHealthy {
		t.Errorf("status = %s, want healthy (info findings should not escalate)", insight.Status)
	}

	// Never downgrade: critical status with only warning findings stays critical.
	insight, err = parseInsight(`{"status":"critical","summary":"Bad.","findings":[
		{"severity":"warning","resource":"a","title":"t","detail":"d"}]}`)
	if err != nil {
		t.Fatalf("parseInsight failed: %v", err)
	}
	if insight.Status != StatusCritical {
		t.Errorf("status = %s, want critical (no downgrade)", insight.Status)
	}
}

// Ring-buffer and persistence semantics are covered in store_test.go.
