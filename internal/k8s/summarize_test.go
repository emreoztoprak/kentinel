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
}

func TestSummarizeUnknownKindIsNil(t *testing.T) {
	if extra := summarize("widgets", &unstructured.Unstructured{Object: map[string]interface{}{}}); extra != nil {
		t.Errorf("unknown kind should return nil, got %v", extra)
	}
}
