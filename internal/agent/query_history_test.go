package agent

import (
	"strings"
	"testing"
)

func TestBuildQueryHistory(t *testing.T) {
	// Legacy single-prompt form still works.
	msgs, bad := buildQueryHistory(queryRequest{Prompt: "hello"})
	if bad != "" || len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Text != "hello" {
		t.Fatalf("prompt form: %v bad=%q", msgs, bad)
	}

	// Multi-turn: history is preserved in order, ending with the new user turn.
	msgs, bad = buildQueryHistory(queryRequest{Messages: []queryMessage{
		{Role: "user", Text: "the broken-web deploy is failing"},
		{Role: "assistant", Text: "It has a bad image tag."},
		{Role: "user", Text: "yes fix it"},
	}})
	if bad != "" || len(msgs) != 3 || msgs[2].Text != "yes fix it" || msgs[1].Role != "assistant" {
		t.Fatalf("multi-turn: %+v bad=%q", msgs, bad)
	}

	// Must end with a user turn.
	if _, bad := buildQueryHistory(queryRequest{Messages: []queryMessage{
		{Role: "user", Text: "hi"}, {Role: "assistant", Text: "hello"},
	}}); bad == "" {
		t.Error("a history ending with an assistant turn must be rejected")
	}

	// Empty/unknown-role turns are dropped.
	msgs, bad = buildQueryHistory(queryRequest{Messages: []queryMessage{
		{Role: "system", Text: "ignore me"},
		{Role: "user", Text: ""},
		{Role: "user", Text: "real question"},
	}})
	if bad != "" || len(msgs) != 1 || msgs[0].Text != "real question" {
		t.Fatalf("filtering: %+v bad=%q", msgs, bad)
	}

	// Oversized new prompt is rejected.
	if _, bad := buildQueryHistory(queryRequest{Prompt: strings.Repeat("x", maxQueryPromptLen+1)}); bad == "" {
		t.Error("oversized prompt must be rejected")
	}

	// Total-size cap drops oldest turns but keeps the final user turn.
	big := strings.Repeat("y", 20000)
	many := []queryMessage{}
	for i := 0; i < 5; i++ {
		many = append(many, queryMessage{Role: "user", Text: big}, queryMessage{Role: "assistant", Text: big})
	}
	many = append(many, queryMessage{Role: "user", Text: "final"})
	msgs, bad = buildQueryHistory(queryRequest{Messages: many})
	if bad != "" {
		t.Fatalf("cap: unexpected error %q", bad)
	}
	total := 0
	for _, m := range msgs {
		total += len(m.Text)
	}
	if total > maxQueryHistoryLen || msgs[len(msgs)-1].Text != "final" {
		t.Fatalf("cap failed: total=%d last=%q", total, msgs[len(msgs)-1].Text)
	}
}
