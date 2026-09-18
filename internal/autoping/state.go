package autoping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const stateVersion = 1

type CredentialState struct {
	CredentialID             string    `json:"credential_id"`
	Provider                 string    `json:"provider"`
	Status                   string    `json:"status"`
	Reason                   string    `json:"reason,omitempty"`
	LastProcessedMilestone   string    `json:"last_processed_milestone,omitempty"`
	LastProcessedMilestoneAt time.Time `json:"last_processed_milestone_at,omitzero"`
	CurrentResetAt           time.Time `json:"current_reset_at,omitzero"`
	LastAttemptAt            time.Time `json:"last_attempt_at,omitzero"`
	LastAttemptStatus        string    `json:"last_attempt_status,omitempty"`
	LastPingAt               time.Time `json:"last_ping_at,omitzero"`
	LastError                string    `json:"last_error,omitempty"`
	NextRetryAt              time.Time `json:"next_retry_at,omitzero"`
	RetryCount               int       `json:"retry_count,omitzero"`
	BlockedCredentialVersion string    `json:"blocked_credential_version,omitempty"`
	SelectedModel            string    `json:"selected_model,omitempty"`
	Transport                string    `json:"transport,omitempty"`
	Attempts                 uint64    `json:"attempts,omitzero"`
	Successes                uint64    `json:"successes,omitzero"`
	Failures                 uint64    `json:"failures,omitzero"`
	Skipped                  uint64    `json:"skipped,omitzero"`
}

type StateDocument struct {
	Version                int                        `json:"version"`
	LastProcessedMilestone string                     `json:"last_processed_milestone,omitempty"`
	Credentials            map[string]CredentialState `json:"credentials"`
}

type StateStore struct {
	mu   sync.RWMutex
	path string
	doc  StateDocument
}

func (s *StateStore) Path() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

func (s *StateStore) LastProcessedMilestone() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.doc.LastProcessedMilestone
}

func (s *StateStore) SetLastMilestone(ctx context.Context, milestoneKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.LastProcessedMilestone = milestoneKey
	return s.saveLocked(ctx)
}

func LoadStateStore(ctx context.Context, path string) (*StateStore, error) {
	store := &StateStore{
		path: path,
		doc:  StateDocument{Version: stateVersion, Credentials: map[string]CredentialState{}},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store.doc); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	if store.doc.Version != stateVersion {
		return nil, fmt.Errorf("decode state: unsupported version %d", store.doc.Version)
	}
	if store.doc.Credentials == nil {
		store.doc.Credentials = map[string]CredentialState{}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *StateStore) Credential(id string) CredentialState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCredentialState(s.doc.Credentials[id])
}

func (s *StateStore) Update(ctx context.Context, id string, update func(*CredentialState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := cloneCredentialState(s.doc.Credentials[id])
	state.CredentialID = id
	if state.Provider == "" {
		state.Provider = "codex"
	}
	update(&state)
	s.doc.Credentials[id] = state
	return s.saveLocked(ctx)
}

func (s *StateStore) Snapshot() StateDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := StateDocument{Version: s.doc.Version, Credentials: make(map[string]CredentialState, len(s.doc.Credentials))}
	for id, state := range s.doc.Credentials {
		snapshot.Credentials[id] = cloneCredentialState(state)
	}
	return snapshot
}

func (s *StateStore) Accounts() []CredentialState {
	snapshot := s.Snapshot()
	ids := slices.Sorted(maps.Keys(snapshot.Credentials))
	accounts := make([]CredentialState, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, snapshot.Credentials[id])
	}
	return accounts
}

func (s *StateStore) saveLocked(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create state temp file: %w", err)
	}
	tempPath := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			resultErr = errors.Join(resultErr, os.Remove(tempPath))
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync state temp file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("protect state temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state temp file: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	cleanup = false
	return nil
}

func cloneCredentialState(state CredentialState) CredentialState {
	return state
}
