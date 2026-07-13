package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"

	"github.com/emreoztoprak/kentinel/internal/k8s"
)

// newTestAPI seeds a fake cluster and returns an httptest server running the
// full router — these tests exercise the real REST contract.
func newTestAPI(t *testing.T) *httptest.Server {
	t.Helper()

	replicas := int32(2)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app"},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.9",
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "web", Ready: true, RestartCount: 2,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "app"},
		Data:       map[string]string{"color": "blue"},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app"}}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: 1, ReadyReplicas: 1, UpdatedReplicas: 2},
	}
	event := &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "web-1.warn", Namespace: "app"},
		Type:           "Warning",
		Reason:         "BackOff",
		Message:        "Back-off restarting failed container",
		Count:          3,
		LastTimestamp:  metav1.Time{Time: time.Now()},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-1"},
	}

	clientset := kfake.NewSimpleClientset(pod, configMap, node, namespace, deployment, event)
	dyn := dynfake.NewSimpleDynamicClient(clientscheme.Scheme, pod, configMap, node, deployment)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(&k8s.Client{Clientset: clientset, Dynamic: dyn}, "http://127.0.0.1:1", "", log)

	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts
}

func getJSON(t *testing.T, ts *httptest.Server, path string, wantStatus int) map[string]interface{} {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	if res.StatusCode != wantStatus {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("GET %s: status %d, want %d — body: %s", path, res.StatusCode, wantStatus, body)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: decoding: %v", path, err)
	}
	return out
}

func TestOverview(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/overview", http.StatusOK)

	nodes := out["nodes"].(map[string]interface{})
	if nodes["total"].(float64) != 1 || nodes["ready"].(float64) != 1 {
		t.Errorf("nodes = %v", nodes)
	}
	pods := out["pods"].(map[string]interface{})
	if pods["running"].(float64) != 1 {
		t.Errorf("pods = %v", pods)
	}
	deployments := out["deployments"].(map[string]interface{})
	if deployments["total"].(float64) != 1 || deployments["available"].(float64) != 0 {
		t.Errorf("deployments = %v (1/2 replicas available must not count as available)", deployments)
	}
	warnings := out["warnings"].([]interface{})
	if len(warnings) != 1 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestListResourcesWithSummary(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/resources/pods/?namespace=app", http.StatusOK)

	items := out["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	pod := items[0].(map[string]interface{})
	extra := pod["extra"].(map[string]interface{})
	if pod["name"] != "web-1" || extra["status"] != "Running" || extra["restarts"] != "2" {
		t.Errorf("unexpected pod summary: %v", pod)
	}
}

func TestUnsupportedKindIs400(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/resources/widgets/", http.StatusBadRequest)
	if out["error"] != "bad_request" {
		t.Errorf("error envelope = %v", out)
	}
}

func TestGetResourceYAMLIsCleaned(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/resources/configmaps/app/demo", http.StatusOK)

	yaml := out["yaml"].(string)
	if !strings.Contains(yaml, "color: blue") {
		t.Errorf("yaml missing data:\n%s", yaml)
	}
	for _, forbidden := range []string{"managedFields", "uid:", "resourceVersion", "creationTimestamp"} {
		if strings.Contains(yaml, forbidden) {
			t.Errorf("yaml must not contain %q:\n%s", forbidden, yaml)
		}
	}
}

func TestGetMissingResourceIs404(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/resources/configmaps/app/nope", http.StatusNotFound)
	if out["error"] != "not_found" {
		t.Errorf("error envelope = %v", out)
	}
}

func putJSON(t *testing.T, ts *httptest.Server, path string, payload interface{}) *http.Response {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestUpdateResourceAppliesYAML(t *testing.T) {
	ts := newTestAPI(t)

	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n  namespace: app\ndata:\n  color: red\n"
	res := putJSON(t, ts, "/api/v1/resources/configmaps/app/demo", map[string]string{"yaml": manifest})
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("update: status %d — %s", res.StatusCode, body)
	}

	out := getJSON(t, ts, "/api/v1/resources/configmaps/app/demo", http.StatusOK)
	if !strings.Contains(out["yaml"].(string), "color: red") {
		t.Errorf("update not applied:\n%s", out["yaml"])
	}
}

func TestUpdateResourceRejectsMismatchedManifest(t *testing.T) {
	ts := newTestAPI(t)

	cases := map[string]string{
		"wrong name":      "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: OTHER\n  namespace: app\n",
		"wrong kind":      "apiVersion: v1\nkind: Secret\nmetadata:\n  name: demo\n  namespace: app\n",
		"wrong namespace": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n  namespace: elsewhere\n",
	}
	for name, manifest := range cases {
		res := putJSON(t, ts, "/api/v1/resources/configmaps/app/demo", map[string]string{"yaml": manifest})
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, res.StatusCode)
		}
	}

	// Empty body and missing yaml field are also 400s.
	res := putJSON(t, ts, "/api/v1/resources/configmaps/app/demo", map[string]string{})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty yaml: status %d, want 400", res.StatusCode)
	}
}

func TestNamespacesAndKinds(t *testing.T) {
	ts := newTestAPI(t)

	out := getJSON(t, ts, "/api/v1/namespaces", http.StatusOK)
	namespaces := out["namespaces"].([]interface{})
	if len(namespaces) != 1 || namespaces[0] != "app" {
		t.Errorf("namespaces = %v", namespaces)
	}

	out = getJSON(t, ts, "/api/v1/kinds", http.StatusOK)
	if len(out["kinds"].([]interface{})) < 10 {
		t.Errorf("kinds = %v", out["kinds"])
	}
}

func TestEvents(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/events?namespace=app", http.StatusOK)
	events := out["events"].([]interface{})
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	ev := events[0].(map[string]interface{})
	if ev["reason"] != "BackOff" || ev["object"] != "Pod/web-1" {
		t.Errorf("event = %v", ev)
	}
}

func TestAgentProxyUnreachableIs502(t *testing.T) {
	ts := newTestAPI(t)
	out := getJSON(t, ts, "/api/v1/agent/status", http.StatusBadGateway)
	if out["error"] != "agent_unavailable" {
		t.Errorf("error envelope = %v", out)
	}
}
