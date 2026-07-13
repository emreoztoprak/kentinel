package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emreoztoprak/kentinel/internal/llm"
)

func TestChatWireFormatAndResponseMapping(t *testing.T) {
	var captured map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("auth header = %q", auth)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_, _ = w.Write([]byte(`{"choices":[{"message":{
			"role":"assistant","content":"Checking.",
			"tool_calls":[{"id":"call_9","type":"function","function":{"name":"get_events","arguments":"{\"namespace\":\"app\"}"}}]
		}}]}`))
	}))
	defer server.Close()

	p := New("openai", server.URL, "test-key", "gpt-5.1")
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		System: "you are a test",
		Messages: []llm.Message{
			{Role: "user", Text: "what's up in app?"},
			{Role: "assistant", Text: "let me look", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "get_cluster_overview", Input: json.RawMessage(`{}`)},
			}},
			{Role: "user", ToolResults: []llm.ToolResult{
				{ID: "call_1", Name: "get_cluster_overview", Content: `{"nodes":1}`},
			}},
		},
		Tools: []llm.Tool{{
			Name: "get_events", Description: "list events",
			Properties: map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}},
			Required:   []string{"namespace"},
		}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// --- request wire format ---
	messages := captured["messages"].([]interface{})
	if len(messages) != 4 { // system + user + assistant + tool
		t.Fatalf("messages = %d, want 4", len(messages))
	}
	system := messages[0].(map[string]interface{})
	if system["role"] != "system" || system["content"] != "you are a test" {
		t.Errorf("system message = %v", system)
	}
	assistant := messages[2].(map[string]interface{})
	toolCalls := assistant["tool_calls"].([]interface{})
	fn := toolCalls[0].(map[string]interface{})["function"].(map[string]interface{})
	if fn["name"] != "get_cluster_overview" || fn["arguments"] != "{}" {
		t.Errorf("assistant tool call = %v (arguments must be a JSON string)", fn)
	}
	toolMsg := messages[3].(map[string]interface{})
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool message = %v", toolMsg)
	}
	tool := captured["tools"].([]interface{})[0].(map[string]interface{})
	params := tool["function"].(map[string]interface{})["parameters"].(map[string]interface{})
	if tool["type"] != "function" || params["type"] != "object" {
		t.Errorf("tool definition = %v", tool)
	}

	// --- response mapping ---
	if resp.Text != "Checking." || !resp.HasToolCalls() {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.ToolCalls[0].ID != "call_9" || string(resp.ToolCalls[0].Input) != `{"namespace":"app"}` {
		t.Errorf("tool call = %+v (string arguments must become RawMessage)", resp.ToolCalls[0])
	}
}

func TestChatAPIErrorSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	p := New("deepseek", server.URL, "bad", "deepseek-chat")
	_, err := p.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: "user", Text: "hi"}}})
	if err == nil || !strings.Contains(err.Error(), "Incorrect API key") || !strings.Contains(err.Error(), "deepseek") {
		t.Fatalf("err = %v, want provider-labeled API error", err)
	}
}

func TestPresetsComplete(t *testing.T) {
	for _, name := range []string{"openai", "deepseek", "gemini"} {
		preset, ok := Presets[name]
		if !ok {
			t.Fatalf("missing preset %s", name)
		}
		if preset.BaseURL == "" || preset.DefaultModel == "" || len(preset.KnownModels) == 0 {
			t.Errorf("incomplete preset %s: %+v", name, preset)
		}
	}
}
