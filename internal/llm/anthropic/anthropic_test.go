package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/emreoztoprak/kentinel/internal/llm"
)

// fakeAPI mimics POST /v1/messages: captures the request body and returns a
// canned response containing both a text block and a tool_use block.
func fakeAPI(t *testing.T, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_test", "type": "message", "role": "assistant",
			"model": "claude-opus-4-8", "stop_reason": "tool_use",
			"usage": {"input_tokens": 10, "output_tokens": 5},
			"content": [
				{"type": "text", "text": "Checking the events."},
				{"type": "tool_use", "id": "tu_1", "name": "get_events", "input": {"namespace": "app"}}
			]
		}`))
	}))
}

func TestChatWireFormatAndResponseMapping(t *testing.T) {
	var captured map[string]interface{}
	server := fakeAPI(t, &captured)
	defer server.Close()

	p := New("test-key", "claude-opus-4-8", option.WithBaseURL(server.URL))

	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		System: "you are a test",
		Messages: []llm.Message{
			{Role: "user", Text: "what is happening in app?"},
			{Role: "assistant", Text: "let me look", ToolCalls: []llm.ToolCall{
				{ID: "tu_0", Name: "get_cluster_overview", Input: json.RawMessage(`{}`)},
			}},
			{Role: "user", ToolResults: []llm.ToolResult{
				{ID: "tu_0", Name: "get_cluster_overview", Content: `{"nodes":1}`},
			}},
		},
		Tools: []llm.Tool{{
			Name:        "get_events",
			Description: "list events",
			Properties:  map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}},
			Required:    []string{"namespace"},
		}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// --- request wire format ---
	if captured["model"] != "claude-opus-4-8" {
		t.Errorf("model = %v", captured["model"])
	}
	system := captured["system"].([]interface{})[0].(map[string]interface{})
	if system["text"] != "you are a test" {
		t.Errorf("system = %v", system)
	}

	messages := captured["messages"].([]interface{})
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	// Assistant turn must carry text + tool_use blocks.
	assistant := messages[1].(map[string]interface{})
	assistantBlocks := assistant["content"].([]interface{})
	lastAssistant := assistantBlocks[len(assistantBlocks)-1].(map[string]interface{})
	if assistant["role"] != "assistant" || lastAssistant["type"] != "tool_use" || lastAssistant["id"] != "tu_0" {
		t.Errorf("assistant turn = %v", assistant)
	}
	// Tool results must be a user turn of tool_result blocks with matching IDs.
	toolTurn := messages[2].(map[string]interface{})
	toolBlock := toolTurn["content"].([]interface{})[0].(map[string]interface{})
	if toolTurn["role"] != "user" || toolBlock["type"] != "tool_result" || toolBlock["tool_use_id"] != "tu_0" {
		t.Errorf("tool result turn = %v", toolTurn)
	}

	tools := captured["tools"].([]interface{})
	tool := tools[0].(map[string]interface{})
	schema := tool["input_schema"].(map[string]interface{})
	if tool["name"] != "get_events" || schema["required"].([]interface{})[0] != "namespace" {
		t.Errorf("tool definition = %v", tool)
	}

	// --- response mapping ---
	if resp.Text != "Checking the events." {
		t.Errorf("text = %q", resp.Text)
	}
	if !resp.HasToolCalls() || resp.ToolCalls[0].ID != "tu_1" || resp.ToolCalls[0].Name != "get_events" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	var input map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Input, &input); err != nil || input["namespace"] != "app" {
		t.Errorf("tool input = %s", resp.ToolCalls[0].Input)
	}
}

func TestChatAPIErrorIsSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer server.Close()

	p := New("bad-key", "claude-opus-4-8", option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	if _, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Text: "hi"}},
	}); err == nil {
		t.Fatal("expected error for HTTP 401")
	}
}
