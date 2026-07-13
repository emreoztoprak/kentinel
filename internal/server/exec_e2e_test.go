package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestExecWebSocketE2E drives a real exec session through a running server.
// It is skipped unless E2E_SERVER_URL and E2E_EXEC_POD (namespace/name of a
// running pod with /bin/sh) are set, e.g.:
//
//	E2E_SERVER_URL=localhost:8080 E2E_EXEC_POD=kube-system/coredns-xyz \
//	  go test ./internal/server -run TestExecWebSocketE2E -v
func TestExecWebSocketE2E(t *testing.T) {
	serverURL := os.Getenv("E2E_SERVER_URL")
	podRef := os.Getenv("E2E_EXEC_POD")
	if serverURL == "" || podRef == "" {
		t.Skip("set E2E_SERVER_URL and E2E_EXEC_POD to run this test")
	}
	parts := strings.SplitN(podRef, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("E2E_EXEC_POD must be namespace/name, got %q", podRef)
	}

	url := fmt.Sprintf("ws://%s/api/v1/pods/%s/%s/exec?command=/bin/sh", serverURL, parts[0], parts[1])
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dialing %s: %v", url, err)
	}
	defer conn.Close()

	send := func(v any) {
		if err := conn.WriteJSON(v); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	send(termMessage{Type: "resize", Cols: 80, Rows: 24})
	send(termMessage{Type: "stdin", Data: "echo EXEC_OK_$((6*7))\nexit\n"})

	deadline := time.Now().Add(20 * time.Second)
	var output strings.Builder
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			break // server closes after exit
		}
		var msg termMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "stdout":
			output.WriteString(msg.Data)
		case "error":
			t.Fatalf("exec error: %s", msg.Data)
		case "exit":
			if !strings.Contains(output.String(), "EXEC_OK_42") {
				t.Fatalf("expected EXEC_OK_42 in output, got:\n%s", output.String())
			}
			return
		}
	}
	t.Fatalf("session did not complete; output so far:\n%s", output.String())
}
