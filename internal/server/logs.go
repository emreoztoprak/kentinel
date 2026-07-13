package server

import (
	"bufio"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

// handlePodLogs returns pod logs. With ?follow=true it streams as
// Server-Sent Events (one "log" event per line); otherwise it returns the
// requested tail as plain text.
func (s *Server) handlePodLogs(w http.ResponseWriter, r *http.Request) {
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	q := r.URL.Query()

	opts := k8s.LogOptions{
		Container:    q.Get("container"),
		Follow:       q.Get("follow") == "true",
		TailLines:    parseInt64(q.Get("tailLines"), 500),
		SinceSeconds: parseInt64(q.Get("sinceSeconds"), 0),
		Previous:     q.Get("previous") == "true",
	}

	stream, err := s.k8s.StreamLogs(r.Context(), namespace, name, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	defer stream.Close()

	if !opts.Follow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			fmt.Fprintln(w, scanner.Text())
		}
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeBadRequest(w, "streaming not supported by this connection")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		// SSE data frames; the client re-assembles lines.
		fmt.Fprintf(w, "event: log\ndata: %s\n\n", scanner.Text())
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil && r.Context().Err() == nil {
		s.log.Warn("log stream ended with error", "pod", namespace+"/"+name, "error", err)
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
	}
}

func parseInt64(v string, def int64) int64 {
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
