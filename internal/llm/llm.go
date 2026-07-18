// Package llm defines a small provider-agnostic chat interface with tool
// calling. Implementations live in the subpackages (anthropic, ollama); the
// agent only depends on this interface.
package llm

import (
	"context"
	"encoding/json"
)

// Provider is a chat-completion backend with optional tool calling.
type Provider interface {
	// Chat sends the conversation and returns either final text or tool calls
	// the caller must execute (appending results before calling Chat again).
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Name identifies the provider, e.g. "anthropic" or "ollama".
	Name() string
	// Model returns the configured model ID.
	Model() string
}

// Message is one turn in a conversation. Exactly one of the content forms is
// typically set: Text (plain user/assistant text), ToolCalls (assistant
// requesting tools, possibly with accompanying Text), or ToolResults (results
// the application provides back).
type Message struct {
	Role        string // "user" or "assistant"
	Text        string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

// ToolCall is a request from the model to run a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult carries a tool's output back to the model.
type ToolResult struct {
	ID      string // matches ToolCall.ID
	Name    string // tool name (required by providers without call IDs)
	Content string
	IsError bool
}

// Tool describes a callable tool as a JSON-schema object.
type Tool struct {
	Name        string
	Description string
	// Properties is the JSON-schema "properties" object.
	Properties map[string]interface{}
	Required   []string
}

// ChatRequest is a full conversation snapshot (providers are stateless).
type ChatRequest struct {
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int // 0 = provider default
}

// ChatResponse is the model's reply.
type ChatResponse struct {
	Text      string
	ToolCalls []ToolCall // non-empty means the caller must run tools and continue
	Usage     TokenUsage // token counts for this call (zero if the provider omits them)
}

// TokenUsage is the token count of a single LLM call, used for cost tracking.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// HasToolCalls reports whether the model asked for tool execution.
func (r *ChatResponse) HasToolCalls() bool { return len(r.ToolCalls) > 0 }
