package coordinator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testACPConfig(serverURL string) *ACPConfig {
	return &ACPConfig{
		BaseURL: serverURL,
		Token:   "test-token",
		Project: "test-project",
		Model:   "claude-sonnet-4",
		Timeout: 900,
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
		if r.Method == "POST" && r.URL.Path == "/v1/sessions" {
			// Verify auth headers
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("wrong auth header: %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Ambient-Project") != "test-project" {
				t.Errorf("wrong project header: %q", r.Header.Get("X-Ambient-Project"))
			}

			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			if body["task"] == nil {
				t.Error("task field missing from request body")
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"id": "session-abc123"})
			return
		}
		// Handle the label PATCH request
		if r.Method == "PATCH" && strings.Contains(r.URL.Path, "/v1/sessions/") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "session-abc123"})
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
	var capturedContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/sessions/sess-1/message" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			capturedContent = body["content"]
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"run_id": "run-1"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	if err := acpSendMessage(cfg, "sess-1", "hello agent"); err != nil {
		t.Fatalf("acpSendMessage: %v", err)
	}
	if capturedContent != "hello agent" {
		t.Errorf("content = %q, want %q", capturedContent, "hello agent")
	}
}

func TestACPGetSessionPhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/sessions/sess-running" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-running", "status": "running"})
			return
		}
		if r.URL.Path == "/v1/sessions/sess-missing" {
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
		if r.Method == "GET" && r.URL.Path == "/v1/sessions" {
			selector := r.URL.Query().Get("labelSelector")
			if selector != "boss-space=myspace,managed-by=agent-boss" && selector != "managed-by=agent-boss,boss-space=myspace" {
				// Also allow unfiltered
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []map[string]interface{}{
					{"id": "sess-1", "status": "running", "labels": map[string]string{"boss-agent": "API", "boss-space": "myspace"}},
					{"id": "sess-2", "status": "stopped", "labels": map[string]string{"boss-agent": "FE", "boss-space": "myspace"}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	sessions, err := acpListSessions(cfg, map[string]string{"boss-space": "myspace", "managed-by": "agent-boss"})
	if err != nil {
		t.Fatalf("acpListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("got %d sessions, want 2", len(sessions))
	}
}

func TestACPStopSession(t *testing.T) {
	var capturedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && r.URL.Path == "/v1/sessions/sess-1" {
			json.NewDecoder(r.Body).Decode(&capturedBody)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "sess-1", "status": "stopped"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	if err := acpStopSession(cfg, "sess-1"); err != nil {
		t.Fatalf("acpStopSession: %v", err)
	}
	if stopped, ok := capturedBody["stopped"].(bool); !ok || !stopped {
		t.Errorf("expected stopped=true in body, got %v", capturedBody)
	}
}

func TestACPDeleteSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/v1/sessions/sess-1" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	if err := acpDeleteSession(cfg, "sess-1"); err != nil {
		t.Fatalf("acpDeleteSession: %v", err)
	}
}

func TestACPGetMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/sessions/sess-1/metrics" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"total_tokens": 5000, "input_tokens": 2500, "output_tokens": 2500,
				"duration_seconds": 120.5, "tool_calls": 15,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := testACPConfig(srv.URL)
	m, err := acpGetMetrics(cfg, "sess-1")
	if err != nil {
		t.Fatalf("acpGetMetrics: %v", err)
	}
	if m.TotalTokens != 5000 {
		t.Errorf("total_tokens = %d, want 5000", m.TotalTokens)
	}
	if m.ToolCalls != 15 {
		t.Errorf("tool_calls = %d, want 15", m.ToolCalls)
	}
}

func TestServerLaunchAgentHandler(t *testing.T) {
	// Mock ACP server
	var createCalled bool
	acpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/v1/sessions" {
			createCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"id": "launched-session-1"})
			return
		}
		if r.Method == "PATCH" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "launched-session-1"})
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
	// Mock ACP server that returns running for any session
	acpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/metrics") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"total_tokens": 100})
			return
		}
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/v1/sessions/") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"id": "s1", "status": "running"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/sessions" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
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
