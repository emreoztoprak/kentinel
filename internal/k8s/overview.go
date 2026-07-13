package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Overview is the home-screen cluster summary.
type Overview struct {
	Nodes       NodeStats      `json:"nodes"`
	Pods        PodStats       `json:"pods"`
	Namespaces  int            `json:"namespaces"`
	Deployments WorkloadStats  `json:"deployments"`
	Warnings    []EventSummary `json:"warnings"`
	CollectedAt time.Time      `json:"collectedAt"`
}

type NodeStats struct {
	Total int `json:"total"`
	Ready int `json:"ready"`
}

type PodStats struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Unknown   int `json:"unknown"`
}

type WorkloadStats struct {
	Total     int `json:"total"`
	Available int `json:"available"`
}

// EventSummary is a trimmed cluster event for lists and the dashboard.
type EventSummary struct {
	Namespace string    `json:"namespace"`
	Type      string    `json:"type"`
	Reason    string    `json:"reason"`
	Object    string    `json:"object"`
	Message   string    `json:"message"`
	Count     int32     `json:"count"`
	LastSeen  time.Time `json:"lastSeen"`
}

// GetOverview aggregates the cluster stats shown on the dashboard.
func (c *Client) GetOverview(ctx context.Context) (*Overview, error) {
	overview := &Overview{CollectedAt: time.Now().UTC()}

	nodes, err := c.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	overview.Nodes.Total = len(nodes.Items)
	for _, node := range nodes.Items {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				overview.Nodes.Ready++
			}
		}
	}

	pods, err := c.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}
	overview.Pods.Total = len(pods.Items)
	for _, pod := range pods.Items {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			overview.Pods.Running++
		case corev1.PodPending:
			overview.Pods.Pending++
		case corev1.PodSucceeded:
			overview.Pods.Succeeded++
		case corev1.PodFailed:
			overview.Pods.Failed++
		default:
			overview.Pods.Unknown++
		}
	}

	namespaces, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}
	overview.Namespaces = len(namespaces.Items)

	deployments, err := c.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	overview.Deployments.Total = len(deployments.Items)
	for _, d := range deployments.Items {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		if d.Status.AvailableReplicas >= desired {
			overview.Deployments.Available++
		}
	}

	warnings, err := c.ListEvents(ctx, "", "Warning")
	if err != nil {
		return nil, err
	}
	if len(warnings) > 10 {
		warnings = warnings[:10]
	}
	overview.Warnings = warnings

	return overview, nil
}

// ListEvents returns cluster events, newest first, optionally filtered by
// namespace and event type ("Normal"/"Warning").
func (c *Client) ListEvents(ctx context.Context, namespace, eventType string) ([]EventSummary, error) {
	opts := metav1.ListOptions{}
	if eventType != "" {
		opts.FieldSelector = "type=" + eventType
	}
	list, err := c.Clientset.CoreV1().Events(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	events := make([]EventSummary, 0, len(list.Items))
	for _, e := range list.Items {
		lastSeen := e.LastTimestamp.Time
		if lastSeen.IsZero() && e.Series != nil {
			lastSeen = e.Series.LastObservedTime.Time
		}
		if lastSeen.IsZero() {
			lastSeen = e.EventTime.Time
		}
		if lastSeen.IsZero() {
			lastSeen = e.CreationTimestamp.Time
		}
		events = append(events, EventSummary{
			Namespace: e.Namespace,
			Type:      e.Type,
			Reason:    e.Reason,
			Object:    fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
			Message:   e.Message,
			Count:     e.Count,
			LastSeen:  lastSeen,
		})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].LastSeen.After(events[j].LastSeen) })
	return events, nil
}
