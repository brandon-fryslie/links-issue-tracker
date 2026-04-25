// Package lifecycle defines the internal lifecycle expression primitives used
// by model.Issue. Callers outside internal/model must use the model package
// hydration, capability, and action APIs instead of importing this package.
package lifecycle

import "fmt"

type State string

const (
	Open       State = "open"
	InProgress State = "in_progress"
	Closed     State = "closed"
)

type Progress struct {
	Open       int `json:"open"`
	InProgress int `json:"in_progress"`
	Closed     int `json:"closed"`
	Total      int `json:"total"`
}

type ActionName string

const (
	ActionStart  ActionName = "start"
	ActionDone   ActionName = "done"
	ActionClose  ActionName = "close"
	ActionReopen ActionName = "reopen"
)

type Lifecycle interface {
	State() State
	Progress() Progress
}

// Container marks lifecycle combinators that own child lifecycle expressions.
// [LAW:one-type-per-behavior] Recursive traversal depends on one container contract instead of ad hoc structural assertions per combinator.
type Container interface {
	Lifecycle
	Children() []Lifecycle
}

type Actionable interface {
	Lifecycle
	AvailableActions() []ActionName
	Apply(name ActionName, actor string, reason string) (Lifecycle, error)
}

// Walk visits the lifecycle tree depth-first. Policy decisions such as which
// capabilities or actions an Issue exposes remain root-only in the model
// package; recursive consumers should use Walk deliberately.
// [LAW:dataflow-not-control-flow] Tree traversal is one primitive that receives variable lifecycle data instead of scattering recursive special cases across callers.
func Walk(l Lifecycle, visit func(Lifecycle) bool) {
	if l == nil || !visit(l) {
		return
	}
	if container, ok := l.(Container); ok {
		for _, child := range container.Children() {
			Walk(child, visit)
		}
	}
}

func ParseState(value string) (State, error) {
	switch State(value) {
	case Open, InProgress, Closed:
		return State(value), nil
	default:
		return "", fmt.Errorf("unknown lifecycle state %q", value)
	}
}

func Statuses(l Lifecycle) []OwnedStatus {
	out := []OwnedStatus{}
	Walk(l, func(current Lifecycle) bool {
		if status, ok := current.(OwnedStatus); ok {
			out = append(out, status)
		}
		return true
	})
	return out
}
