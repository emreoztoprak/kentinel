package server

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Same-origin in practice: the SPA is served by this server (or the Vite
	// dev proxy). v1 has no auth, so we accept the upgrade and document the
	// trusted-network assumption in docs/security.md.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// terminal message protocol (JSON over the websocket):
//
//	client → server: {"type":"stdin","data":"..."} | {"type":"resize","cols":80,"rows":24}
//	server → client: {"type":"stdout","data":"..."} | {"type":"error","data":"..."} | {"type":"exit"}
type termMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// handlePodExec upgrades to a WebSocket and bridges it to a pod exec session.
func (s *Server) handlePodExec(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	container := r.URL.Query().Get("container")
	command := r.URL.Query().Get("command")
	if command == "" {
		command = "/bin/sh"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	session := &execSession{conn: conn, resize: make(chan remotecommand.TerminalSize, 4)}

	// Fail fast with a readable message instead of the kubelet's cryptic
	// "container not found" when the pod/container isn't running.
	if err := s.k8s.ValidateExecTarget(r.Context(), namespace, name, container); err != nil {
		session.send(termMessage{Type: "error", Data: err.Error()})
		session.send(termMessage{Type: "exit"})
		return
	}
	stdinReader, stdinWriter := io.Pipe()
	go session.readLoop(stdinWriter)

	s.log.Info("exec session started", "pod", namespace+"/"+name, "container", container, "command", command)

	err = s.k8s.Exec(r.Context(), k8s.ExecOptions{
		Namespace: namespace,
		Pod:       name,
		Container: container,
		Command:   []string{command},
		Stdin:     stdinReader,
		Stdout:    session.writer("stdout"),
		Stderr:    session.writer("stdout"), // TTY sessions merge the streams anyway
		TTY:       true,
		Resize:    session,
	})

	if err != nil {
		session.send(termMessage{Type: "error", Data: err.Error()})
		s.log.Warn("exec session ended with error", "pod", namespace+"/"+name, "error", err)
	}
	session.send(termMessage{Type: "exit"})
	_ = stdinWriter.Close()
}

// execSession bridges websocket frames to the exec streams.
type execSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	resize  chan remotecommand.TerminalSize
}

// readLoop pumps client frames into stdin / the resize queue. It runs until
// the websocket closes, which also terminates stdin (ending the shell).
func (t *execSession) readLoop(stdin *io.PipeWriter) {
	defer stdin.Close()
	defer close(t.resize)
	for {
		_, data, err := t.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg termMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "stdin":
			if _, err := stdin.Write([]byte(msg.Data)); err != nil {
				return
			}
		case "resize":
			select {
			case t.resize <- remotecommand.TerminalSize{Width: msg.Cols, Height: msg.Rows}:
			default: // drop resize if the queue is full
			}
		}
	}
}

// Next implements remotecommand.TerminalSizeQueue.
func (t *execSession) Next() *remotecommand.TerminalSize {
	size, ok := <-t.resize
	if !ok {
		return nil
	}
	return &size
}

func (t *execSession) send(msg termMessage) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_ = t.conn.WriteJSON(msg)
}

// writer adapts websocket sends to io.Writer for stdout/stderr.
func (t *execSession) writer(msgType string) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		t.send(termMessage{Type: msgType, Data: string(p)})
		return len(p), nil
	})
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
