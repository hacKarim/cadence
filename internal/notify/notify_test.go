package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

type capture struct {
	mu     sync.Mutex
	bodies []string
}

func (c *capture) server(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(b))
		c.mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(s.Close)
	return s
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func run(state store.State) store.Run {
	end := time.Now()
	return store.Run{ID: 3, Workflow: "research-funnel", State: state, StartedAt: time.Now(), EndedAt: &end}
}

func TestNotifyOnFailureSends(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	ns := &spec.NotifySpec{On: []spec.NotifyEvent{spec.NotifyFailure}, Slack: srv.URL}
	if err := Send(context.Background(), ns, run(store.StateFailed), nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if c.count() != 1 {
		t.Fatalf("sent %d notifications, want 1", c.count())
	}
	if !strings.Contains(c.bodies[0], "research-funnel") {
		t.Errorf("payload does not name the workflow: %s", c.bodies[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(c.bodies[0]), &payload); err != nil {
		t.Errorf("payload is not JSON: %v", err)
	}
}

func TestNotifyOnSuccessNotSentWhenOnlyFailureRequested(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	ns := &spec.NotifySpec{On: []spec.NotifyEvent{spec.NotifyFailure}, Slack: srv.URL}
	if err := Send(context.Background(), ns, run(store.StateSucceeded), nil); err != nil {
		t.Fatal(err)
	}
	if c.count() != 0 {
		t.Errorf("sent %d notifications for a success, want 0", c.count())
	}
}

func TestNotifySendsToBothDestinations(t *testing.T) {
	a, b := &capture{}, &capture{}
	ns := &spec.NotifySpec{
		On:      []spec.NotifyEvent{spec.NotifySuccess},
		Slack:   a.server(t).URL,
		Discord: b.server(t).URL,
	}
	if err := Send(context.Background(), ns, run(store.StateSucceeded), nil); err != nil {
		t.Fatal(err)
	}
	if a.count() != 1 || b.count() != 1 {
		t.Errorf("slack %d, discord %d; want one each", a.count(), b.count())
	}
}

func TestNilSpecIsNoOp(t *testing.T) {
	if err := Send(context.Background(), nil, run(store.StateFailed), nil); err != nil {
		t.Errorf("Send with no notify block returned %v", err)
	}
}

// A notification leaves the machine. Whatever a step was given in its
// environment must not ride along with it.
func TestNotifyPayloadCarriesNoStepEnvironment(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	tree := &store.RunTree{
		Run: run(store.StateFailed),
		Steps: []store.StepRun{{
			StepID: "analyse", State: store.StateFailed,
			Items: []store.StepItem{{Index: 0, State: store.StateFailed, Error: "exit status 1"}},
		}},
	}
	ns := &spec.NotifySpec{On: []spec.NotifyEvent{spec.NotifyFailure}, Slack: srv.URL}
	if err := Send(context.Background(), ns, tree.Run, tree); err != nil {
		t.Fatal(err)
	}
	body := c.bodies[0]
	for _, secret := range []string{"hunter2", "AWS_SECRET", "ANTHROPIC_API_KEY", "sk-"} {
		if strings.Contains(body, secret) {
			t.Errorf("payload contains %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, "analyse") {
		t.Errorf("payload should name the failing step: %s", body)
	}
}

func TestNotifyReportsTransportFailure(t *testing.T) {
	ns := &spec.NotifySpec{On: []spec.NotifyEvent{spec.NotifyFailure}, Slack: "http://127.0.0.1:1/nope"}
	if err := Send(context.Background(), ns, run(store.StateFailed), nil); err == nil {
		t.Error("Send reported success although the webhook was unreachable")
	}
}
