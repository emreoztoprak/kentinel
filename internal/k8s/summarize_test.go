package k8s

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestSummarizePodCrashLoop(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"nodeName": "worker-1"},
		"status": map[string]interface{}{
			"phase": "Running",
			"podIP": "10.0.0.5",
			"containerStatuses": []interface{}{
				map[string]interface{}{
					"ready":        false,
					"restartCount": int64(7),
					"state": map[string]interface{}{
						"waiting": map[string]interface{}{"reason": "CrashLoopBackOff"},
					},
				},
			},
		},
	}}

	extra := summarize("pods", pod)
	if extra["status"] != "CrashLoopBackOff" {
		t.Errorf("status = %q, want CrashLoopBackOff (waiting reason should override phase)", extra["status"])
	}
	if extra["ready"] != "0/1" {
		t.Errorf("ready = %q, want 0/1", extra["ready"])
	}
	if extra["restarts"] != "7" {
		t.Errorf("restarts = %q, want 7", extra["restarts"])
	}
	if extra["node"] != "worker-1" {
		t.Errorf("node = %q, want worker-1", extra["node"])
	}
}

func TestSummarizeWorkload(t *testing.T) {
	deploy := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"replicas": int64(3)},
		"status": map[string]interface{}{
			"readyReplicas":     int64(2),
			"updatedReplicas":   int64(3),
			"availableReplicas": int64(2),
		},
	}}

	extra := summarize("deployments", deploy)
	if extra["ready"] != "2/3" {
		t.Errorf("ready = %q, want 2/3", extra["ready"])
	}

	// A stuck rollout: 1 desired, 1 available (old pod still serving), but a
	// new pod is unavailable (e.g. ImagePullBackOff). availableReplicas alone
	// looks healthy — the summary must flag the incomplete rollout so the
	// agent/dashboard don't call it fine.
	stuck := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"replicas": int64(1)},
		"status": map[string]interface{}{
			"readyReplicas":       int64(1),
			"updatedReplicas":     int64(1),
			"availableReplicas":   int64(1),
			"unavailableReplicas": int64(1),
		},
	}}
	if got := summarize("deployments", stuck)["rollout"]; got == "" {
		t.Error("a stuck rollout (unavailableReplicas>0) must surface a rollout hint")
	}

	// A healthy steady-state deployment must NOT show a rollout hint.
	healthy := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": map[string]interface{}{"replicas": int64(2)},
		"status": map[string]interface{}{
			"readyReplicas":     int64(2),
			"updatedReplicas":   int64(2),
			"availableReplicas": int64(2),
		},
	}}
	if got := summarize("deployments", healthy)["rollout"]; got != "" {
		t.Errorf("healthy deployment must not show a rollout hint, got %q", got)
	}
}

func TestSummarizeUnknownKindIsNil(t *testing.T) {
	if extra := summarize("widgets", &unstructured.Unstructured{Object: map[string]interface{}{}}); extra != nil {
		t.Errorf("unknown kind should return nil, got %v", extra)
	}
}
