package model

import (
	"time"

	"github.com/bmf/links-issue-tracker/internal/lifecycle"
)

type Capabilities struct {
	Status *StatusView `json:"status,omitempty"`
}

type StatusView struct {
	Value    State      `json:"value"`
	Assignee string     `json:"assignee,omitempty"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

func capabilitiesFrom(l lifecycle.Lifecycle) Capabilities {
	statuses := lifecycle.Statuses(l)
	if len(statuses) == 0 {
		return Capabilities{}
	}
	status := statuses[0]
	return Capabilities{Status: &StatusView{
		Value:    State(status.Value),
		Assignee: status.Assignee,
		ClosedAt: cloneTime(status.ClosedAt),
	}}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
