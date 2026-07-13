// Package openaicompat implements llm.Provider over the OpenAI
// chat-completions wire protocol with function calling. It powers three
// providers from one implementation:
//
//   - OpenAI (ChatGPT):  https://api.openai.com/v1
//   - DeepSeek:          https://api.deepseek.com/v1  (natively compatible)
//   - Google Gemini:     https://generativelanguage.googleapis.com/v1beta/openai
//     (Google's official OpenAI-compatibility endpoint)
//
// Anything else that speaks this protocol (vLLM, LiteLLM, internal LLM
// gateways) works too by pointing a provider at its base URL.
package openaicompat

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
)

// Provider implements llm.Provider against an OpenAI-compatible API.
type Provider struct {
	name    string // provider identity: "openai", "deepseek", "gemini", ...
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// New creates a provider. name is the user-facing provider identity;
// baseURL is the API root (ending before /chat/completions).
func New(name, baseURL, apiKey, model string) *Provider {
	return &Provider{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 3 * time.Minute},
	}
}

func (p *Provider) Name() string  { return p.name }
func (p *Provider) Model() string { return p.model }

// --- wire types (OpenAI chat completions subset) ---

type chatMessage struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name string `json:"name"`
	// Arguments is a JSON-encoded string per the OpenAI protocol.
	Arguments string `json:"arguments"`
}

type toolDef struct {
	Type     string      `json:"type"` // "function"
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []toolDef     `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Chat implements llm.Provider.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	body := chatRequest{
		Model:    p.model,
		Messages: convertMessages(req.System, req.Messages),
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, toolDef{
			Type: "function",
			Function: functionDef{
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
		return nil, fmt.Errorf("%s: encoding request: %w", p.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", p.name, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: reading response: %w", p.name, err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%s: HTTP %d, undecodable response: %s", p.name, resp.StatusCode, truncate(string(raw), 300))
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("%s: %s", p.name, parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", p.name, resp.StatusCode, truncate(string(raw), 300))
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("%s: response contained no choices", p.name)
	}

	message := parsed.Choices[0].Message
	out := &llm.ChatResponse{Text: message.Content}
	for i, tc := range message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
			ID:    id,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
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
				out = append(out, chatMessage{Role: "tool", ToolCallID: tr.ID, Content: content})
			}
		case len(m.ToolCalls) > 0:
			msg := chatMessage{Role: "assistant", Content: m.Text}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, toolCall{
					ID:   tc.ID,
					Type: "function",
					Function: functionCall{
						Name:      tc.Name,
						Arguments: string(tc.Input),
					},
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- provider presets ---

// Preset describes one OpenAI-compatible provider offering.
type Preset struct {
	Name        string
	BaseURL     string
	DefaultModel string
	KnownModels []string
}

// Presets are the built-in OpenAI-compatible providers. Model lists are
// curated for the Settings dropdown — any valid model ID still works.
var Presets = map[string]Preset{
	"openai": {
		Name:         "openai",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-5.1",
		KnownModels:  []string{"gpt-5.2", "gpt-5.1", "gpt-5", "gpt-4.1", "gpt-4o", "gpt-4o-mini"},
	},
	"deepseek": {
		Name:         "deepseek",
		BaseURL:      "https://api.deepseek.com/v1",
		DefaultModel: "deepseek-chat",
		KnownModels:  []string{"deepseek-chat", "deepseek-reasoner"},
	},
	"gemini": {
		Name:         "gemini",
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta/openai",
		DefaultModel: "gemini-2.5-flash",
		KnownModels:  []string{"gemini-3-pro-preview", "gemini-2.5-pro", "gemini-2.5-flash"},
	},
}
