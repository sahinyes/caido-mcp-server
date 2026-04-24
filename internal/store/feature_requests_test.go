package store

import (
	"os"
	"testing"
)

func newTestStore(t *testing.T) *FeatureRequestStore {
	t.Helper()
	tmp := t.TempDir()
	return &FeatureRequestStore{dirPath: tmp}
}

func TestEmptyList(t *testing.T) {
	s := newTestStore(t)
	frs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(frs) != 0 {
		t.Fatalf("expected 0, got %d", len(frs))
	}
}

func TestAddAndList(t *testing.T) {
	s := newTestStore(t)

	f1, err := s.Add("Dark mode", "Add dark mode support", "high", []string{"UX"})
	if err != nil {
		t.Fatal(err)
	}
	if f1.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if f1.Priority != "high" {
		t.Fatalf("expected high, got %s", f1.Priority)
	}

	f2, err := s.Add("CSV export", "Export as CSV", "medium", nil)
	if err != nil {
		t.Fatal(err)
	}

	frs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(frs) != 2 {
		t.Fatalf("expected 2, got %d", len(frs))
	}

	// IDs must be unique
	if f1.ID == f2.ID {
		t.Fatal("IDs should be unique")
	}
}

func TestGet(t *testing.T) {
	s := newTestStore(t)
	added, _ := s.Add("Title", "Desc", "low", nil)

	got, err := s.Get(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != added.ID || got.Title != added.Title {
		t.Fatalf("mismatch: %+v vs %+v", got, added)
	}
}

func TestGetMissing(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	f1, _ := s.Add("Keep", "keep me", "low", nil)
	f2, _ := s.Add("Delete", "delete me", "high", nil)

	if err := s.Delete(f2.ID); err != nil {
		t.Fatal(err)
	}

	frs, _ := s.List()
	if len(frs) != 1 {
		t.Fatalf("expected 1, got %d", len(frs))
	}
	if frs[0].ID != f1.ID {
		t.Fatalf("wrong item survived: %s", frs[0].ID)
	}
}

func TestDeleteMissing(t *testing.T) {
	s := newTestStore(t)
	err := s.Delete("does-not-exist")
	if err == nil {
		t.Fatal("expected error deleting missing ID")
	}
}

func TestDeleteLast(t *testing.T) {
	s := newTestStore(t)
	f, _ := s.Add("Only one", "desc", "medium", nil)
	if err := s.Delete(f.ID); err != nil {
		t.Fatal(err)
	}
	frs, _ := s.List()
	if len(frs) != 0 {
		t.Fatalf("expected 0 after deleting last item, got %d", len(frs))
	}
}

func TestPersistence(t *testing.T) {
	s := newTestStore(t)
	added, _ := s.Add("Persist", "survives restart", "medium", nil)

	// new store instance, same dir
	s2 := &FeatureRequestStore{dirPath: s.dirPath}
	frs, err := s2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(frs) != 1 || frs[0].ID != added.ID {
		t.Fatalf("persistence failed: %+v", frs)
	}
}

func TestCorruptFile(t *testing.T) {
	s := newTestStore(t)
	s.ensureDir()
	os.WriteFile(s.filePath(), []byte("not json{{{"), 0600)

	_, err := s.List()
	if err == nil {
		t.Fatal("expected error on corrupt file")
	}
}
