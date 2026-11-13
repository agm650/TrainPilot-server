package store

import (
	"context"
	"testing"
)

func TestListLocomotivesEmptyReturnsNonNilSlice(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	items, err := store.ListLocomotives(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if items == nil {
		t.Fatal("ListLocomotives returned nil instead of an empty slice")
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 locomotives, got %d", len(items))
	}
}
