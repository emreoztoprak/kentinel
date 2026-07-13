package k8s

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LogOptions controls a pod log request.
type LogOptions struct {
	Container    string
	Follow       bool
	TailLines    int64 // <= 0 means server default handled by caller
	SinceSeconds int64 // <= 0 means from the beginning
	Previous     bool
}

// maxTailLines caps a single log fetch so one request can't pull an entire
// multi-GB log into memory (also applied to agent tool calls).
const maxTailLines = 5000

// StreamLogs opens a pod log stream. The caller must Close the reader.
func (c *Client) StreamLogs(ctx context.Context, namespace, pod string, opts LogOptions) (io.ReadCloser, error) {
	podOpts := &corev1.PodLogOptions{
		Container: opts.Container,
		Follow:    opts.Follow,
		Previous:  opts.Previous,
	}
	if opts.TailLines > 0 {
		tail := min(opts.TailLines, maxTailLines)
		podOpts.TailLines = &tail
	}
	if opts.SinceSeconds > 0 {
		podOpts.SinceSeconds = &opts.SinceSeconds
	}

	stream, err := c.Clientset.CoreV1().Pods(namespace).GetLogs(pod, podOpts).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening log stream for %s/%s: %w", namespace, pod, err)
	}
	return stream, nil
}

// PodContainers returns the container names of a pod (init containers last).
func (c *Client) PodContainers(ctx context.Context, namespace, pod string) ([]string, error) {
	p, err := c.Clientset.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting pod %s/%s: %w", namespace, pod, err)
	}
	names := make([]string, 0, len(p.Spec.Containers)+len(p.Spec.InitContainers))
	for _, ct := range p.Spec.Containers {
		names = append(names, ct.Name)
	}
	for _, ct := range p.Spec.InitContainers {
		names = append(names, ct.Name)
	}
	return names, nil
}
