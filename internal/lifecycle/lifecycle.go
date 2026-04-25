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

func ParseState(value string) (State, error) {
	switch State(value) {
	case Open, InProgress, Closed:
		return State(value), nil
	default:
		return "", fmt.Errorf("unknown lifecycle state %q", value)
	}
}

func Statuses(l Lifecycle) []OwnedStatus {
	switch typed := l.(type) {
	case nil:
		return nil
	case OwnedStatus:
		return []OwnedStatus{typed}
	case AllOf:
		return nil
	default:
		return nil
	}
}

func Actionables(l Lifecycle) []Actionable {
	switch typed := l.(type) {
	case nil:
		return nil
	case AllOf:
		return nil
	case Actionable:
		return []Actionable{typed}
	default:
		return nil
	}
}
