package agent

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

// snapshot builds a compact plain-text description of cluster health for the
// review prompt. It intentionally includes only signal (problems, counts) —
// sending every object to the LLM would be slow, expensive and noisy.
func snapshot(ctx context.Context, client *k8s.Client) (string, error) {
	var b strings.Builder

	overview, err := client.GetOverview(ctx)
	if err != nil {
		return "", fmt.Errorf("collecting overview: %w", err)
	}

	fmt.Fprintf(&b, "CLUSTER SNAPSHOT (collected %s)\n\n", overview.CollectedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "Nodes: %d total, %d ready\n", overview.Nodes.Total, overview.Nodes.Ready)
	fmt.Fprintf(&b, "Namespaces: %d\n", overview.Namespaces)
	fmt.Fprintf(&b, "Pods: %d total — running=%d pending=%d succeeded=%d failed=%d unknown=%d\n",
		overview.Pods.Total, overview.Pods.Running, overview.Pods.Pending,
		overview.Pods.Succeeded, overview.Pods.Failed, overview.Pods.Unknown)
	fmt.Fprintf(&b, "Deployments: %d total, %d fully available\n\n",
		overview.Deployments.Total, overview.Deployments.Available)

	// Nodes with problems.
	nodes, err := client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing nodes: %w", err)
	}
	var nodeProblems []string
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			ready := cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue
			pressure := cond.Type != corev1.NodeReady && cond.Status == corev1.ConditionTrue
			if (cond.Type == corev1.NodeReady && !ready) || pressure {
				nodeProblems = append(nodeProblems,
					fmt.Sprintf("- node %s: condition %s=%s (%s)", node.Name, cond.Type, cond.Status, cond.Message))
			}
		}
	}
	writeSection(&b, "NODE PROBLEMS", nodeProblems)

	// Pods that are not healthy: not Running/Succeeded, waiting containers,
	// or high restart counts.
	pods, err := client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing pods: %w", err)
	}
	var podProblems []string
	for _, pod := range pods.Items {
		problems := describePodProblems(&pod)
		if problems != "" {
			podProblems = append(podProblems, fmt.Sprintf("- pod %s/%s: %s", pod.Namespace, pod.Name, problems))
		}
	}
	writeSection(&b, "UNHEALTHY PODS", capList(podProblems, 30))

	// Deployments below desired availability.
	deployments, err := client.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing deployments: %w", err)
	}
	var deployProblems []string
	for _, d := range deployments.Items {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		if d.Status.AvailableReplicas < desired {
			deployProblems = append(deployProblems,
				fmt.Sprintf("- deployment %s/%s: %d/%d replicas available", d.Namespace, d.Name, d.Status.AvailableReplicas, desired))
		}
	}
	writeSection(&b, "DEGRADED DEPLOYMENTS", deployProblems)

	// Recent warning events.
	var warnings []string
	for _, e := range overview.Warnings {
		warnings = append(warnings,
			fmt.Sprintf("- [%s] %s %s: %s (x%d, last %s)", e.Namespace, e.Object, e.Reason, e.Message, e.Count, e.LastSeen.Format("15:04:05")))
	}
	writeSection(&b, "RECENT WARNING EVENTS", capList(warnings, 20))

	return b.String(), nil
}

func describePodProblems(pod *corev1.Pod) string {
	var parts []string
	if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
		parts = append(parts, fmt.Sprintf("phase=%s", pod.Status.Phase))
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			parts = append(parts, fmt.Sprintf("container %s waiting (%s: %s)",
				cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
		}
		if cs.RestartCount >= 3 {
			parts = append(parts, fmt.Sprintf("container %s restarted %d times", cs.Name, cs.RestartCount))
		}
		if pod.Status.Phase == corev1.PodRunning && !cs.Ready && cs.State.Running != nil {
			parts = append(parts, fmt.Sprintf("container %s running but not ready", cs.Name))
		}
	}
	return strings.Join(parts, "; ")
}

func writeSection(b *strings.Builder, title string, lines []string) {
	fmt.Fprintf(b, "%s:\n", title)
	if len(lines) == 0 {
		b.WriteString("(none)\n\n")
		return
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func capList(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	capped := lines[:max]
	capped = append(capped, fmt.Sprintf("... and %d more", len(lines)-max))
	return capped
}
