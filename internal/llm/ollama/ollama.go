// Package ollama adapts a local Ollama server (/api/chat with tool calling)
// to the llm.Provider interface. Requires a tool-calling-capable model
// (e.g. qwen3, llama3.1) — see docs/configuration.md.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emreoztoprak/kentinel/internal/llm"
	"github.com/emreoztoprak/kentinel/internal/safehttp"
)

// Provider implements llm.Provider against an Ollama server.
type Provider struct {
	host   string
	model  string
	client *http.Client
}

// New creates the provider. host is the Ollama base URL
// (e.g. http://localhost:11434).
func New(host, model string) *Provider {
	return &Provider{
		host:  strings.TrimRight(host, "/"),
		model: model,
		// Local models can be slow, especially on first load. User-supplied
		// host, so the dialer blocks cloud-metadata targets.
		client: safehttp.Client(5 * time.Minute),
	}
}

func (p *Provider) Name() string  { return "ollama" }
func (p *Provider) Model() string { return p.model }

// --- Ollama wire types (subset of /api/chat) ---

type chatMessage struct {
	Role      string     `json:"role"` // system | user | assistant | tool
	Content   string     `json:"content"`
	ToolName  string     `json:"tool_name,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	Function toolCallFunction `json:"function"`
}

type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolDef struct {
	Type     string      `json:"type"` // always "function"
	Function toolDefFunc `json:"function"`
}

type toolDefFunc struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []toolDef     `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// Chat implements llm.Provider.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	body := chatRequest{
		Model:    p.model,
		Messages: convertMessages(req.System, req.Messages),
		Stream:   false,
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, toolDef{
			Type: "function",
			Function: toolDefFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": t.Properties,
					"required":   t.Required,
				},
			},
		})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request to %s failed (is Ollama running?): %w", p.host, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("ollama: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// Ollama returns 404 when the model isn't pulled — the most common
		// cause of this error, so make it actionable instead of a bare 404.
		return nil, fmt.Errorf("ollama: model %q is not installed on the server at %s — pick an installed model in Settings, or run `ollama pull %s`", p.model, p.host, p.model)
	}
	if resp.StatusCode != http.StatusOK {
		// No response-body echo: the host is user-supplied (SSRF hygiene).
		return nil, fmt.Errorf("ollama: HTTP %d from %s", resp.StatusCode, p.host)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("ollama: %s", parsed.Error)
	}

	out := &llm.ChatResponse{Text: parsed.Message.Content}
	for i, tc := range parsed.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			// Ollama has no call IDs; synthesize stable ones for this turn.
			ID:    fmt.Sprintf("call_%d", i),
			Name:  tc.Function.Name,
			Input: tc.Function.Arguments,
		})
	}
	return out, nil
}

func convertMessages(system string, messages []llm.Message) []chatMessage {
	out := make([]chatMessage, 0, len(messages)+1)
	if system != "" {
		out = append(out, chatMessage{Role: "system", Content: system})
	}
	for _, m := range messages {
		switch {
		case len(m.ToolResults) > 0:
			for _, tr := range m.ToolResults {
				content := tr.Content
				if tr.IsError {
					content = "ERROR: " + content
				}
				out = append(out, chatMessage{Role: "tool", Content: content, ToolName: tr.Name})
			}
		case len(m.ToolCalls) > 0:
			msg := chatMessage{Role: "assistant", Content: m.Text}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, toolCall{
					Function: toolCallFunction{Name: tc.Name, Arguments: tc.Input},
				})
			}
			out = append(out, msg)
		default:
			role := m.Role
			if role == "" {
				role = "user"
			}
			out = append(out, chatMessage{Role: role, Content: m.Text})
		}
	}
	return out
}

// ListModels returns the models installed on an Ollama server (GET /api/tags).
// Used by the Settings UI to populate the model dropdown.
func ListModels(ctx context.Context, host string) ([]string, error) {
	url := strings.TrimRight(host, "/") + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}

	resp, err := safehttp.Client(10 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request to %s failed (is Ollama running?): %w", host, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ollama: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// No response-body echo: the host is user-supplied (SSRF hygiene).
		return nil, fmt.Errorf("ollama: HTTP %d from %s", resp.StatusCode, host)
	}

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}

	names := make([]string, 0, len(parsed.Models))
	for _, m := range parsed.Models {
		names = append(names, m.Name)
	}
	return names, nil
}
