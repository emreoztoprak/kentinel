package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	dynfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/llm"
)

// scriptedProvider returns canned responses in order and records every
// request, so tests can assert what the loop sent back to the "model".
type scriptedProvider struct {
	responses []*llm.ChatResponse
	err       error
	requests  []llm.ChatRequest
}

func (p *scriptedProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return nil, p.err
	}
	i := len(p.requests) - 1
	if i >= len(p.responses) {
		i = len(p.responses) - 1 // repeat the last response (for cap tests)
	}
	return p.responses[i], nil
}

func (p *scriptedProvider) Name() string  { return "scripted" }
func (p *scriptedProvider) Model() string { return "scripted-1" }

func newQueryEngine(t *testing.T, provider llm.Provider) *QueryEngine {
	t.Helper()
	client := &k8s.Client{
		Clientset: kfake.NewSimpleClientset(),
		Dynamic:   dynfake.NewSimpleDynamicClient(clientscheme.Scheme),
	}
	runtime := &Runtime{
		settings: Settings{Provider: "scripted", Model: "scripted-1", ReviewInterval: time.Minute},
		provider: provider,
		changed:  make(chan struct{}, 1),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return NewQueryEngine(client, runtime, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func runQuery(engine *QueryEngine, prompt string) []QueryEvent {
	var events []QueryEvent
	engine.Run(context.Background(), []llm.Message{{Role: "user", Text: prompt}}, func(ev QueryEvent) { events = append(events, ev) })
	return events
}

func eventTypes(events []QueryEvent) string {
	types := make([]string, 0, len(events))
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	return strings.Join(types, ",")
}

func TestQueryToolLoopHappyPath(t *testing.T) {
	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "get_cluster_overview", Input: json.RawMessage(`{}`)}}},
		{Text: "All clear — no problems found."},
	}}
	engine := newQueryEngine(t, provider)

	events := runQuery(engine, "how is the cluster?")
	if got := eventTypes(events); got != "tool,text,done" {
		t.Fatalf("event sequence = %s, want tool,text,done — events: %+v", got, events)
	}

	// The tool must have actually executed: the second request's tool result
	// carries the overview JSON, not an error.
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(provider.requests))
	}
	last := provider.requests[1].Messages
	result := last[len(last)-1].ToolResults[0]
	if result.IsError || !strings.Contains(result.Content, "\"nodes\"") {
		t.Errorf("tool result = %+v", result)
	}
}

func TestQueryFeedsToolErrorsBackToModel(t *testing.T) {
	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "list_resources", Input: json.RawMessage(`{"kind":"widgets"}`)}}},
		{Text: "That kind does not exist."},
	}}
	engine := newQueryEngine(t, provider)

	events := runQuery(engine, "list widgets")
	if got := eventTypes(events); got != "tool,text,done" {
		t.Fatalf("event sequence = %s (a tool error must not abort the query)", got)
	}

	last := provider.requests[1].Messages
	result := last[len(last)-1].ToolResults[0]
	if !result.IsError || !strings.Contains(result.Content, "unsupported resource kind") {
		t.Errorf("expected error tool result fed back to the model, got %+v", result)
	}
}

func TestQueryStopsAtIterationCap(t *testing.T) {
	// The provider asks for a tool on every call, forever.
	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{{ID: "c", Name: "get_cluster_overview", Input: json.RawMessage(`{}`)}}},
	}}
	engine := newQueryEngine(t, provider)

	events := runQuery(engine, "loop forever")
	final := events[len(events)-1]
	if final.Type != "error" || !strings.Contains(final.Content, fmt.Sprint(maxQueryIterations)) {
		t.Fatalf("final event = %+v, want iteration-cap error", final)
	}
	if len(provider.requests) != maxQueryIterations {
		t.Errorf("LLM calls = %d, want exactly %d", len(provider.requests), maxQueryIterations)
	}
}

func TestQueryLLMFailureEmitsError(t *testing.T) {
	provider := &scriptedProvider{err: fmt.Errorf("model exploded")}
	engine := newQueryEngine(t, provider)

	events := runQuery(engine, "hello")
	if len(events) != 1 || events[0].Type != "error" || !strings.Contains(events[0].Content, "model exploded") {
		t.Fatalf("events = %+v, want single error event", events)
	}
}

func TestQueryUnknownToolIsReportedNotFatal(t *testing.T) {
	provider := &scriptedProvider{responses: []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "delete_everything", Input: json.RawMessage(`{}`)}}},
		{Text: "I cannot do that."},
	}}
	engine := newQueryEngine(t, provider)

	events := runQuery(engine, "wipe the cluster")
	if got := eventTypes(events); got != "tool,text,done" {
		t.Fatalf("event sequence = %s", got)
	}
	result := provider.requests[1].Messages[2].ToolResults[0]
	if !result.IsError || !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("expected unknown-tool error result, got %+v", result)
	}
}
