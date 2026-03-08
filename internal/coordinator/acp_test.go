package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSessionsPath = "/api/projects/test-project/agentic-sessions"

func testACPConfig(serverURL string) *ACPConfig {
	return &ACPConfig{
		BaseURL: serverURL,
		Token:   "test-token",
		Project: "test-project",
		Model:   "claude-sonnet-4",
		Timeout: 900,
	}
}

// backendCR is a test helper to build a K8s CR-shaped response.
func backendCR(name, phase string, labels map[string]string) map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":   name,
			"labels": labels,
		},
		"spec": map[string]interface{}{
			"displayName": name,
		},
		"status": map[string]interface{}{
			"phase": phase,
		},
	}
}

func TestACPAvailable(t *testing.T) {
	if acpAvailable(nil) {
		t.Error("nil config should return false")
	}
	if acpAvailable(&ACPConfig{}) {
		t.Error("empty config should return false")
	}
	cfg := &ACPConfig{BaseURL: "http://x", Token: "t", Project: "p"}
	if !acpAvailable(cfg) {
		t.Error("complete config should return true")
	}
}

func TestACPCreateSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == testSessionsPath {
			// Verify auth header
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("wrong auth header: %q", r.Header.Get("Authorization"))
			}
			// Verify no X-Ambient-Project header (backend API uses path-based project)
			if r.Header.Get("X-Ambient-Project") != "" {
				t.Error("X-Ambient-Project header should not be set for backend API")
			}

			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			if body["initialPrompt"] == nil {
				t.Error("initialPrompt field missing from request body")
			}
			if body["task"] != nil {
				t.Error("task field should not be in backend API request")
			}
			if llm, ok := body["llmSettings"].(map[string]interface{}); !ok || llm["model"] == nil {
				t.Error("llmSettings.model missing from request body")
			}
			if body["labels"] == nil {
				t.Error("labels field missing from request body")
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(backendCR("session-abc123", "pending", nil))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	sessionID, err := acpCreateSession(cfg, "TestAgent", "myspace", "Do the task", []string{"https://github.com/org/repo"})
	if err != nil {
		t.Fatalf("acpCreateSession: %v", err)
	}
	if sessionID != "session-abc123" {
		t.Errorf("sessionID = %q, want session-abc123", sessionID)
	}
}

func TestACPSendMessage(t *testing.T) {
	var capturedMessages []interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == testSessionsPath+"/sess-1/agui/run" {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			if msgs, ok := body["messages"].([]interface{}); ok {
				capturedMessages = msgs
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"runId": "run-1", "threadId": "sess-1"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	if err := acpSendMessage(cfg, "sess-1", "hello agent"); err != nil {
		t.Fatalf("acpSendMessage: %v", err)
	}
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}
	msg := capturedMessages[0].(map[string]interface{})
	if msg["role"] != "user" {
		t.Errorf("role = %q, want user", msg["role"])
	}
	if msg["content"] != "hello agent" {
		t.Errorf("content = %q, want %q", msg["content"], "hello agent")
	}
	if msg["id"] == nil || msg["id"] == "" {
		t.Error("message id should be set")
	}
}

func TestACPGetSessionPhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath+"/sess-running" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(backendCR("sess-running", "running", nil))
			return
		}
		if r.URL.Path == testSessionsPath+"/sess-missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	phase, err := acpGetSessionPhase(cfg, "sess-running")
	if err != nil {
		t.Fatalf("acpGetSessionPhase: %v", err)
	}
	if phase != "running" {
		t.Errorf("phase = %q, want running", phase)
	}

	phase, err = acpGetSessionPhase(cfg, "sess-missing")
	if err != nil {
		t.Fatalf("acpGetSessionPhase (404): %v", err)
	}
	if phase != "not_found" {
		t.Errorf("phase = %q, want not_found", phase)
	}
}

func TestACPListSessions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []interface{}{
					backendCR("sess-1", "running", map[string]string{
						"boss-agent": "API", "boss-space": "myspace", "managed-by": "agent-boss",
					}),
					backendCR("sess-2", "stopped", map[string]string{
						"boss-agent": "FE", "boss-space": "myspace", "managed-by": "agent-boss",
					}),
					backendCR("sess-3", "running", map[string]string{
						"boss-agent": "Other", "boss-space": "otherspace", "managed-by": "agent-boss",
					}),
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	// Filter by labels (client-side filtering)
	sessions, err := acpListSessions(cfg, map[string]string{"boss-space": "myspace", "managed-by": "agent-boss"})
	if err != nil {
		t.Fatalf("acpListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("got %d sessions, want 2 (filtered from 3)", len(sessions))
	}

	// No filter returns all
	allSessions, err := acpListSessions(cfg, nil)
	if err != nil {
		t.Fatalf("acpListSessions (no filter): %v", err)
	}
	if len(allSessions) != 3 {
		t.Errorf("got %d sessions, want 3 (unfiltered)", len(allSessions))
	}
}

func TestACPStopSession(t *testing.T) {
	var stopCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == testSessionsPath+"/sess-1/stop" {
			stopCalled = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	if err := acpStopSession(cfg, "sess-1"); err != nil {
		t.Fatalf("acpStopSession: %v", err)
	}
	if !stopCalled {
		t.Error("expected POST to /stop endpoint")
	}
}

func TestACPDeleteSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == testSessionsPath+"/sess-1" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == testSessionsPath+"/sess-404" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == testSessionsPath+"/sess-error" {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	// Test successful delete
	if err := acpDeleteSession(cfg, "sess-1"); err != nil {
		t.Fatalf("acpDeleteSession: %v", err)
	}

	// Test 404 (should not error)
	if err := acpDeleteSession(cfg, "sess-404"); err != nil {
		t.Fatalf("acpDeleteSession (404): %v", err)
	}

	// Test error case
	if err := acpDeleteSession(cfg, "sess-error"); err == nil {
		t.Error("acpDeleteSession should error on 500")
	}
}

func TestACPGetMetrics(t *testing.T) {
	cfg := testACPConfig("http://unused")
	// acpGetMetrics returns empty metrics (not supported by backend API)
	m, err := acpGetMetrics(cfg, "sess-1")
	if err != nil {
		t.Fatalf("acpGetMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}
	if m.TotalTokens != 0 {
		t.Errorf("total_tokens = %d, want 0 (backend doesn't provide metrics)", m.TotalTokens)
	}
}

func TestACPGetSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath+"/sess-exists" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(backendCR("sess-exists", "running", map[string]string{"boss-agent": "TestAgent"}))
			return
		}
		if r.URL.Path == testSessionsPath+"/sess-notfound" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	// Test existing session
	session, err := acpGetSession(cfg, "sess-exists")
	if err != nil {
		t.Fatalf("acpGetSession: %v", err)
	}
	if session == nil {
		t.Fatal("expected session, got nil")
	}
	if session.ID != "sess-exists" {
		t.Errorf("session.ID = %q, want sess-exists", session.ID)
	}
	if session.Status != "running" {
		t.Errorf("session.Status = %q, want running", session.Status)
	}

	// Test not found (should return nil without error)
	session, err = acpGetSession(cfg, "sess-notfound")
	if err != nil {
		t.Fatalf("acpGetSession (404): %v", err)
	}
	if session != nil {
		t.Error("expected nil for 404, got session")
	}
}

func TestACPGetOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath+"/sess-1/export" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"aguiEvents":[{"type":"output","content":"all output"}],"legacyMessages":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	output, err := acpGetOutput(cfg, "sess-1", "")
	if err != nil {
		t.Fatalf("acpGetOutput: %v", err)
	}
	if !strings.Contains(string(output), "all output") {
		t.Errorf("output = %s, want to contain 'all output'", string(output))
	}

	// runID parameter is accepted but ignored (export returns all events)
	output, err = acpGetOutput(cfg, "sess-1", "run-123")
	if err != nil {
		t.Fatalf("acpGetOutput (with runID): %v", err)
	}
	if !strings.Contains(string(output), "all output") {
		t.Errorf("output = %s, want to contain 'all output'", string(output))
	}
}

func TestACPGetTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath+"/sess-1/export" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"aguiEvents":[{"type":"messages_snapshot"}],"legacyMessages":[{"role":"user","content":"hello"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)

	// acpGetTranscript delegates to export endpoint
	transcript, err := acpGetTranscript(cfg, "sess-1", "")
	if err != nil {
		t.Fatalf("acpGetTranscript: %v", err)
	}
	if !strings.Contains(string(transcript), "legacyMessages") {
		t.Errorf("transcript = %s, want to contain 'legacyMessages'", string(transcript))
	}

	// Format parameter accepted but export always returns full shape
	transcript, err = acpGetTranscript(cfg, "sess-1", "json")
	if err != nil {
		t.Fatalf("acpGetTranscript (json): %v", err)
	}
	if !strings.Contains(string(transcript), "legacyMessages") {
		t.Errorf("transcript = %s, want to contain 'legacyMessages'", string(transcript))
	}
}

func TestServerLaunchAgentHandler(t *testing.T) {
	// Mock ACP server
	var createCalled bool
	acpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == testSessionsPath {
			createCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(backendCR("launched-session-1", "pending", nil))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer acpSrv.Close()

	dataDir := t.TempDir()
	srv := NewServer(":0", dataDir)
	srv.acpConfig = testACPConfig(acpSrv.URL)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	base := serverBaseURL(srv)

	// POST to launch agent
	req, _ := http.NewRequest("POST", base+"/spaces/testspace/launch/TestAgent",
		strings.NewReader(`{"prompt":"Do the thing","repos":["https://github.com/org/repo"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !createCalled {
		t.Error("ACP createSession was not called")
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["session_id"] != "launched-session-1" {
		t.Errorf("session_id = %q, want launched-session-1", result["session_id"])
	}
}

func TestServerSessionStatusHandler(t *testing.T) {
	// Mock ACP server
	acpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == testSessionsPath {
			// List sessions (for auto-discover)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, testSessionsPath+"/") {
			// Get individual session phase
			sessionName := strings.TrimPrefix(r.URL.Path, testSessionsPath+"/")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(backendCR(sessionName, "running", nil))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer acpSrv.Close()

	dataDir := t.TempDir()
	srv := NewServer(":0", dataDir)
	srv.acpConfig = testACPConfig(acpSrv.URL)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	base := serverBaseURL(srv)

	// Create an agent with an ACP session ID
	postJSON(t, base+"/spaces/myspace/agent/API", AgentUpdate{
		Status:       StatusActive,
		Summary:      "working",
		ACPSessionID: "s1",
	})

	// Check session status endpoint
	code, body := getBody(t, base+"/spaces/myspace/api/session-status")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", code, body)
	}
	var statuses []sessionAgentStatus
	json.Unmarshal([]byte(body), &statuses)
	if len(statuses) == 0 {
		t.Fatal("expected at least 1 session status entry")
	}
	found := false
	for _, s := range statuses {
		if s.Agent == "Api" {
			found = true
			if s.ACPSessionID != "s1" {
				t.Errorf("acp_session_id = %q, want s1", s.ACPSessionID)
			}
			if s.Phase != "running" {
				t.Errorf("phase = %q, want running", s.Phase)
			}
		}
	}
	if !found {
		t.Error("agent 'Api' not found in session status")
	}
}

func TestMatchLabels(t *testing.T) {
	tests := []struct {
		name string
		have map[string]string
		want map[string]string
		ok   bool
	}{
		{"nil want matches anything", map[string]string{"a": "1"}, nil, true},
		{"empty want matches anything", map[string]string{"a": "1"}, map[string]string{}, true},
		{"exact match", map[string]string{"a": "1"}, map[string]string{"a": "1"}, true},
		{"subset match", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1"}, true},
		{"mismatch value", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{"missing key", map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
		{"nil have", nil, map[string]string{"a": "1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchLabels(tt.have, tt.want); got != tt.ok {
				t.Errorf("matchLabels(%v, %v) = %v, want %v", tt.have, tt.want, got, tt.ok)
			}
		})
	}
}

func TestBackendSessionCRToSessionStatus(t *testing.T) {
	cr := backendSessionCR{}
	cr.Metadata.Name = "test-session"
	cr.Metadata.Labels = map[string]string{"boss-agent": "TestAgent"}
	cr.Metadata.CreationTimestamp = "2026-03-07T12:00:00Z"
	cr.Spec.DisplayName = "Test Agent"
	cr.Status.Phase = "running"

	s := cr.toSessionStatus()
	if s.ID != "test-session" {
		t.Errorf("ID = %q, want test-session", s.ID)
	}
	if s.Status != "running" {
		t.Errorf("Status = %q, want running", s.Status)
	}
	if s.DisplayName != "Test Agent" {
		t.Errorf("DisplayName = %q, want Test Agent", s.DisplayName)
	}
	if s.Labels["boss-agent"] != "TestAgent" {
		t.Errorf("Labels[boss-agent] = %q, want TestAgent", s.Labels["boss-agent"])
	}
}

func TestSessionsPath(t *testing.T) {
	cfg := &ACPConfig{Project: "my-project"}
	if got := cfg.sessionsPath(); got != "/api/projects/my-project/agentic-sessions" {
		t.Errorf("sessionsPath() = %q", got)
	}
	if got := cfg.sessionPath("sess-1"); got != "/api/projects/my-project/agentic-sessions/sess-1" {
		t.Errorf("sessionPath() = %q", got)
	}
}
