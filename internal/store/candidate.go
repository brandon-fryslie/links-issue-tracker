package store

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Candidate is one disposable, fully isolated rebuild of a workspace: a fresh
// Dolt directory at the current baseline, loaded with the domain data a
// validated (dump, mapping) produced. It is the unit the recovery loop verifies
// (Doctor + conservation against the dump) and then either promotes or throws
// away.
//
// [LAW:types-are-the-program] A candidate OWNS its own directory. That is what
// makes "a rejected attempt leaves zero durable residue" a structural fact
// rather than a cleanup routine: discarding a candidate removes its directory
// whole, and the next attempt is a different, empty directory — there are no
// rows to scrub and nothing one attempt can leak into the next. The alternative
// (reuse one workspace and roll back) would lean on the import path's row
// deletion to undo a rejected attempt, which is the drift-prone shape this type
// exists to forbid.
type Candidate struct {
	store *Store
	dir   string
}

// RebuildCandidate is the mechanical applier's lifecycle: it turns a validated
// (dump, mapping) into a fresh candidate workspace, or rejects. No LLM is in
// this path — deterministic and LLM mappers alike produce a ShapeMapping that
// flows through the identical Apply + load.
//
// [LAW:dataflow-not-control-flow] The sequence is the same on every attempt;
// the (dump, mapping) values are the only variability. Apply (pure) runs first,
// so an invalid or incomplete mapping is rejected before any directory or
// database handle exists — the common rejection cannot leave residue because no
// resource was acquired. Only once a valid Export exists does the lifecycle
// touch the filesystem.
//
// [LAW:one-source-of-truth] dump is read-only here and may be reused unchanged
// across attempts; Apply never mutates it, so two attempts from one dump yield
// identical candidates.
//
// parentDir is the directory under which the throwaway candidate directory is
// created (empty means the system temp dir). The caller owns where recovery
// scratch lives; the unique per-call subdirectory is what guarantees each
// attempt starts clean.
func RebuildCandidate(ctx context.Context, parentDir string, dump RawDump, mapping ShapeMapping) (_ *Candidate, err error) {
	// [LAW:single-enforcer] Apply folds through Validate — the one well-formedness
	// boundary — so a rejection here is exactly "the mapping is invalid/incomplete".
	export, err := Apply(dump, mapping)
	if err != nil {
		return nil, fmt.Errorf("apply mapping: %w", err)
	}

	dir, err := os.MkdirTemp(parentDir, "lit-candidate-*")
	if err != nil {
		return nil, fmt.Errorf("create candidate workspace dir: %w", err)
	}
	// [LAW:dataflow-not-control-flow] Cleanup runs unconditionally on the way out;
	// the success flag is the datum that decides whether it fires. A rejected
	// attempt thus removes the directory (and closes the store, if opened) it
	// touched, leaving zero durable residue — the same idiom Open uses to release
	// resources it acquired before a failure.
	var st *Store
	success := false
	defer func() {
		if success {
			return
		}
		if st != nil {
			err = errors.Join(err, st.Close())
		}
		err = errors.Join(err, os.RemoveAll(dir))
	}()

	st, err = Open(ctx, dir, dump.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("open candidate workspace: %w", err)
	}
	if err = st.ReplaceFromExport(ctx, export); err != nil {
		return nil, fmt.Errorf("load export into candidate: %w", err)
	}

	success = true
	return &Candidate{store: st, dir: dir}, nil
}

// Store hands out the built workspace so the verify gate can inspect it (Doctor,
// conservation Export against the dump). The candidate is the owner; the gate is
// a read-only consumer.
func (c *Candidate) Store() *Store { return c.store }

// Discard releases the candidate: it closes the store and removes its directory.
// It is idempotent — a caller may defer Discard and still discard explicitly on
// the reject path. After Discard the candidate holds no resources.
func (c *Candidate) Discard() error {
	if c.store == nil {
		return nil
	}
	err := c.store.Close()
	c.store = nil
	return errors.Join(err, os.RemoveAll(c.dir))
}
