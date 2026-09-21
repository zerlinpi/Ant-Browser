package profile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPrepareRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	loader := New(WithRoot(root))
	outside := filepath.Join(root, "..", "outside")
	_, err := loader.PrepareContext(context.Background(), Profile{ID: "profile", Path: outside})
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("outside profile accepted: %v", err)
	}
}

func TestPrepareDefaultsToIDUnderRoot(t *testing.T) {
	root := t.TempDir()
	prepared, err := New(WithRoot(root)).PrepareContext(context.Background(), Profile{ID: "profile-a"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "profile-a")
	if prepared.Path != filepath.Clean(want) {
		t.Fatalf("path = %q, want %q", prepared.Path, want)
	}
}
