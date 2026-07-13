// Package anthropic adapts the official Anthropic Go SDK to the llm.Provider
// interface using a manual tool-use conversation shape.
package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/emreoztoprak/kentinel/internal/llm"
)

const defaultMaxTokens = 4096

// Provider implements llm.Provider backed by the Anthropic Messages API.
type Provider struct {
	client sdk.Client
	model  string
}

// New creates the provider. model must be a valid Anthropic model ID
// (e.g. "claude-opus-4-8"). Extra options are for tests (custom base URL).
func New(apiKey, model string, opts ...option.RequestOption) *Provider {
	options := append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)
	return &Provider{
		client: sdk.NewClient(options...),
		model:  model,
	}
}

func (p *Provider) Name() string  { return "anthropic" }
func (p *Provider) Model() string { return p.model }

// KnownModels is the curated list of current Claude model IDs offered in the
// Settings UI dropdown, best-first. Any valid model ID still works via the
// API; this is a convenience list, not an allowlist.
func KnownModels() []string {
	return []string{
		"claude-opus-4-8",  // default — most capable Opus-tier, best all-round
		"claude-sonnet-5",  // near-Opus quality at lower cost
		"claude-haiku-4-5", // fastest and cheapest
		"claude-fable-5",   // most capable overall, premium pricing
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
	}
}

// Chat implements llm.Provider.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	params := sdk.MessageNewParams{
		Model:     sdk.Model(p.model),
		MaxTokens: int64(maxTokens),
		Messages:  convertMessages(req.Messages),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		params.Tools = convertTools(req.Tools)
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	out := &llm.ChatResponse{}
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case sdk.TextBlock:
			out.Text += b.Text
		case sdk.ToolUseBlock:
			out.ToolCalls = append(out.ToolCalls, llm.ToolCall{
				ID:    b.ID,
				Name:  b.Name,
				Input: json.RawMessage(b.JSON.Input.Raw()),
			})
		}
	}
	return out, nil
}

func convertMessages(messages []llm.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(messages))
	for _, m := range messages {
		switch {
		case len(m.ToolResults) > 0:
			blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.ToolResults))
			for _, tr := range m.ToolResults {
				blocks = append(blocks, sdk.NewToolResultBlock(tr.ID, tr.Content, tr.IsError))
			}
			out = append(out, sdk.NewUserMessage(blocks...))

		case len(m.ToolCalls) > 0:
			blocks := make([]sdk.ContentBlockParamUnion, 0, len(m.ToolCalls)+1)
			if m.Text != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Text))
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				_ = json.Unmarshal(tc.Input, &input)
				blocks = append(blocks, sdk.ContentBlockParamUnion{
					OfToolUse: &sdk.ToolUseBlockParam{ID: tc.ID, Name: tc.Name, Input: input},
				})
			}
			out = append(out, sdk.NewAssistantMessage(blocks...))

		case m.Role == "assistant":
			out = append(out, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Text)))

		default:
			out = append(out, sdk.NewUserMessage(sdk.NewTextBlock(m.Text)))
		}
	}
	return out
}

func convertTools(tools []llm.Tool) []sdk.ToolUnionParam {
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := sdk.ToolParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			InputSchema: sdk.ToolInputSchemaParam{
				Properties: t.Properties,
				Required:   t.Required,
			},
		}
		out = append(out, sdk.ToolUnionParam{OfTool: &tool})
	}
	return out
}
