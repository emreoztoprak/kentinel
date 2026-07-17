package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/emreoztoprak/kentinel/internal/config"
	"github.com/emreoztoprak/kentinel/internal/k8s"
)

// fakeAgent stands in for the agent's proposal API. It serves one proposal by
// ID and records the /resolve callback so the test can assert the audit path.
type fakeAgent struct {
	prop         agentProposal
	resolvedWith *string // nil until /resolve is called
}

func (f *fakeAgent) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/proposals/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/resolve") {
			var body struct {
				Error string `json:"error"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.resolvedWith = &body.Error
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"resolved"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(f.prop)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newProposalTestServer(t *testing.T, mode config.Mode, agentURL string, objs ...runtime.Object) *Server {
	dyn := dynfake.NewSimpleDynamicClient(clientscheme.Scheme, objs...)
	return New(&k8s.Client{Clientset: kfake.NewSimpleClientset(), Dynamic: dyn},
		agentURL, "", "test", mode, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestApplyProposalAppliesAndResolves(t *testing.T) {
	live := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-api", Namespace: "shop"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr32(1)},
	}
	fa := &fakeAgent{prop: agentProposal{
		ID: "abc", Status: "pending", Kind: "deployments", Namespace: "shop", Name: "orders-api",
		ProposedYAML: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: orders-api\n  namespace: shop\nspec:\n  replicas: 3\n",
	}}
	agent := fa.server(t)
	srv := newProposalTestServer(t, config.ModeAssisted, agent.URL, live)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	res, err := http.Post(ts.URL+"/api/v1/proposals/abc/apply", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("apply status %d: %s", res.StatusCode, body)
	}
	// The audit callback must have fired with no error (successful apply).
	if fa.resolvedWith == nil || *fa.resolvedWith != "" {
		t.Errorf("resolve callback = %v, want called with empty error", fa.resolvedWith)
	}
}

func TestApplyProposalBlockedInReadonly(t *testing.T) {
	fa := &fakeAgent{prop: agentProposal{ID: "abc", Status: "pending"}}
	agent := fa.server(t)
	srv := newProposalTestServer(t, config.ModeReadOnly, agent.URL)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	res, _ := http.Post(ts.URL+"/api/v1/proposals/abc/apply", "application/json", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("apply in readonly = %d, want 403", res.StatusCode)
	}
	if fa.resolvedWith != nil {
		t.Error("readonly apply must not reach the agent at all")
	}
}

func TestApplyNonPendingProposalConflicts(t *testing.T) {
	fa := &fakeAgent{prop: agentProposal{ID: "abc", Status: "applied"}}
	agent := fa.server(t)
	srv := newProposalTestServer(t, config.ModeAssisted, agent.URL)
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	res, _ := http.Post(ts.URL+"/api/v1/proposals/abc/apply", "application/json", nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("applying an already-applied proposal = %d, want 409", res.StatusCode)
	}
}

// TestResolveProxyBlocked confirms the server-internal /resolve endpoint isn't
// reachable through the public agent proxy (audit-spoofing guard).
func TestResolveProxyBlocked(t *testing.T) {
	srv := newProposalTestServer(t, config.ModeAssisted, "http://127.0.0.1:1")
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	res, _ := http.Post(ts.URL+"/api/v1/agent/proposals/abc/resolve", "application/json", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("/agent/proposals/{id}/resolve via proxy = %d, want 404 (blocked)", res.StatusCode)
	}
}

func ptr32(i int32) *int32 { return &i }
