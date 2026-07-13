package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// promClient is a tiny Prometheus HTTP API client (instant queries only —
// PromQL's own lookbehind functions like rate()/avg_over_time() cover the
// trend use cases the agent needs).
type promClient struct {
	baseURL string
	client  *http.Client
}

func newPromClient(baseURL string) *promClient {
	return &promClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// promSample is one time series result of an instant query.
type promSample struct {
	Labels map[string]string
	Value  float64
}

// Query runs a PromQL instant query.
func (p *promClient) Query(ctx context.Context, promql string) ([]promSample, error) {
	endpoint := p.baseURL + "/api/v1/query?query=" + url.QueryEscape(promql)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus request to %s failed (is Prometheus running?): %w", p.baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("prometheus: reading response: %w", err)
	}

	var parsed struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []interface{}     `json:"value"` // [ts, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("prometheus: HTTP %d, undecodable response: %s", resp.StatusCode, truncateRunes(string(raw), 300))
	}
	if parsed.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", parsed.Error)
	}
	if parsed.Data.ResultType != "vector" {
		return nil, fmt.Errorf("prometheus: unsupported result type %q (only instant vector queries are supported; use rate()/avg_over_time() for windows)", parsed.Data.ResultType)
	}

	samples := make([]promSample, 0, len(parsed.Data.Result))
	for _, r := range parsed.Data.Result {
		if len(r.Value) != 2 {
			continue
		}
		str, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			continue
		}
		samples = append(samples, promSample{Labels: r.Metric, Value: v})
	}
	return samples, nil
}

// Healthy checks connectivity (used by the Settings test button).
func (p *promClient) Healthy(ctx context.Context) error {
	_, err := p.Query(ctx, "up")
	return err
}

// renderSamples formats query results compactly for the LLM. Series are
// sorted by value (descending) and capped.
func renderSamples(samples []promSample, maxSeries int) string {
	if len(samples) == 0 {
		return "(no series matched)"
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Value > samples[j].Value })

	var b strings.Builder
	for i, s := range samples {
		if i == maxSeries {
			fmt.Fprintf(&b, "... and %d more series\n", len(samples)-maxSeries)
			break
		}
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			if k == "__name__" || k == "job" || k == "instance" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+s.Labels[k])
		}
		name := s.Labels["__name__"]
		fmt.Fprintf(&b, "%s{%s} = %s\n", name, strings.Join(parts, ","), formatValue(name, s.Value))
	}
	return b.String()
}

// formatValue renders bytes-like metrics human-readably; everything else
// with reasonable precision.
func formatValue(metric string, v float64) string {
	if strings.Contains(metric, "bytes") {
		return formatBytes(v)
	}
	return strconv.FormatFloat(v, 'g', 4, 64)
}

func formatBytes(v float64) string {
	const unit = 1024.0
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f%s", v, units[i])
}

// --- canned queries used by get_resource_usage and the review snapshot ---

const (
	// kubelet resource metrics (scraped by the bundled Prometheus config)
	qTopPodMemory = `topk(%d, sum by (namespace, pod) (pod_memory_working_set_bytes%s))`
	qTopPodCPU    = `topk(%d, sum by (namespace, pod) (rate(pod_cpu_usage_seconds_total%s[5m])))`
	qNodeMemory   = `node_memory_working_set_bytes`
	qNodeCPU      = `rate(node_cpu_usage_seconds_total[5m])`
	// cAdvisor: fraction of CPU periods throttled per container
	qThrottled = `sum by (namespace, pod, container) (rate(container_cpu_cfs_throttled_periods_total[10m])) / sum by (namespace, pod, container) (rate(container_cpu_cfs_periods_total[10m])) > 0.25`
)

// resourceUsageReport builds the canned usage overview (top consumers,
// node usage, throttled containers) for a namespace or the whole cluster.
func resourceUsageReport(ctx context.Context, prom *promClient, namespace string) (string, error) {
	selector := ""
	if namespace != "" {
		selector = fmt.Sprintf(`{namespace=%q}`, namespace)
	}

	var b strings.Builder
	sections := []struct {
		title string
		query string
	}{
		{"TOP PODS BY MEMORY (working set)", fmt.Sprintf(qTopPodMemory, 15, selector)},
		{"TOP PODS BY CPU (cores, 5m rate)", fmt.Sprintf(qTopPodCPU, 15, selector)},
		{"NODE MEMORY USAGE", qNodeMemory},
		{"NODE CPU USAGE (cores, 5m rate)", qNodeCPU},
		{"CPU-THROTTLED CONTAINERS (>25% of periods, 10m)", qThrottled},
	}
	for _, section := range sections {
		samples, err := prom.Query(ctx, section.query)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s:\n%s\n", section.title, renderSamples(samples, 15))
	}
	b.WriteString("Note: compare usage against the requests/limits in the pod manifests (get_resource) to judge sizing.\n")
	return b.String(), nil
}

// metricsSnapshot returns a compact metrics section for the review prompt,
// or a short note when metrics are unavailable. Never fails the review.
func metricsSnapshot(ctx context.Context, prom *promClient) string {
	snapCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var b strings.Builder
	sections := []struct {
		title string
		query string
	}{
		{"NODE MEMORY USAGE", qNodeMemory},
		{"TOP 8 PODS BY MEMORY", fmt.Sprintf(qTopPodMemory, 8, "")},
		{"CPU-THROTTLED CONTAINERS (>25% of periods)", qThrottled},
	}
	for _, section := range sections {
		samples, err := prom.Query(snapCtx, section.query)
		if err != nil {
			return fmt.Sprintf("RESOURCE METRICS: unavailable (%s)\n\n", err)
		}
		fmt.Fprintf(&b, "%s:\n%s\n", section.title, renderSamples(samples, 8))
	}
	return b.String()
}
