package k8s

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecOptions describes an interactive exec session into a pod container.
type ExecOptions struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	TTY       bool
	Resize    remotecommand.TerminalSizeQueue
}

// ValidateExecTarget checks that the pod exists, is running, and the target
// container is actually running — the kubelet's own errors for these cases
// ("container not found") are too cryptic to show users.
func (c *Client) ValidateExecTarget(ctx context.Context, namespace, pod, container string) error {
	p, err := c.Clientset.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting pod %s/%s: %w", namespace, pod, err)
	}

	if p.Status.Phase != corev1.PodRunning {
		return fmt.Errorf(
			"cannot open a terminal: pod is %s — a terminal needs a running container (crashed, completed, and pending pods cannot be exec'd into)",
			p.Status.Phase)
	}

	for _, cs := range p.Status.ContainerStatuses {
		if container != "" && cs.Name != container {
			continue
		}
		if cs.State.Running != nil {
			return nil
		}
		reason := "not running"
		if cs.State.Waiting != nil {
			reason = cs.State.Waiting.Reason
		} else if cs.State.Terminated != nil {
			reason = "terminated (" + cs.State.Terminated.Reason + ")"
		}
		return fmt.Errorf("cannot open a terminal: container %q is %s", cs.Name, reason)
	}
	return fmt.Errorf("cannot open a terminal: container %q not found in pod %s/%s", container, namespace, pod)
}

// Exec runs a command in a pod container, streaming stdin/stdout until the
// command exits or ctx is cancelled.
func (c *Client) Exec(ctx context.Context, opts ExecOptions) error {
	req := c.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(opts.Namespace).
		Name(opts.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: opts.Container,
			Command:   opts.Command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.RestConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating exec executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             opts.Stdin,
		Stdout:            opts.Stdout,
		Stderr:            opts.Stderr,
		Tty:               opts.TTY,
		TerminalSizeQueue: opts.Resize,
	})
	if err != nil {
		return fmt.Errorf("exec stream: %w", err)
	}
	return nil
}
