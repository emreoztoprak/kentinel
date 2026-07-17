package agent

import (
	"context"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

func newProposalStore(t *testing.T) *Store {
	t.Helper()
	return NewPersistentStore(filepath.Join(t.TempDir(), "p.db"), 90, 20, discardLog())
}

func TestProposalLifecycle(t *testing.T) {
	s := newProposalStore(t)

	p, err := s.SaveProposal(Proposal{
		Kind: "deployments", Namespace: "shop", Name: "orders-api",
		Rationale: "fix image tag", ProposedYAML: "kind: Deployment\n",
	})
	if err != nil {
		t.Fatalf("SaveProposal: %v", err)
	}
	if p.ID == "" || p.Status != ProposalPending {
		t.Fatalf("new proposal not pending with ID: %+v", p)
	}

	pending, _ := s.ListProposals(true)
	if len(pending) != 1 {
		t.Fatalf("pending list = %d, want 1", len(pending))
	}

	// Resolve (applied). Second resolve must fail — no double-apply.
	if err := s.ResolveProposal(p.ID, ""); err != nil {
		t.Fatalf("ResolveProposal: %v", err)
	}
	if err := s.ResolveProposal(p.ID, ""); err == nil {
		t.Error("resolving an already-applied proposal should fail (double-apply guard)")
	}
	got, ok, _ := s.GetProposal(p.ID)
	if !ok || got.Status != ProposalApplied || got.DecidedAt == nil {
		t.Fatalf("after resolve: %+v ok=%v", got, ok)
	}
	if pend, _ := s.ListProposals(true); len(pend) != 0 {
		t.Errorf("applied proposal must leave the pending list: %d remain", len(pend))
	}
}

func TestRejectThenApplyIsBlocked(t *testing.T) {
	s := newProposalStore(t)
	p, _ := s.SaveProposal(Proposal{Kind: "pods", Namespace: "x", Name: "y", ProposedYAML: "kind: Pod\n"})
	if err := s.RejectProposal(p.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// A rejected proposal can't then be applied (approve-after-reject race).
	if err := s.ResolveProposal(p.ID, ""); err == nil {
		t.Error("resolving a rejected proposal must fail")
	}
	got, _, _ := s.GetProposal(p.ID)
	if got.Status != ProposalRejected {
		t.Errorf("status = %s, want rejected", got.Status)
	}
}

func TestProposeChangeToolValidatesAndSnapshots(t *testing.T) {
	// A live deployment the tool will snapshot.
	dep := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{"name": "orders-api", "namespace": "shop"},
		"spec":     map[string]interface{}{"replicas": int64(1)},
	}}
	dyn := fake.NewSimpleDynamicClient(clientscheme.Scheme, dep)
	client := &k8s.Client{Clientset: kfake.NewSimpleClientset(), Dynamic: dyn}
	s := newProposalStore(t)

	goodYAML := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: orders-api\n  namespace: shop\nspec:\n  replicas: 2\n"

	// Mismatched name must be rejected before anything is stored.
	if _, err := proposeChange(context.Background(), client, s, "deployments", "shop", "WRONG", goodYAML, "x"); err == nil {
		t.Error("propose_change must reject a manifest whose name != target")
	}
	if list, _ := s.ListProposals(false); len(list) != 0 {
		t.Fatalf("a rejected proposal must not be stored: %d", len(list))
	}

	// Valid proposal: stored pending, with a current snapshot for the diff.
	out, err := proposeChange(context.Background(), client, s, "deployments", "shop", "orders-api", goodYAML, "bump replicas")
	if err != nil {
		t.Fatalf("valid propose_change failed: %v", err)
	}
	if out == "" {
		t.Error("expected a confirmation string for the model")
	}
	list, _ := s.ListProposals(true)
	if len(list) != 1 || list[0].CurrentYAML == "" || list[0].ProposedYAML != goodYAML {
		t.Fatalf("stored proposal wrong: %+v", list)
	}
}

func TestProposeChangeToolGatedByMode(t *testing.T) {
	readonlyTools := queryTools(false, false)
	assistedTools := queryTools(false, true)

	name := "propose_change"
	inReadonly, inAssisted := false, false
	for _, tl := range readonlyTools {
		if tl.Name == name {
			inReadonly = true
		}
	}
	for _, tl := range assistedTools {
		if tl.Name == name {
			inAssisted = true
		}
	}
	if inReadonly {
		t.Error("propose_change must NOT be offered in readonly mode")
	}
	if !inAssisted {
		t.Error("propose_change must be offered in assisted mode")
	}
}

func TestSaveProposalRequiresPersistence(t *testing.T) {
	s := NewStore(20) // memory-only, no db
	if _, err := s.SaveProposal(Proposal{Kind: "pods", Name: "x", ProposedYAML: "kind: Pod\n"}); err == nil {
		t.Error("SaveProposal must error without a persistent database")
	}
}
