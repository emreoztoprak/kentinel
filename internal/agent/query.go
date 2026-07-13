package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/llm"
)

const querySystemPrompt = `You are a Kubernetes assistant embedded in a cluster dashboard.
You answer questions about THIS cluster using the provided read-only tools. Guidelines:
- Ground every claim in tool output. If you did not look, do not guess.
- Prefer targeted tool calls (namespace + name) over listing everything.
- For log analysis, use sinceSeconds to honor time windows the user asks for.
- You cannot modify the cluster. If a fix is needed, explain the exact kubectl command or UI action the user should take.
- Answer in concise Markdown. Use short bullet lists; include resource names verbatim.`

const maxQueryIterations = 15

// QueryEvent is one step of an in-progress query, streamed to the UI.
type QueryEvent struct {
	// Type: "text" (assistant said something), "tool" (a tool is being
	// called), "done" (final), "error".
	Type string `json:"type"`
	// Content holds text for "text"/"error"; tool description for "tool".
	Content string `json:"content"`
}

// QueryEngine answers ad-hoc user questions with an agentic tool loop. The
// provider is resolved per query so settings changes apply immediately.
type QueryEngine struct {
	k8s     *k8s.Client
	runtime *Runtime
	log     *slog.Logger
}

// NewQueryEngine wires the query engine.
func NewQueryEngine(client *k8s.Client, runtime *Runtime, log *slog.Logger) *QueryEngine {
	return &QueryEngine{k8s: client, runtime: runtime, log: log}
}

// Run executes one query, emitting progress events until "done" or "error".
// emit is called from a single goroutine.
func (q *QueryEngine) Run(ctx context.Context, prompt string, emit func(QueryEvent)) {
	start := time.Now()
	q.log.Info("query started", "prompt", truncateForLog(prompt))

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	provider := q.runtime.Provider()
	prom := q.runtime.Prometheus()
	messages := []llm.Message{{Role: "user", Text: prompt}}
	tools := queryTools(prom != nil)

	for iteration := 0; iteration < maxQueryIterations; iteration++ {
		resp, err := provider.Chat(queryCtx, llm.ChatRequest{
			System:    querySystemPrompt,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: 4096,
		})
		if err != nil {
			q.log.Error("query LLM call failed", "error", err)
			emit(QueryEvent{Type: "error", Content: "LLM request failed: " + err.Error()})
			return
		}

		if resp.Text != "" {
			emit(QueryEvent{Type: "text", Content: resp.Text})
		}

		if !resp.HasToolCalls() {
			emit(QueryEvent{Type: "done"})
			q.log.Info("query completed", "iterations", iteration+1, "duration", time.Since(start).Round(time.Millisecond))
			return
		}

		messages = append(messages, llm.Message{Role: "assistant", Text: resp.Text, ToolCalls: resp.ToolCalls})

		results := make([]llm.ToolResult, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			emit(QueryEvent{Type: "tool", Content: describeToolCall(call)})
			output, err := runTool(queryCtx, q.k8s, prom, call)
			result := llm.ToolResult{ID: call.ID, Name: call.Name, Content: output}
			if err != nil {
				// Tool errors go back to the model so it can adapt
				// (e.g. wrong namespace → list namespaces first).
				result.Content = err.Error()
				result.IsError = true
				q.log.Warn("query tool failed", "tool", call.Name, "error", err)
			}
			results = append(results, result)
		}
		messages = append(messages, llm.Message{Role: "user", ToolResults: results})
	}

	emit(QueryEvent{Type: "error", Content: fmt.Sprintf("query stopped after %d tool iterations without a final answer", maxQueryIterations)})
}

func describeToolCall(call llm.ToolCall) string {
	if len(call.Input) > 0 && string(call.Input) != "{}" {
		return fmt.Sprintf("%s %s", call.Name, string(call.Input))
	}
	return call.Name
}

func truncateForLog(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "..."
}
