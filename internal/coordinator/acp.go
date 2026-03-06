package coordinator

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	acpHTTPTimeout    = 30 * time.Second
	acpDefaultModel   = "claude-sonnet-4-5"
	acpDefaultTimeout = 900
	boardPollTimeout  = 3 * time.Minute
	boardPollInterval = 3 * time.Second
)

// ACPConfig holds ACP REST API connection settings.
type ACPConfig struct {
	BaseURL string // ACP public-api gateway URL
	Token   string // Bearer token
	Project string // ACP project name
	Model   string // Default model (e.g. "claude-sonnet-4")
	Timeout int    // Session timeout seconds
}

// acpSessionStatus represents an ACP session retrieved from the API.
type acpSessionStatus struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	DisplayName string            `json:"displayName,omitempty"`
	CreatedAt   time.Time         `json:"createdAt,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// acpMetrics holds usage metrics for an ACP session.
type acpMetrics struct {
	TotalTokens     int     `json:"total_tokens,omitempty"`
	InputTokens     int     `json:"input_tokens,omitempty"`
	OutputTokens    int     `json:"output_tokens,omitempty"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	ToolCalls       int     `json:"tool_calls,omitempty"`
}

// acpAvailable returns true if ACP configuration is set.
func acpAvailable(cfg *ACPConfig) bool {
	return cfg != nil && cfg.BaseURL != "" && cfg.Token != "" && cfg.Project != ""
}

func newACPHTTPClient() *http.Client {
	// Allow disabling TLS verification for self-signed cluster certificates
	// (common in OpenShift/OKD lab deployments)
	skipTLS := os.Getenv("ACP_INSECURE_TLS") == "true"
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLS}, //nolint:gosec
	}
	return &http.Client{
		Timeout:   acpHTTPTimeout,
		Transport: transport,
	}
}

func (cfg *ACPConfig) doRequest(method, path string, body interface{}) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	fullURL := strings.TrimRight(cfg.BaseURL, "/") + path
	req, err := http.NewRequest(method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Ambient-Project", cfg.Project)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := newACPHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

func (cfg *ACPConfig) doRequestWithQuery(method, path string, params map[string]string) ([]byte, int, error) {
	if len(params) > 0 {
		q := url.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		path = path + "?" + q.Encode()
	}
	return cfg.doRequest(method, path, nil)
}

// acpCreateSession creates a new ACP session for an agent.
// Returns the session ID on success.
func acpCreateSession(cfg *ACPConfig, agentName, spaceName, task string, repos []string) (string, error) {
	type repoEntry struct {
		URL string `json:"url"`
	}
	type createReq struct {
		Task        string      `json:"task"`
		DisplayName string      `json:"display_name,omitempty"`
		Model       string      `json:"model,omitempty"`
		Repos       []repoEntry `json:"repos,omitempty"`
	}

	model := cfg.Model
	if model == "" {
		model = acpDefaultModel
	}

	reqBody := createReq{
		Task:        task,
		DisplayName: agentName,
		Model:       model,
	}
	for _, r := range repos {
		if r != "" {
			reqBody.Repos = append(reqBody.Repos, repoEntry{URL: r})
		}
	}

	data, code, err := cfg.doRequest("POST", "/v1/sessions", reqBody)
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("create session: HTTP %d: %s", code, string(data))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse create response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("create session: empty ID in response")
	}

	// Label the session for discovery
	labels := map[string]string{
		"boss-space":  spaceName,
		"boss-agent":  agentName,
		"managed-by":  "agent-boss",
	}
	if err := acpLabelSession(cfg, result.ID, labels); err != nil {
		// Non-fatal: session is created, labels just won't be set
		_ = err
	}

	return result.ID, nil
}

// acpLabelSession sets labels on an existing ACP session.
func acpLabelSession(cfg *ACPConfig, sessionID string, labels map[string]string) error {
	body := map[string]interface{}{"labels": labels}
	data, code, err := cfg.doRequest("PATCH", "/v1/sessions/"+sessionID, body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("label session: HTTP %d: %s", code, string(data))
	}
	return nil
}

// acpSendMessage sends a message to an ACP session.
func acpSendMessage(cfg *ACPConfig, sessionID, content string) error {
	body := map[string]string{"content": content}
	data, code, err := cfg.doRequest("POST", "/v1/sessions/"+sessionID+"/message", body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("send message: HTTP %d: %s", code, string(data))
	}
	return nil
}

// acpGetSessionPhase retrieves the current status/phase of an ACP session.
func acpGetSessionPhase(cfg *ACPConfig, sessionID string) (string, error) {
	data, code, err := cfg.doRequest("GET", "/v1/sessions/"+sessionID, nil)
	if err != nil {
		return "", err
	}
	if code == 404 {
		return "not_found", nil
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("get session: HTTP %d: %s", code, string(data))
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parse session response: %w", err)
	}
	return result.Status, nil
}

// acpGetSession retrieves full session details.
func acpGetSession(cfg *ACPConfig, sessionID string) (*acpSessionStatus, error) {
	data, code, err := cfg.doRequest("GET", "/v1/sessions/"+sessionID, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 {
		return nil, nil
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("get session: HTTP %d: %s", code, string(data))
	}
	var s acpSessionStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	return &s, nil
}

// acpListSessions lists ACP sessions matching a label selector.
// labels is a map of key=value pairs; empty map returns all sessions.
func acpListSessions(cfg *ACPConfig, labels map[string]string) ([]acpSessionStatus, error) {
	params := map[string]string{}
	if len(labels) > 0 {
		var parts []string
		for k, v := range labels {
			parts = append(parts, k+"="+v)
		}
		params["labelSelector"] = strings.Join(parts, ",")
	}

	data, code, err := cfg.doRequestWithQuery("GET", "/v1/sessions", params)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("list sessions: HTTP %d: %s", code, string(data))
	}

	var result struct {
		Items []acpSessionStatus `json:"items"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse sessions: %w", err)
	}
	return result.Items, nil
}

// acpStopSession stops a running ACP session.
func acpStopSession(cfg *ACPConfig, sessionID string) error {
	body := map[string]bool{"stopped": true}
	data, code, err := cfg.doRequest("PATCH", "/v1/sessions/"+sessionID, body)
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("stop session: HTTP %d: %s", code, string(data))
	}
	return nil
}

// acpDeleteSession deletes an ACP session.
func acpDeleteSession(cfg *ACPConfig, sessionID string) error {
	data, code, err := cfg.doRequest("DELETE", "/v1/sessions/"+sessionID, nil)
	if err != nil {
		return err
	}
	if code == 204 || code == 404 {
		return nil
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("delete session: HTTP %d: %s", code, string(data))
	}
	return nil
}

// acpGetOutput retrieves AG-UI events/output for an ACP session.
// Optionally filter by runID.
func acpGetOutput(cfg *ACPConfig, sessionID, runID string) (json.RawMessage, error) {
	params := map[string]string{}
	if runID != "" {
		params["run_id"] = runID
	}
	data, code, err := cfg.doRequestWithQuery("GET", "/v1/sessions/"+sessionID+"/output", params)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("get output: HTTP %d: %s", code, string(data))
	}
	return json.RawMessage(data), nil
}

// acpGetMetrics retrieves usage metrics for an ACP session.
func acpGetMetrics(cfg *ACPConfig, sessionID string) (*acpMetrics, error) {
	data, code, err := cfg.doRequest("GET", "/v1/sessions/"+sessionID+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("get metrics: HTTP %d: %s", code, string(data))
	}
	var m acpMetrics
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}
	return &m, nil
}

// acpGetTranscript retrieves the conversation transcript for an ACP session.
func acpGetTranscript(cfg *ACPConfig, sessionID, format string) (json.RawMessage, error) {
	if format == "" {
		format = "json"
	}
	params := map[string]string{"format": format}
	data, code, err := cfg.doRequestWithQuery("GET", "/v1/sessions/"+sessionID+"/transcript", params)
	if err != nil {
		return nil, err
	}
	if code < 200 || code >= 300 {
		return nil, fmt.Errorf("get transcript: HTTP %d: %s", code, string(data))
	}
	return json.RawMessage(data), nil
}

// ACPAutoDiscover discovers ACP sessions for agents in a space using labels,
// and stores the ACPSessionID on agents that don't have one yet.
// Returns the number of sessions matched.
func (s *Server) ACPAutoDiscover(spaceName string) int {
	if !acpAvailable(s.acpConfig) {
		return 0
	}

	sessions, err := acpListSessions(s.acpConfig, map[string]string{
		"boss-space": spaceName,
		"managed-by": "agent-boss",
	})
	if err != nil {
		s.logEvent(fmt.Sprintf("[%s] ACP auto-discover error: %v", spaceName, err))
		return 0
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		return 0
	}

	matched := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range sessions {
		agentName := session.Labels["boss-agent"]
		if agentName == "" {
			continue
		}
		// Find matching agent (case-insensitive)
		for name, agent := range ks.Agents {
			if agent.ACPSessionID != "" {
				continue
			}
			if strings.EqualFold(name, agentName) {
				agent.ACPSessionID = session.ID
				matched++
				s.logEvent(fmt.Sprintf("[%s/%s] ACP session auto-discovered: %s", spaceName, name, session.ID))
				break
			}
		}
	}
	if matched > 0 {
		s.saveSpace(ks)
	}
	return matched
}

// BroadcastCheckIn sends a check-in message to all running agents in a space.
func (s *Server) BroadcastCheckIn(spaceName string) *BroadcastResult {
	result := &BroadcastResult{}

	if !acpAvailable(s.acpConfig) {
		result.Errors = append(result.Errors, "ACP not configured (ACP_URL, ACP_TOKEN, ACP_PROJECT required)")
		return result
	}

	s.ACPAutoDiscover(spaceName)

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
	}

	bossURL := s.externalURL()

	s.mu.RLock()
	type target struct {
		agentName    string
		acpSessionID string
	}
	var targets []target
	for name, agent := range ks.Agents {
		if agent.ACPSessionID != "" {
			targets = append(targets, target{
				agentName:    name,
				acpSessionID: agent.ACPSessionID,
			})
		}
	}
	s.mu.RUnlock()

	if len(targets) == 0 {
		result.Errors = append(result.Errors, "no agents have registered ACP sessions")
		return result
	}

	s.logEvent(fmt.Sprintf("[%s] broadcast: processing %d agents concurrently", spaceName, len(targets)))

	checkinPrompt := fmt.Sprintf(
		"BOSS CHECK-IN: Read the blackboard at %s/spaces/%s/raw\nthen POST your current status following the protocol. "+
			"Include status, summary, branch, pr, items, next_steps. Resume your previous work after.",
		bossURL, spaceName,
	)

	var wg sync.WaitGroup
	for _, t := range targets {
		// Check session phase first
		phase, err := acpGetSessionPhase(s.acpConfig, t.acpSessionID)
		if err != nil {
			result.addSkipped(t.agentName + " (phase check failed: " + err.Error() + ")")
			continue
		}
		if phase != "running" {
			result.addSkipped(fmt.Sprintf("%s (phase: %s)", t.agentName, phase))
			continue
		}

		wg.Add(1)
		go func(agentName, sessionID string) {
			defer wg.Done()
			s.runACPAgentCheckIn(spaceName, agentName, sessionID, checkinPrompt, result)
		}(t.agentName, t.acpSessionID)
	}
	wg.Wait()

	s.logEvent(fmt.Sprintf("[%s] broadcast complete: %d sent, %d skipped, %d errors",
		spaceName, len(result.Sent), len(result.Skipped), len(result.Errors)))
	return result
}

// SingleAgentCheckIn sends a check-in message to a single agent.
func (s *Server) SingleAgentCheckIn(spaceName, agentName string) *BroadcastResult {
	result := &BroadcastResult{}

	if !acpAvailable(s.acpConfig) {
		result.Errors = append(result.Errors, "ACP not configured (ACP_URL, ACP_TOKEN, ACP_PROJECT required)")
		return result
	}

	ks, ok := s.getSpace(spaceName)
	if !ok {
		result.Errors = append(result.Errors, "space not found: "+spaceName)
		return result
	}

	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var sessionID string
	if exists {
		sessionID = agent.ACPSessionID
	}
	s.mu.RUnlock()

	if !exists {
		result.Errors = append(result.Errors, "agent not found: "+agentName)
		return result
	}
	if sessionID == "" {
		result.Errors = append(result.Errors, canonical+": no ACP session registered")
		return result
	}

	phase, err := acpGetSessionPhase(s.acpConfig, sessionID)
	if err != nil {
		result.addSkipped(canonical + " (phase check failed: " + err.Error() + ")")
		return result
	}
	if phase != "running" {
		result.addSkipped(fmt.Sprintf("%s (phase: %s)", canonical, phase))
		return result
	}

	bossURL := s.externalURL()
	checkinPrompt := fmt.Sprintf(
		"BOSS CHECK-IN: Read the blackboard at %s/spaces/%s/raw\nthen POST your current status following the protocol. "+
			"Include status, summary, branch, pr, items, next_steps. Resume your previous work after.",
		bossURL, spaceName,
	)

	s.runACPAgentCheckIn(spaceName, canonical, sessionID, checkinPrompt, result)
	return result
}

func (s *Server) runACPAgentCheckIn(spaceName, agentName, sessionID, checkinPrompt string, result *BroadcastResult) {
	progress := func(msg string) {
		full := fmt.Sprintf("[%s/%s] %s", spaceName, agentName, msg)
		s.logEvent(full)
		s.broadcastProgress(spaceName, agentName+": "+msg)
	}

	boardTimeBefore := s.agentUpdatedAt(spaceName, agentName)

	progress("sending check-in message")
	if err := acpSendMessage(s.acpConfig, sessionID, checkinPrompt); err != nil {
		result.addError(agentName + ": send failed: " + err.Error())
		return
	}

	progress(fmt.Sprintf("waiting for board post (up to %s)...", boardPollTimeout))
	if err := s.waitForBoardPost(spaceName, agentName, boardTimeBefore, boardPollTimeout); err != nil {
		result.addError(agentName + ": " + err.Error())
		return
	}
	result.addSent(agentName)
	progress("board post received")
}

func (s *Server) agentUpdatedAt(spaceName, agentName string) time.Time {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, exists := ks.Agents[agentName]
	if !exists {
		return time.Time{}
	}
	return agent.UpdatedAt
}

func (s *Server) waitForBoardPost(spaceName, agentName string, since time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(boardPollInterval)
		current := s.agentUpdatedAt(spaceName, agentName)
		if current.After(since) {
			return nil
		}
	}
	return fmt.Errorf("timed out after %s waiting for board post", timeout)
}

func (s *Server) broadcastProgress(spaceName, msg string) {
	data, _ := json.Marshal(map[string]string{"space": spaceName, "message": msg})
	s.broadcastSSE(spaceName, "broadcast_progress", string(data))
}

// BroadcastResult holds the result of a broadcast check-in operation.
type BroadcastResult struct {
	mu      sync.Mutex `json:"-"`
	Sent    []string   `json:"sent"`
	Skipped []string   `json:"skipped"`
	Errors  []string   `json:"errors"`
}

func (r *BroadcastResult) addSent(s string) {
	r.mu.Lock()
	r.Sent = append(r.Sent, s)
	r.mu.Unlock()
}

func (r *BroadcastResult) addSkipped(s string) {
	r.mu.Lock()
	r.Skipped = append(r.Skipped, s)
	r.mu.Unlock()
}

func (r *BroadcastResult) addError(s string) {
	r.mu.Lock()
	r.Errors = append(r.Errors, s)
	r.mu.Unlock()
}

// externalURL returns the URL where ACP pods can reach the boss coordinator.
func (s *Server) externalURL() string {
	if u := s.bossExternalURL; u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost" + s.port
}
