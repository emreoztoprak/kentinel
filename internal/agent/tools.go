package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/emreoztoprak/kentinel/internal/k8s"
	"github.com/emreoztoprak/kentinel/internal/llm"
)

// The agent's cluster tools are strictly read-only: list, get, logs, events,
// overview, and (when Prometheus is configured) metrics. In assisted mode one
// more tool — propose_change — is added, but it too performs NO cluster write:
// it only records a proposal in the agent's own database for a human to
// approve. The privileged apply happens in the server, never here.
func queryTools(metricsEnabled, assisted bool) []llm.Tool {
	kinds := make([]string, 0)
	for _, k := range k8s.SupportedKinds() {
		kinds = append(kinds, k.Kind)
	}
	kindList := strings.Join(kinds, ", ")

	tools := []llm.Tool{
		{
			Name:        "get_cluster_overview",
			Description: "Get cluster-wide stats: node/pod/namespace/deployment counts, pod phases, and recent warning events. Use this first for broad questions.",
			Properties:  map[string]interface{}{},
		},
		{
			Name:        "list_resources",
			Description: "List resources of a kind with status columns. Supported kinds: " + kindList + ".",
			Properties: map[string]interface{}{
				"kind":      map[string]interface{}{"type": "string", "description": "resource kind (plural), e.g. pods"},
				"namespace": map[string]interface{}{"type": "string", "description": "namespace filter; empty = all namespaces"},
			},
			Required: []string{"kind"},
		},
		{
			Name:        "get_resource",
			Description: "Get one resource's full manifest as YAML, including status.",
			Properties: map[string]interface{}{
				"kind":      map[string]interface{}{"type": "string", "description": "resource kind (plural), e.g. deployments"},
				"namespace": map[string]interface{}{"type": "string", "description": "namespace (empty for cluster-scoped kinds)"},
				"name":      map[string]interface{}{"type": "string", "description": "resource name"},
			},
			Required: []string{"kind", "name"},
		},
		{
			Name:        "get_pod_logs",
			Description: "Fetch recent logs of a pod container. Use sinceSeconds to bound the time window (e.g. 600 = last 10 minutes).",
			Properties: map[string]interface{}{
				"namespace":    map[string]interface{}{"type": "string", "description": "pod namespace"},
				"pod":          map[string]interface{}{"type": "string", "description": "pod name"},
				"container":    map[string]interface{}{"type": "string", "description": "container name; empty = first container"},
				"tailLines":    map[string]interface{}{"type": "integer", "description": "number of trailing lines (default 200, max 1000)"},
				"sinceSeconds": map[string]interface{}{"type": "integer", "description": "only logs newer than this many seconds"},
				"previous":     map[string]interface{}{"type": "boolean", "description": "logs of the previous (crashed) container instance"},
			},
			Required: []string{"namespace", "pod"},
		},
		{
			Name:        "get_events",
			Description: "List Kubernetes events, newest first. Filter by namespace and/or type (Normal, Warning).",
			Properties: map[string]interface{}{
				"namespace": map[string]interface{}{"type": "string", "description": "namespace filter; empty = all"},
				"type":      map[string]interface{}{"type": "string", "description": "event type filter: Normal or Warning"},
			},
		},
	}

	if assisted {
		tools = append(tools, llm.Tool{
			Name: "propose_change",
			Description: "Propose a fix to ONE resource for the human to review and approve. " +
				"This does NOT apply anything — it queues a proposed change that the user must approve; the server applies it only after approval. " +
				"Use this when the user asks you to fix/change/update something. First call get_resource to read the current manifest, then supply the full modified manifest as proposedYaml. Only propose changes you are confident are correct and minimal.",
			Properties: map[string]interface{}{
				"kind":         map[string]interface{}{"type": "string", "description": "resource kind (plural), e.g. deployments"},
				"namespace":    map[string]interface{}{"type": "string", "description": "namespace (empty for cluster-scoped kinds)"},
				"name":         map[string]interface{}{"type": "string", "description": "resource name"},
				"proposedYaml": map[string]interface{}{"type": "string", "description": "the FULL modified manifest as YAML (not a patch). Its kind/name/namespace must match the target."},
				"rationale":    map[string]interface{}{"type": "string", "description": "a one or two sentence explanation of what this change does and why"},
			},
			Required: []string{"kind", "name", "proposedYaml", "rationale"},
		})
	}

	if metricsEnabled {
		tools = append(tools,
			llm.Tool{
				Name:        "get_resource_usage",
				Description: "Get actual CPU/memory usage: top pods by memory and CPU, node usage, and CPU-throttled containers. Use this for performance questions, sizing checks, and to distinguish memory leaks from undersized limits.",
				Properties: map[string]interface{}{
					"namespace": map[string]interface{}{"type": "string", "description": "namespace filter; empty = whole cluster"},
				},
			},
			llm.Tool{
				Name:        "query_metrics",
				Description: "Run a PromQL instant query against Prometheus. Use rate()/avg_over_time()/increase() with a time window for trends (e.g. rate(pod_cpu_usage_seconds_total{namespace=\"app\"}[5m])). Available metrics include pod_memory_working_set_bytes, pod_cpu_usage_seconds_total, node_memory_working_set_bytes, node_cpu_usage_seconds_total, container_cpu_cfs_throttled_periods_total, machine_memory_bytes.",
				Properties: map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "the PromQL expression"},
				},
				Required: []string{"query"},
			},
		)
	}
	return tools
}

const (
	maxToolLogLines  = 1000
	maxToolResultLen = 30000 // characters per tool result fed back to the model
)

// runTool executes one tool call. All cluster tools are read-only; the
// metrics tools hit Prometheus (prom may be nil). propose_change writes ONLY
// to the agent's own store (never the cluster) and requires a non-nil store.
func runTool(ctx context.Context, client *k8s.Client, prom *promClient, store *Store, call llm.ToolCall) (string, error) {
	var args struct {
		Kind         string `json:"kind"`
		Namespace    string `json:"namespace"`
		Name         string `json:"name"`
		Pod          string `json:"pod"`
		Container    string `json:"container"`
		TailLines    int64  `json:"tailLines"`
		SinceSeconds int64  `json:"sinceSeconds"`
		Previous     bool   `json:"previous"`
		Type         string `json:"type"`
		Query        string `json:"query"`
		ProposedYAML string `json:"proposedYaml"`
		Rationale    string `json:"rationale"`
	}
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &args); err != nil {
			return "", fmt.Errorf("invalid tool arguments: %w", err)
		}
	}

	switch call.Name {
	case "get_cluster_overview":
		overview, err := client.GetOverview(ctx)
		if err != nil {
			return "", err
		}
		return marshalJSON(overview)

	case "list_resources":
		items, err := client.ListResources(ctx, args.Kind, args.Namespace)
		if err != nil {
			return "", err
		}
		return marshalJSON(items)

	case "get_resource":
		detail, err := client.GetResource(ctx, args.Kind, args.Namespace, args.Name)
		if err != nil {
			return "", err
		}
		return capString(detail.YAML), nil

	case "get_pod_logs":
		tail := args.TailLines
		if tail <= 0 {
			tail = 200
		}
		if tail > maxToolLogLines {
			tail = maxToolLogLines
		}
		stream, err := client.StreamLogs(ctx, args.Namespace, args.Pod, k8s.LogOptions{
			Container:    args.Container,
			TailLines:    tail,
			SinceSeconds: args.SinceSeconds,
			Previous:     args.Previous,
		})
		if err != nil {
			return "", err
		}
		defer stream.Close()
		data, err := io.ReadAll(io.LimitReader(stream, 1<<20))
		if err != nil {
			return "", fmt.Errorf("reading logs: %w", err)
		}
		if len(data) == 0 {
			return "(no log output in the requested window)", nil
		}
		return capString(string(data)), nil

	case "get_events":
		events, err := client.ListEvents(ctx, args.Namespace, args.Type)
		if err != nil {
			return "", err
		}
		if len(events) > 50 {
			events = events[:50]
		}
		return marshalJSON(events)

	case "get_resource_usage":
		if prom == nil {
			return "", fmt.Errorf("metrics are not configured (set a Prometheus URL in Settings)")
		}
		report, err := resourceUsageReport(ctx, prom, args.Namespace)
		if err != nil {
			return "", err
		}
		return capString(report), nil

	case "query_metrics":
		if prom == nil {
			return "", fmt.Errorf("metrics are not configured (set a Prometheus URL in Settings)")
		}
		if args.Query == "" {
			return "", fmt.Errorf("query is required")
		}
		samples, err := prom.Query(ctx, args.Query)
		if err != nil {
			return "", err
		}
		return capString(renderSamples(samples, 50)), nil

	case "propose_change":
		return proposeChange(ctx, client, store, args.Kind, args.Namespace, args.Name, args.ProposedYAML, args.Rationale)

	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

// proposeChange records a remediation proposal for human approval. It makes
// NO change to the cluster — it snapshots the current manifest (read-only),
// validates the proposed one, and stores a pending proposal. Returns a short
// confirmation for the model to relay to the user.
func proposeChange(ctx context.Context, client *k8s.Client, store *Store, kind, namespace, name, proposedYAML, rationale string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("proposals are unavailable (no persistent store)")
	}
	if proposedYAML == "" {
		return "", fmt.Errorf("proposedYaml is required")
	}
	// Validate the proposed manifest parses and its identity matches the
	// target. The server re-validates this at apply time (authoritative
	// guard in k8s.UpdateResource); this is early feedback for the model.
	if err := k8s.ValidateManifestTarget(kind, namespace, name, proposedYAML); err != nil {
		return "", err
	}
	// Snapshot the current manifest for the approval diff (read-only).
	current, err := client.GetResource(ctx, kind, namespace, name)
	if err != nil {
		return "", fmt.Errorf("reading current %s %s: %w", kind, name, err)
	}

	p, err := store.SaveProposal(Proposal{
		Kind: kind, Namespace: namespace, Name: name,
		Rationale:    rationale,
		CurrentYAML:  current.YAML,
		ProposedYAML: proposedYAML,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Proposed a change to %s %s/%s (proposal %s). It is now pending the user's approval — it has NOT been applied. Tell the user to review and approve it in the Pending Changes panel.",
		kind, namespace, name, p.ID), nil
}

func marshalJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encoding tool result: %w", err)
	}
	return capString(string(data)), nil
}

func capString(s string) string {
	if len(s) <= maxToolResultLen {
		return s
	}
	return s[:maxToolResultLen] + "\n... (truncated)"
}
