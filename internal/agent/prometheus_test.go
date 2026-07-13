package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emreoztoprak/kentinel/internal/llm"
)

func toolCallNamed(name string, input []byte) llm.ToolCall {
	return llm.ToolCall{ID: "c1", Name: name, Input: input}
}

func promServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(response))
	}))
}

func TestPromQuery(t *testing.T) {
	server := promServer(t, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"__name__":"pod_memory_working_set_bytes","namespace":"app","pod":"web-1"},"value":[1720000000,"268435456"]},
		{"metric":{"__name__":"pod_memory_working_set_bytes","namespace":"app","pod":"web-2"},"value":[1720000000,"536870912"]}
	]}}`)
	defer server.Close()

	samples, err := newPromClient(server.URL).Query(context.Background(), "pod_memory_working_set_bytes")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(samples) != 2 || samples[0].Labels["pod"] != "web-1" || samples[0].Value != 268435456 {
		t.Errorf("samples = %+v", samples)
	}

	// Rendering: sorted by value desc, bytes humanized, noise labels dropped.
	rendered := renderSamples(samples, 10)
	lines := strings.Split(strings.TrimSpace(rendered), "\n")
	if !strings.Contains(lines[0], "web-2") || !strings.Contains(lines[0], "512.0MiB") {
		t.Errorf("first rendered line = %q (want web-2 / 512.0MiB first)", lines[0])
	}
	if strings.Contains(rendered, "instance=") {
		t.Errorf("noise labels must be dropped: %s", rendered)
	}
}

func TestPromQueryErrors(t *testing.T) {
	// PromQL error surfaces with the server's message.
	server := promServer(t, `{"status":"error","errorType":"bad_data","error":"parse error: unexpected end of input"}`)
	defer server.Close()
	if _, err := newPromClient(server.URL).Query(context.Background(), "sum("); err == nil ||
		!strings.Contains(err.Error(), "parse error") {
		t.Fatalf("err = %v, want PromQL parse error surfaced", err)
	}

	// Matrix results are rejected with guidance.
	matrix := promServer(t, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	defer matrix.Close()
	if _, err := newPromClient(matrix.URL).Query(context.Background(), "up[5m]"); err == nil ||
		!strings.Contains(err.Error(), "rate()") {
		t.Fatalf("err = %v, want unsupported-type guidance", err)
	}

	// Unreachable host.
	if _, err := newPromClient("http://127.0.0.1:1").Query(context.Background(), "up"); err == nil {
		t.Fatal("expected error for unreachable Prometheus")
	}
}

func TestQueryMetricsToolWiring(t *testing.T) {
	server := promServer(t, `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"__name__":"up","job":"kubelet-resource"},"value":[1720000000,"1"]}
	]}}`)
	defer server.Close()

	input, _ := json.Marshal(map[string]string{"query": "up"})
	out, err := runTool(context.Background(), nil, newPromClient(server.URL), toolCallNamed("query_metrics", input))
	if err != nil {
		t.Fatalf("runTool failed: %v", err)
	}
	if !strings.Contains(out, "up{} = 1") {
		t.Errorf("output = %q", out)
	}

	// Without a client the metrics tools return a clear configuration error.
	if _, err := runTool(context.Background(), nil, nil, toolCallNamed("query_metrics", input)); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want not-configured error", err)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[float64]string{512: "512.0B", 2048: "2.0KiB", 268435456: "256.0MiB", 2147483648: "2.0GiB"}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%v) = %q, want %q", in, got, want)
		}
	}
}
