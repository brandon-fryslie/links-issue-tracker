package annotation

import (
	"context"
	"encoding/json"

	"github.com/bmf/links-issue-tracker/internal/model"
)

// Kind identifies a category of annotation. The unexported key field prevents
// construction outside this package — only the package-level vars are valid kinds.
// [LAW:single-enforcer] New annotation types are defined here and nowhere else.
type Kind struct {
	key string
}

// String returns the serialization key for this kind.
func (k Kind) String() string { return k.key }

// MarshalJSON serializes the kind as a JSON string.
func (k Kind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.key)
}

// UnmarshalJSON deserializes a JSON string into a Kind.
func (k *Kind) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &k.key)
}

// Registry — closed set of annotation kinds.
var (
	MissingField = Kind{key: "missing_field"} // a required field is empty or unset
	BlockedBy    = Kind{key: "blocked_by"}    // issue depends on an open ticket
)

// Annotation is a computed fact about an issue.
type Annotation struct {
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
}

// AnnotatedIssue pairs an issue with its computed annotations.
// [LAW:one-type-per-behavior] All issues flow through this single type regardless
// of what annotations they carry. Consumers interpret annotations via predicates.
type AnnotatedIssue struct {
	model.Issue
	Annotations []Annotation `json:"annotations"`
}

// Annotator computes annotations for a single issue.
type Annotator func(ctx context.Context, issue model.Issue) ([]Annotation, error)

// Annotate applies all annotators to every issue unconditionally.
// [LAW:dataflow-not-control-flow] Every issue flows through every annotator.
// Variability is in the annotation values, not in whether annotators execute.
func Annotate(ctx context.Context, issues []model.Issue, annotators ...Annotator) ([]AnnotatedIssue, error) {
	result := make([]AnnotatedIssue, len(issues))
	for i, issue := range issues {
		var all []Annotation
		for _, annotator := range annotators {
			annotations, err := annotator(ctx, issue)
			if err != nil {
				return nil, err
			}
			all = append(all, annotations...)
		}
		if all == nil {
			all = []Annotation{}
		}
		result[i] = AnnotatedIssue{
			Issue:       issue,
			Annotations: all,
		}
	}
	return result, nil
}

// HasAny returns true if any annotation has a kind matching one of the given kinds.
// This is a neutral utility — the caller decides which kinds matter and why.
func HasAny(annotations []Annotation, kinds ...Kind) bool {
	for _, a := range annotations {
		for _, k := range kinds {
			if a.Kind.key == k.key {
				return true
			}
		}
	}
	return false
}
