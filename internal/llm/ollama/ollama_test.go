package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emreoztoprak/kentinel/internal/llm"
)

func TestChatToolCallRoundTrip(t *testing.T) {
	var captured chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(chatResponse{
			Message: chatMessage{
				Role: "assistant",
				ToolCalls: []toolCall{
					{Function: toolCallFunction{Name: "get_events", Arguments: json.RawMessage(`{"namespace":"app"}`)}},
				},
			},
			Done: true,
		})
	}))
	defer server.Close()

	p := New(server.URL, "test-model")
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		System:   "system prompt",
		Messages: []llm.Message{{Role: "user", Text: "what's happening in app?"}},
		Tools: []llm.Tool{{
			Name:        "get_events",
			Description: "list events",
			Properties:  map[string]interface{}{"namespace": map[string]interface{}{"type": "string"}},
		}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	// Request shape
	if captured.Model != "test-model" || captured.Stream {
		t.Errorf("unexpected request: model=%s stream=%v", captured.Model, captured.Stream)
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" {
		t.Errorf("expected [system,user] messages, got %+v", captured.Messages)
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "get_events" {
		t.Errorf("tools not forwarded: %+v", captured.Tools)
	}

	// Response mapping
	if !resp.HasToolCalls() || resp.ToolCalls[0].Name != "get_events" {
		t.Fatalf("tool call not mapped: %+v", resp)
	}
	if string(resp.ToolCalls[0].Input) != `{"namespace":"app"}` {
		t.Errorf("tool input = %s", resp.ToolCalls[0].Input)
	}
}

func TestChatToolResultsBecomeToolMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// user, assistant(tool call), tool
		if len(req.Messages) != 3 {
			t.Errorf("expected 3 messages, got %d: %+v", len(req.Messages), req.Messages)
		} else {
			if req.Messages[1].Role != "assistant" || len(req.Messages[1].ToolCalls) != 1 {
				t.Errorf("assistant tool-call message malformed: %+v", req.Messages[1])
			}
			if req.Messages[2].Role != "tool" || req.Messages[2].ToolName != "get_events" {
				t.Errorf("tool result message malformed: %+v", req.Messages[2])
			}
		}
		_ = json.NewEncoder(w).Encode(chatResponse{
			Message: chatMessage{Role: "assistant", Content: "All clear."},
			Done:    true,
		})
	}))
	defer server.Close()

	p := New(server.URL, "test-model")
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Text: "check events"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_0", Name: "get_events", Input: json.RawMessage(`{}`)}}},
			{Role: "user", ToolResults: []llm.ToolResult{{ID: "call_0", Name: "get_events", Content: "[]"}}},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Text != "All clear." || resp.HasToolCalls() {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:0.6b"},{"name":"llama3.1:8b"}]}`))
	}))
	defer server.Close()

	models, err := ListModels(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 || models[0] != "qwen3:0.6b" || models[1] != "llama3.1:8b" {
		t.Errorf("models = %v", models)
	}
}

func TestListModelsUnreachable(t *testing.T) {
	if _, err := ListModels(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

func TestChatServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'missing' not found"}`))
	}))
	defer server.Close()

	p := New(server.URL, "missing")
	if _, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{{Role: "user", Text: "hi"}},
	}); err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}
