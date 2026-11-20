package main

import (
	"path/filepath"
	"testing"
)

func TestResumeFromDisk(t *testing.T) {
	db := filepath.Join(t.TempDir(), "review.db")
	for _, step := range []struct {
		approve bool
		want    string
	}{
		{false, "Awaiting approval: Ship the new search"},
		{true, "Approved: Ship the new search"},
		{true, "Already approved: Ship the new search"},
	} {
		got, err := run(t.Context(), db, step.approve)
		if err != nil || got != step.want {
			t.Fatalf("run(approve=%v) = %q, %v; want %q", step.approve, got, err, step.want)
		}
	}
}
