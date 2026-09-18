package autoping

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestManagementRegistersAuthenticatedRoutesOnly(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	raw := runtime.Handle(t.Context(), "management.register", nil)
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("management registration failed")
	}
	var registration struct {
		Routes    []managementRoute `json:"Routes"`
		Resources []any             `json:"Resources"`
	}
	if err := json.Unmarshal(envelope.Result, &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Routes) != 3 || len(registration.Resources) != 0 {
		t.Fatalf("registration = %#v", registration)
	}
	for _, route := range registration.Routes {
		if !strings.HasPrefix(route.Path, "/cliproxyapi-auto-ping/") {
			t.Fatalf("route %q does not use the manifest ID prefix", route.Path)
		}
	}
}

func TestManagementStatusNeverExposesCredentialToken(t *testing.T) {
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document
	host.httpStreamFunc = func(HTTPRequest) (HTTPStreamResponse, []HTTPStreamChunk, error) { return successStream() }

	runtime := newTestRuntime(t, host, Options{})
	cfg := testConfig(t, runtime, "")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ManualPing(t.Context(), ManualPingRequest{CredentialID: "codex-a"}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var response managementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(response.Body, []byte("token-a")) {
		t.Fatalf("management response exposed token: %s", response.Body)
	}
}

func TestManagementStatusReportsManifestIdentity(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	response := decodeManagementResponse(t, raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var payload struct {
		Plugin  string `json:"plugin"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Plugin != "cliproxyapi-auto-ping" || payload.Version != "0.1.0" {
		t.Fatalf("status identity = %#v", payload)
	}
}

func TestManagementRejectsObsoleteRoutePrefix(t *testing.T) {
	runtime := newTestRuntime(t, newFakeHost(), Options{})
	for _, path := range []string{"/v0/management/auto-ping/status", "/v0/management/auto-ping/diagnostics"} {
		request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: path})
		response := decodeManagementResponse(t, runtime.Handle(t.Context(), "management.handle", request))
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("obsolete route %q returned %d, want 404", path, response.StatusCode)
		}
	}
}

func decodeManagementResponse(t *testing.T, raw []byte) managementResponse {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var response managementResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestManagementStatusReportsScheduleMetadata(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 6, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	file, document := credential("codex-a", "token-a", 1)
	host.auths = []AuthFile{file}
	host.docs[file.AuthIndex] = document

	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := LoadStateStore(t.Context(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLastMilestone(t.Context(), "2026-09-18#05:00"); err != nil {
		t.Fatal(err)
	}

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg := testConfig(t, runtime, "schedule:\n  - \"05:00\"\n  - \"10:00\"\n  - \"15:00\"\n  - \"20:00\"\ntimezone: UTC\n")
	cfg.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	request, _ := json.Marshal(managementRequest{Method: http.MethodGet, Path: "/v0/management/cliproxyapi-auto-ping/status"})
	raw := runtime.Handle(t.Context(), "management.handle", request)
	response := decodeManagementResponse(t, raw)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var payload struct {
		Plugin          string       `json:"plugin"`
		Version         string       `json:"version"`
		Schedule        []string     `json:"schedule"`
		Timezone        string       `json:"timezone"`
		NextMilestoneAt string       `json:"next_milestone_at"`
		LastMilestone   string       `json:"last_milestone"`
		AutoPing        statusConfig `json:"auto_ping"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}

	expectedSchedule := []string{"05:00", "10:00", "15:00", "20:00"}
	if !slices.Equal(payload.Schedule, expectedSchedule) || !slices.Equal(payload.AutoPing.Schedule, expectedSchedule) {
		t.Fatalf("schedule = %v, auto_ping.schedule = %v, want %v", payload.Schedule, payload.AutoPing.Schedule, expectedSchedule)
	}
	if payload.Timezone != "UTC" || payload.AutoPing.Timezone != "UTC" {
		t.Fatalf("timezone = %q, want UTC", payload.Timezone)
	}
	// At 06:00 UTC with schedule 05:00, 10:00, 15:00, 20:00, next milestone is 10:00
	expectedNext := "2026-09-18T10:00:00Z"
	if payload.NextMilestoneAt != expectedNext || payload.AutoPing.NextMilestoneAt != expectedNext {
		t.Fatalf("next_milestone_at = %q, auto_ping.next_milestone_at = %q, want %q", payload.NextMilestoneAt, payload.AutoPing.NextMilestoneAt, expectedNext)
	}
	if payload.LastMilestone != "2026-09-18#05:00" || payload.AutoPing.LastMilestone != "2026-09-18#05:00" {
		t.Fatalf("last_milestone = %q, auto_ping.last_milestone = %q, want 2026-09-18#05:00", payload.LastMilestone, payload.AutoPing.LastMilestone)
	}
}

func TestReconfigurationReschedulesNextMilestone(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 6, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	statePath := filepath.Join(t.TempDir(), "state.json")
	store, _ := LoadStateStore(t.Context(), statePath)
	_ = store.SetLastMilestone(t.Context(), "2026-09-18#05:00")

	startupDelay := 24 * time.Hour
	runtime := newTestRuntime(t, host, Options{Now: clock.Now, StartupDelay: &startupDelay})
	cfg1 := testConfig(t, runtime, "schedule:\n  - \"05:00\"\n  - \"10:00\"\n  - \"15:00\"\n  - \"20:00\"\ntimezone: UTC\n")
	cfg1.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg1); err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(t.Context())

	// Initial status has 4 milestones, next is 10:00
	status1 := runtime.status()
	if len(status1.Schedule) != 4 || status1.NextMilestoneAt != "2026-09-18T10:00:00Z" {
		t.Fatalf("initial status = %#v", status1)
	}

	// Reconfigure with 3 milestones: 07:00, 14:00, 21:00
	cfg2 := testConfig(t, runtime, "schedule:\n  - \"07:00\"\n  - \"14:00\"\n  - \"21:00\"\ntimezone: UTC\n")
	cfg2.StatePath = statePath
	if err := runtime.Configure(t.Context(), cfg2); err != nil {
		t.Fatal(err)
	}

	// Reconfigured status has 3 milestones, next is rescheduled to 07:00, and last milestone history preserved
	status2 := runtime.status()
	expectedSchedule := []string{"07:00", "14:00", "21:00"}
	if !slices.Equal(status2.Schedule, expectedSchedule) {
		t.Fatalf("reconfigured schedule = %v, want %v", status2.Schedule, expectedSchedule)
	}
	if status2.NextMilestoneAt != "2026-09-18T07:00:00Z" {
		t.Fatalf("reconfigured next milestone = %q, want 2026-09-18T07:00:00Z", status2.NextMilestoneAt)
	}
	if status2.LastMilestone != "2026-09-18#05:00" {
		t.Fatalf("reconfigured last milestone history lost: got %q, want 2026-09-18#05:00", status2.LastMilestone)
	}
}

func TestReconfigurationGracefulShutdownCleanLifecycle(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)}
	host := newFakeHost()
	runtime := newTestRuntime(t, host, Options{Now: clock.Now})
	cfg := testConfig(t, runtime, "timezone: UTC\n")
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")

	// Start runtime with active scheduleLoop
	if err := runtime.Configure(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}

	// Reconfigure while running
	cfg2 := testConfig(t, runtime, "schedule:\n  - \"08:00\"\n  - \"16:00\"\ntimezone: UTC\n")
	cfg2.StatePath = cfg.StatePath
	if err := runtime.Configure(t.Context(), cfg2); err != nil {
		t.Fatal(err)
	}

	// Shutdown should halt background scanner cleanly within bounded time
	shutdownCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	// Double shutdown is safe and idempotent
	if err := runtime.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("double shutdown failed: %v", err)
	}
}
