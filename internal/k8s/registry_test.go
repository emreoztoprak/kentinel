package k8s

import (
	"sort"
	"testing"
)

func TestLookupKind(t *testing.T) {
	info, err := LookupKind("pods")
	if err != nil {
		t.Fatalf("LookupKind(pods) returned error: %v", err)
	}
	if info.GVR.Resource != "pods" || info.GVR.Version != "v1" || !info.Namespaced {
		t.Errorf("unexpected pods info: %+v", info)
	}

	if _, err := LookupKind("widgets"); err == nil {
		t.Error("LookupKind(widgets) should fail for unknown kind")
	}
}

func TestSupportedKindsSortedAndComplete(t *testing.T) {
	kinds := SupportedKinds()
	if len(kinds) != len(registry) {
		t.Fatalf("SupportedKinds returned %d kinds, registry has %d", len(kinds), len(registry))
	}
	if !sort.SliceIsSorted(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind }) {
		t.Error("SupportedKinds is not sorted")
	}
	for _, k := range kinds {
		if k.DisplayName == "" {
			t.Errorf("kind %s has no display name", k.Kind)
		}
	}
}
