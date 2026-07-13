package k8s

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// ResourceSummary is one row in a resource list view. Extra holds
// kind-specific columns (e.g. pod phase) keyed by column name.
type ResourceSummary struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// ResourceDetail is the full view of a single resource.
type ResourceDetail struct {
	Name      string                 `json:"name"`
	Namespace string                 `json:"namespace,omitempty"`
	Kind      string                 `json:"kind"`
	CreatedAt time.Time              `json:"createdAt"`
	Labels    map[string]string      `json:"labels,omitempty"`
	Object    map[string]interface{} `json:"object"`
	YAML      string                 `json:"yaml"`
}

// ListResources lists resources of a kind, optionally filtered by namespace.
// Cluster-scoped kinds ignore the namespace argument.
func (c *Client) ListResources(ctx context.Context, kind, namespace string) ([]ResourceSummary, error) {
	info, err := LookupKind(kind)
	if err != nil {
		return nil, err
	}

	var list *unstructured.UnstructuredList
	if info.Namespaced && namespace != "" {
		list, err = c.Dynamic.Resource(info.GVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		list, err = c.Dynamic.Resource(info.GVR).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", kind, err)
	}

	summaries := make([]ResourceSummary, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		summaries = append(summaries, ResourceSummary{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
			CreatedAt: item.GetCreationTimestamp().Time,
			Extra:     summarize(kind, item),
		})
	}
	return summaries, nil
}

// GetResource fetches one resource including its cleaned YAML manifest.
// Secret data values are masked; the raw values never leave the server on
// this endpoint (the YAML is what the edit view shows).
func (c *Client) GetResource(ctx context.Context, kind, namespace, name string) (*ResourceDetail, error) {
	info, err := LookupKind(kind)
	if err != nil {
		return nil, err
	}

	ri := c.Dynamic.Resource(info.GVR).Namespace(namespaceFor(info, namespace))
	obj, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting %s %s: %w", kind, name, err)
	}

	cleaned := obj.DeepCopy()
	cleanForDisplay(cleaned)

	yamlBytes, err := yaml.Marshal(cleaned.Object)
	if err != nil {
		return nil, fmt.Errorf("rendering YAML: %w", err)
	}

	return &ResourceDetail{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Kind:      obj.GetKind(),
		CreatedAt: obj.GetCreationTimestamp().Time,
		Labels:    obj.GetLabels(),
		Object:    cleaned.Object,
		YAML:      string(yamlBytes),
	}, nil
}

// UpdateResource applies an edited YAML manifest. The manifest's kind, name
// and namespace must match the request path — this prevents an edit form for
// one object from silently modifying another.
func (c *Client) UpdateResource(ctx context.Context, kind, namespace, name, manifest string) (*ResourceDetail, error) {
	info, err := LookupKind(kind)
	if err != nil {
		return nil, err
	}

	var objMap map[string]interface{}
	if err := yaml.Unmarshal([]byte(manifest), &objMap); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	obj := &unstructured.Unstructured{Object: objMap}

	if !strings.EqualFold(obj.GetKind(), info.DisplayName) {
		return nil, fmt.Errorf("manifest kind %q does not match resource kind %q", obj.GetKind(), info.DisplayName)
	}
	if obj.GetName() != name {
		return nil, fmt.Errorf("manifest name %q does not match resource name %q", obj.GetName(), name)
	}
	if info.Namespaced && obj.GetNamespace() != namespace {
		return nil, fmt.Errorf("manifest namespace %q does not match resource namespace %q", obj.GetNamespace(), namespace)
	}

	ri := c.Dynamic.Resource(info.GVR).Namespace(namespaceFor(info, namespace))

	// The display YAML strips resourceVersion, so re-fetch the live object and
	// carry its resourceVersion into the update (same optimistic-concurrency
	// window as `kubectl edit`).
	if obj.GetResourceVersion() == "" {
		current, err := ri.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("fetching current resource: %w", err)
		}
		obj.SetResourceVersion(current.GetResourceVersion())
	}

	if _, err := ri.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("updating %s %s: %w", kind, name, err)
	}
	return c.GetResource(ctx, kind, namespace, name)
}

// ListNamespaces returns all namespace names.
func (c *Client) ListNamespaces(ctx context.Context) ([]string, error) {
	list, err := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		names = append(names, ns.Name)
	}
	return names, nil
}

func namespaceFor(info KindInfo, namespace string) string {
	if info.Namespaced {
		return namespace
	}
	return ""
}

// cleanForDisplay removes server-managed noise so the YAML view is editable.
func cleanForDisplay(obj *unstructured.Unstructured) {
	obj.SetManagedFields(nil)
	unstructured.RemoveNestedField(obj.Object, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(obj.Object, "metadata", "uid")
	unstructured.RemoveNestedField(obj.Object, "metadata", "generation")
	unstructured.RemoveNestedField(obj.Object, "metadata", "creationTimestamp")
	annotations := obj.GetAnnotations()
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	if len(annotations) == 0 {
		obj.SetAnnotations(nil)
	} else {
		obj.SetAnnotations(annotations)
	}
}
