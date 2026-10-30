// Package notify posts a finished run to Slack or Discord. It is engine level
// rather than a step type because a failed run has no step left to send it.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hacKarim/cadence/internal/spec"
	"github.com/hacKarim/cadence/internal/store"
)

// Client is the HTTP client used for webhooks.
var Client = &http.Client{Timeout: 10 * time.Second}

// Send posts the run outcome to every configured destination, if the run's
// state is one the workflow asked to hear about.
func Send(ctx context.Context, ns *spec.NotifySpec, run store.Run, tree *store.RunTree) error {
	if ns == nil || !wants(ns, run.State) {
		return nil
	}
	text := summary(run, tree)

	var errs []string
	if ns.Slack != "" {
		if err := post(ctx, ns.Slack, map[string]any{"text": text}); err != nil {
			errs = append(errs, "slack: "+err.Error())
		}
	}
	if ns.Discord != "" {
		if err := post(ctx, ns.Discord, map[string]any{"content": text}); err != nil {
			errs = append(errs, "discord: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("notify: %s", strings.Join(errs, "; "))
	}
	return nil
}

func wants(ns *spec.NotifySpec, state store.State) bool {
	want := spec.NotifyFailure
	if state == store.StateSucceeded {
		want = spec.NotifySuccess
	}
	for _, e := range ns.On {
		if e == want {
			return true
		}
	}
	return false
}

// summary describes the run in a few lines. It reports states and step names
// only: nothing a step was given in its environment reaches a third party.
func summary(run store.Run, tree *store.RunTree) string {
	var b strings.Builder
	took := ""
	if run.EndedAt != nil {
		took = " in " + run.EndedAt.Sub(run.StartedAt).Round(time.Second).String()
	}
	fmt.Fprintf(&b, "cadence: %s run %d %s%s", run.Workflow, run.ID, run.State, took)
	if run.Error != "" {
		fmt.Fprintf(&b, "\n%s", run.Error)
	}
	if tree != nil {
		for _, s := range tree.Steps {
			if s.State == store.StateFailed {
				fmt.Fprintf(&b, "\n  %s: %s", s.StepID, firstLine(s.Error))
			}
		}
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func post(ctx context.Context, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}
