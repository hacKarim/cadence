package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hacKarim/cadence/internal/spec"
)

func req(id string) Request { return Request{Step: spec.Step{ID: id}} }

func TestFakeReturnsScriptedStdout(t *testing.T) {
	f := NewFake(map[string]FakeResponse{"a": {Stdout: "hello"}})
	res, err := f.Run(context.Background(), req("a"))
	if err != nil || res.Stdout != "hello" {
		t.Fatalf("got %#v, %v", res, err)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := NewFake(nil)
	f.Run(context.Background(), req("a"))
	f.Run(context.Background(), req("b"))
	if got := f.Order(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Order = %v, want [a b]", got)
	}
	if f.CallCount("a") != 1 {
		t.Errorf("CallCount(a) = %d, want 1", f.CallCount("a"))
	}
}

func TestFakeFailsFirstNCalls(t *testing.T) {
	f := NewFake(map[string]FakeResponse{"a": {FailTimes: 2, ExitCode: 7, Stdout: "ok"}})
	for i := 0; i < 2; i++ {
		res, _ := f.Run(context.Background(), req("a"))
		if res.ExitCode != 7 {
			t.Errorf("call %d ExitCode = %d, want 7", i, res.ExitCode)
		}
	}
	res, _ := f.Run(context.Background(), req("a"))
	if res.ExitCode != 0 || res.Stdout != "ok" {
		t.Errorf("third call = %#v, want success", res)
	}
}

func TestFakeTracksPeakConcurrency(t *testing.T) {
	f := NewFake(map[string]FakeResponse{"a": {Delay: 40 * time.Millisecond}})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f.Run(context.Background(), req("a")) }()
	}
	wg.Wait()
	if f.Peak() < 2 {
		t.Errorf("Peak = %d, want at least 2 with five concurrent calls", f.Peak())
	}
}

// The real runners apply Request.Timeout themselves, so the fake must model
// the same contract or engine timeout behaviour goes untested.
func TestFakeHonoursRequestTimeout(t *testing.T) {
	f := NewFake(map[string]FakeResponse{"a": {Delay: 2 * time.Second}})
	r := req("a")
	r.Timeout = 50 * time.Millisecond
	start := time.Now()
	res, err := f.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TimedOut {
		t.Error("TimedOut = false, want true")
	}
	if time.Since(start) > time.Second {
		t.Error("Run waited for the full delay instead of timing out")
	}
}
