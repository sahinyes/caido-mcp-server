package store

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	frFileName    = "feature_requests.json"
	configDirName = ".caido-mcp"
	filePerm      = 0600
	dirPerm       = 0700
)

// FeatureRequest represents a user-submitted feature request.
type FeatureRequest struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    string   `json:"priority"` // low | medium | high
	Tags        []string `json:"tags,omitempty"`
	CreatedAt   string   `json:"createdAt"`
}

// FeatureRequestStore persists feature requests to a local JSON file.
type FeatureRequestStore struct {
	mu      sync.RWMutex
	dirPath string
}

// NewFeatureRequestStore creates a store backed by ~/.caido-mcp/feature_requests.json.
func NewFeatureRequestStore() (*FeatureRequestStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("feature request store: get home dir: %w", err)
	}
	return &FeatureRequestStore{dirPath: filepath.Join(home, configDirName)}, nil
}

func (s *FeatureRequestStore) filePath() string {
	return filepath.Join(s.dirPath, frFileName)
}

func (s *FeatureRequestStore) ensureDir() error {
	return os.MkdirAll(s.dirPath, dirPerm)
}

func (s *FeatureRequestStore) readAll() ([]FeatureRequest, error) {
	data, err := os.ReadFile(s.filePath())
	if os.IsNotExist(err) {
		return []FeatureRequest{}, nil
	}
	if err != nil {
		return nil, err
	}
	var frs []FeatureRequest
	if err := json.Unmarshal(data, &frs); err != nil {
		return nil, err
	}
	return frs, nil
}

func (s *FeatureRequestStore) writeAll(frs []FeatureRequest) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(frs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath() + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.filePath()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Add persists a new feature request and returns it with a generated ID.
func (s *FeatureRequestStore) Add(title, description, priority string, tags []string) (FeatureRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	frs, err := s.readAll()
	if err != nil {
		return FeatureRequest{}, err
	}

	fr := FeatureRequest{
		ID:          newID(),
		Title:       title,
		Description: description,
		Priority:    priority,
		Tags:        tags,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	frs = append(frs, fr)
	if err := s.writeAll(frs); err != nil {
		return FeatureRequest{}, err
	}
	return fr, nil
}

// List returns all stored feature requests.
func (s *FeatureRequestStore) List() ([]FeatureRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readAll()
}

// Get returns a single feature request by ID.
func (s *FeatureRequestStore) Get(id string) (FeatureRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	frs, err := s.readAll()
	if err != nil {
		return FeatureRequest{}, err
	}
	for _, fr := range frs {
		if fr.ID == id {
			return fr, nil
		}
	}
	return FeatureRequest{}, fmt.Errorf("feature request %q not found", id)
}

// Delete removes a feature request by ID.
func (s *FeatureRequestStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	frs, err := s.readAll()
	if err != nil {
		return err
	}
	filtered := frs[:0]
	found := false
	for _, fr := range frs {
		if fr.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, fr)
	}
	if !found {
		return fmt.Errorf("feature request %q not found", id)
	}
	return s.writeAll(filtered)
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b) //nolint:errcheck
	return fmt.Sprintf("fr-%d-%x", time.Now().UnixMilli(), b)
}
