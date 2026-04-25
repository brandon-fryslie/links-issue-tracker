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
	if container, ok := l.(interface{ Children() []Lifecycle }); ok {
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

func Actionables(l Lifecycle) []Actionable {
	out := []Actionable{}
	Walk(l, func(current Lifecycle) bool {
		if actionable, ok := current.(Actionable); ok {
			out = append(out, actionable)
		}
		return true
	})
	return out
}
