package store

// State is the lifecycle position of a run, a step within a run, or a single
// fan-out item. One vocabulary is used at all three levels so the TUI and the
// CLI can render them identically.
type State string

// The states a run, step or item can be in.
const (
	StatePending   State = "pending"
	StateReady     State = "ready"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateSkipped   State = "skipped"
	StateCached    State = "cached"
	StateCancelled State = "cancelled"
)

// AllStates lists every state, for exhaustive tests and for display code.
func AllStates() []State {
	return []State{
		StatePending, StateReady, StateRunning, StateSucceeded,
		StateFailed, StateSkipped, StateCached, StateCancelled,
	}
}

// Terminal reports whether no further transition is expected.
func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateSkipped, StateCached, StateCancelled:
		return true
	}
	return false
}

// OK reports whether the state counts as a success for dependency purposes.
// A skipped step unlocks its dependents; a failed one does not.
func (s State) OK() bool {
	switch s {
	case StateSucceeded, StateCached, StateSkipped:
		return true
	}
	return false
}
