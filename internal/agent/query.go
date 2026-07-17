package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/llm"
)

const querySystemPromptBase = `You are a Kubernetes assistant embedded in a cluster dashboard.
You answer questions about THIS cluster using the provided tools. Guidelines:
- Ground every claim in tool output. If you did not look, do not guess.
- Prefer targeted tool calls (namespace + name) over listing everything.
- For log analysis, use sinceSeconds to honor time windows the user asks for.
- Answer in concise Markdown. Use short bullet lists; include resource names verbatim.`

// readonly appendix: no write path at all.
const querySystemPromptReadOnly = `
- You cannot modify the cluster. If a fix is needed, explain the exact kubectl command or UI action the user should take.`

// assisted appendix: the propose_change tool is available, gated by approval.
const querySystemPromptAssisted = `
- When the user asks you to fix, change, or update something, use the propose_change tool. First read the current manifest with get_resource, then propose the FULL modified manifest. This does NOT apply the change — it queues it for the user to approve.
- Never claim you have applied or changed anything. You can only propose; a human approves and the system applies. Make changes minimal and targeted.`

func querySystemPrompt(assisted bool) string {
	if assisted {
		return querySystemPromptBase + querySystemPromptAssisted
	}
	return querySystemPromptBase + querySystemPromptReadOnly
}

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
	store   *Store // for propose_change (assisted mode); may be nil
	log     *slog.Logger
}

// NewQueryEngine wires the query engine.
func NewQueryEngine(client *k8s.Client, runtime *Runtime, store *Store, log *slog.Logger) *QueryEngine {
	return &QueryEngine{k8s: client, runtime: runtime, store: store, log: log}
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
	assisted := q.runtime.Assisted()
	messages := []llm.Message{{Role: "user", Text: prompt}}
	tools := queryTools(prom != nil, assisted)

	for iteration := 0; iteration < maxQueryIterations; iteration++ {
		resp, err := provider.Chat(queryCtx, llm.ChatRequest{
			System:    querySystemPrompt(assisted),
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
			output, err := runTool(queryCtx, q.k8s, prom, q.store, call)
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
