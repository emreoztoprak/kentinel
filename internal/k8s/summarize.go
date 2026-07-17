package k8s

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// summarize extracts kind-specific list columns from an unstructured object.
// Unknown kinds return nil — the UI falls back to name/namespace/age.
func summarize(kind string, obj *unstructured.Unstructured) map[string]string {
	switch kind {
	case "pods":
		return summarizePod(obj)
	case "deployments", "statefulsets":
		return summarizeWorkload(obj)
	case "daemonsets":
		return summarizeDaemonSet(obj)
	case "services":
		return summarizeService(obj)
	case "nodes":
		return summarizeNode(obj)
	case "jobs":
		return summarizeJob(obj)
	case "cronjobs":
		return summarizeCronJob(obj)
	case "persistentvolumeclaims":
		return summarizePVC(obj)
	case "configmaps":
		return map[string]string{"keys": fmt.Sprintf("%d", nestedMapLen(obj, "data"))}
	case "secrets":
		t, _, _ := unstructured.NestedString(obj.Object, "type")
		return map[string]string{"type": t, "keys": fmt.Sprintf("%d", nestedMapLen(obj, "data"))}
	default:
		return nil
	}
}

func summarizePod(obj *unstructured.Unstructured) map[string]string {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	node, _, _ := unstructured.NestedString(obj.Object, "spec", "nodeName")
	podIP, _, _ := unstructured.NestedString(obj.Object, "status", "podIP")

	statuses, _, _ := unstructured.NestedSlice(obj.Object, "status", "containerStatuses")
	ready, restarts := 0, int64(0)
	var waitReason string
	for _, s := range statuses {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if r, ok := sm["ready"].(bool); ok && r {
			ready++
		}
		if rc, ok := sm["restartCount"].(int64); ok {
			restarts += rc
		}
		if state, ok := sm["state"].(map[string]interface{}); ok {
			if waiting, ok := state["waiting"].(map[string]interface{}); ok {
				if reason, ok := waiting["reason"].(string); ok {
					waitReason = reason
				}
			}
		}
	}

	status := phase
	if waitReason != "" {
		status = waitReason // e.g. CrashLoopBackOff is more useful than "Running"
	}
	if deleted := obj.GetDeletionTimestamp(); deleted != nil {
		status = "Terminating"
	}

	return map[string]string{
		"status":   status,
		"ready":    fmt.Sprintf("%d/%d", ready, len(statuses)),
		"restarts": fmt.Sprintf("%d", restarts),
		"node":     node,
		"ip":       podIP,
	}
}

func summarizeWorkload(obj *unstructured.Unstructured) map[string]string {
	desired, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	ready, _, _ := unstructured.NestedInt64(obj.Object, "status", "readyReplicas")
	updated, _, _ := unstructured.NestedInt64(obj.Object, "status", "updatedReplicas")
	available, _, _ := unstructured.NestedInt64(obj.Object, "status", "availableReplicas")
	unavailable, _, _ := unstructured.NestedInt64(obj.Object, "status", "unavailableReplicas")
	m := map[string]string{
		"ready":     fmt.Sprintf("%d/%d", ready, desired),
		"updated":   fmt.Sprintf("%d", updated),
		"available": fmt.Sprintf("%d", available),
	}
	// A deployment can report available == desired (old pods still serving)
	// while a rollout is stuck — the new ReplicaSet has a pod that can't
	// become ready (e.g. ImagePullBackOff). availableReplicas alone would
	// read as healthy and hide that, so surface the incomplete rollout.
	if unavailable > 0 || updated < desired {
		m["rollout"] = fmt.Sprintf("incomplete — %d unavailable", unavailable)
	}
	return m
}

func summarizeDaemonSet(obj *unstructured.Unstructured) map[string]string {
	desired, _, _ := unstructured.NestedInt64(obj.Object, "status", "desiredNumberScheduled")
	ready, _, _ := unstructured.NestedInt64(obj.Object, "status", "numberReady")
	return map[string]string{"ready": fmt.Sprintf("%d/%d", ready, desired)}
}

func summarizeService(obj *unstructured.Unstructured) map[string]string {
	svcType, _, _ := unstructured.NestedString(obj.Object, "spec", "type")
	clusterIP, _, _ := unstructured.NestedString(obj.Object, "spec", "clusterIP")
	ports, _, _ := unstructured.NestedSlice(obj.Object, "spec", "ports")
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		port, _ := pm["port"].(int64)
		proto, _ := pm["protocol"].(string)
		parts = append(parts, fmt.Sprintf("%d/%s", port, proto))
	}
	return map[string]string{
		"type":      svcType,
		"clusterIP": clusterIP,
		"ports":     strings.Join(parts, ", "),
	}
}

func summarizeNode(obj *unstructured.Unstructured) map[string]string {
	status := "NotReady"
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conditions {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cm["type"] == "Ready" && cm["status"] == "True" {
			status = "Ready"
		}
	}

	var roles []string
	for label := range obj.GetLabels() {
		if role, found := strings.CutPrefix(label, "node-role.kubernetes.io/"); found && role != "" {
			roles = append(roles, role)
		}
	}
	version, _, _ := unstructured.NestedString(obj.Object, "status", "nodeInfo", "kubeletVersion")

	return map[string]string{
		"status":  status,
		"roles":   strings.Join(roles, ","),
		"version": version,
	}
}

func summarizeJob(obj *unstructured.Unstructured) map[string]string {
	succeeded, _, _ := unstructured.NestedInt64(obj.Object, "status", "succeeded")
	failed, _, _ := unstructured.NestedInt64(obj.Object, "status", "failed")
	completions, found, _ := unstructured.NestedInt64(obj.Object, "spec", "completions")
	if !found {
		completions = 1
	}
	return map[string]string{
		"completions": fmt.Sprintf("%d/%d", succeeded, completions),
		"failed":      fmt.Sprintf("%d", failed),
	}
}

func summarizeCronJob(obj *unstructured.Unstructured) map[string]string {
	schedule, _, _ := unstructured.NestedString(obj.Object, "spec", "schedule")
	suspend, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend")
	lastRun, _, _ := unstructured.NestedString(obj.Object, "status", "lastScheduleTime")
	return map[string]string{
		"schedule": schedule,
		"suspend":  fmt.Sprintf("%t", suspend),
		"lastRun":  lastRun,
	}
}

func summarizePVC(obj *unstructured.Unstructured) map[string]string {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	capacity, _, _ := unstructured.NestedString(obj.Object, "status", "capacity", "storage")
	storageClass, _, _ := unstructured.NestedString(obj.Object, "spec", "storageClassName")
	return map[string]string{
		"status":   phase,
		"capacity": capacity,
		"class":    storageClass,
	}
}

func nestedMapLen(obj *unstructured.Unstructured, fields ...string) int {
	m, _, _ := unstructured.NestedMap(obj.Object, fields...)
	return len(m)
}
