package coordinator

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	DefaultPort      = ":8899"
	DefaultSpaceName = "default"
)

//go:embed protocol.md
var protocolTemplate string

type sseClient struct {
	ch    chan []byte
	space string
}

const (
	// broadcastCooldown is the minimum time between broadcasts for a given space.
	broadcastCooldown = 60 * time.Second
)

type Server struct {
	port            string
	dataDir         string
	bossExternalURL string
	acpConfig       *ACPConfig
	spaces          map[string]*KnowledgeSpace
	mu              sync.RWMutex
	httpServer      *http.Server
	running         bool
	runMu           sync.Mutex
	EventLog        []string
	eventMu         sync.Mutex
	stopLiveness    chan struct{}
	stopHeartbeat   chan struct{}
	sseClients      map[*sseClient]struct{}
	sseMu           sync.Mutex
	interrupts      *InterruptLedger
	broadcastLast   map[string]time.Time // space → last broadcast time
	broadcastMu     sync.Mutex
}

func NewServer(port, dataDir string) *Server {
	if port == "" {
		port = DefaultPort
	}

	var acpCfg *ACPConfig
	if u := os.Getenv("ACP_URL"); u != "" {
		model := os.Getenv("ACP_MODEL")
		if model == "" {
			model = acpDefaultModel
		}
		timeout := acpDefaultTimeout
		if t := os.Getenv("ACP_TIMEOUT"); t != "" {
			fmt.Sscanf(t, "%d", &timeout)
		}
		acpCfg = &ACPConfig{
			BaseURL: u,
			Token:   os.Getenv("ACP_TOKEN"),
			Project: os.Getenv("ACP_PROJECT"),
			Model:   model,
			Timeout: timeout,
		}
	}

	return &Server{
		port:            port,
		dataDir:         dataDir,
		bossExternalURL: os.Getenv("BOSS_EXTERNAL_URL"),
		acpConfig:       acpCfg,
		spaces:          make(map[string]*KnowledgeSpace),
		stopLiveness:    make(chan struct{}),
		stopHeartbeat:   make(chan struct{}),
		sseClients:      make(map[*sseClient]struct{}),
		interrupts:      NewInterruptLedger(dataDir),
		broadcastLast:   make(map[string]time.Time),
	}
}

func (s *Server) Running() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running
}

func (s *Server) Port() string {
	return s.port
}

func (s *Server) logEvent(msg string) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	s.EventLog = append(s.EventLog, entry)
	if len(s.EventLog) > 200 {
		s.EventLog = s.EventLog[len(s.EventLog)-200:]
	}
}

func (s *Server) RecentEvents(n int) []string {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if n > len(s.EventLog) {
		n = len(s.EventLog)
	}
	out := make([]string, n)
	copy(out, s.EventLog[len(s.EventLog)-n:])
	return out
}

func (s *Server) Start() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.running {
		return fmt.Errorf("already running")
	}

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// Protocol template is now embedded at compile time

	if err := s.loadAllSpaces(); err != nil {
		return fmt.Errorf("load spaces: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/spaces", s.handleListSpaces)
	mux.HandleFunc("/spaces/", s.handleSpaceRoute)
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/raw", func(w http.ResponseWriter, r *http.Request) {
		s.handleSpaceRaw(w, r, DefaultSpaceName)
	})
	mux.HandleFunc("/agent/", func(w http.ResponseWriter, r *http.Request) {
		agentName := strings.TrimPrefix(r.URL.Path, "/agent/")
		agentName = strings.TrimRight(agentName, "/")
		s.handleSpaceAgent(w, r, DefaultSpaceName, agentName)
	})
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		s.handleSpaceAgentsJSON(w, r, DefaultSpaceName)
	})

	listener, err := net.Listen("tcp", s.port)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.port, err)
	}
	s.port = ":" + strings.Split(listener.Addr().String(), ":")[len(strings.Split(listener.Addr().String(), ":"))-1]

	s.httpServer = &http.Server{Handler: mux}
	s.running = true

	go func() {
		s.logEvent(fmt.Sprintf("coordinator started on %s (data: %s)", s.port, s.dataDir))
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logEvent(fmt.Sprintf("server error: %v", err))
		}
	}()

	go s.livenessLoop()
	go s.heartbeatLoop()

	return nil
}

func (s *Server) Stop() error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if !s.running {
		return fmt.Errorf("not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	close(s.stopLiveness)
	close(s.stopHeartbeat)
	err := s.httpServer.Shutdown(ctx)
	s.running = false
	s.logEvent("coordinator stopped")
	return err
}

func (s *Server) spacePath(name string) string {
	return filepath.Join(s.dataDir, name+".json")
}

func (s *Server) spaceMarkdownPath(name string) string {
	return filepath.Join(s.dataDir, name+".md")
}

func (s *Server) loadAllSpaces() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		ks, err := s.loadSpace(name)
		if err != nil {
			s.logEvent(fmt.Sprintf("failed to load space %q: %v", name, err))
			continue
		}
		s.spaces[name] = ks
		s.logEvent(fmt.Sprintf("loaded space %q (%d agents)", name, len(ks.Agents)))
	}
	return nil
}

func (s *Server) loadSpace(name string) (*KnowledgeSpace, error) {
	data, err := os.ReadFile(s.spacePath(name))
	if err != nil {
		return nil, err
	}
	var ks KnowledgeSpace
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", name, err)
	}
	if ks.Agents == nil {
		ks.Agents = make(map[string]*AgentUpdate)
	}
	return &ks, nil
}

const maxBackups = 10

func (s *Server) rotateBackups(spaceName string) {
	backupDir := filepath.Join(s.dataDir, "backups")
	os.MkdirAll(backupDir, 0755)

	base := filepath.Join(backupDir, spaceName+".json")
	for i := maxBackups; i > 1; i-- {
		src := fmt.Sprintf("%s.%d", base, i-1)
		dst := fmt.Sprintf("%s.%d", base, i)
		os.Rename(src, dst)
	}

	src := s.spacePath(spaceName)
	dst := fmt.Sprintf("%s.%d", base, 1)
	data, err := os.ReadFile(src)
	if err == nil {
		os.WriteFile(dst, data, 0644)
	}
}

func (s *Server) saveSpace(ks *KnowledgeSpace) error {
	s.refreshProtocol(ks)
	data, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", ks.Name, err)
	}
	s.rotateBackups(ks.Name)
	if err := os.WriteFile(s.spacePath(ks.Name), data, 0644); err != nil {
		return err
	}
	md := ks.RenderMarkdown()
	if err := os.WriteFile(s.spaceMarkdownPath(ks.Name), []byte(md), 0644); err != nil {
		s.logEvent(fmt.Sprintf("warning: failed to write markdown for %q: %v", ks.Name, err))
	}
	return nil
}

func (s *Server) refreshProtocol(ks *KnowledgeSpace) {
	if protocolTemplate == "" {
		return
	}
	// Only set protocol if SharedContracts is empty (don't overwrite manual edits)
	if ks.SharedContracts == "" {
		ks.SharedContracts = strings.ReplaceAll(protocolTemplate, "{SPACE}", ks.Name)
	}
}

func (s *Server) getOrCreateSpace(name string) *KnowledgeSpace {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ks, ok := s.spaces[name]; ok {
		return ks
	}
	ks := NewKnowledgeSpace(name)
	s.spaces[name] = ks
	s.logEvent(fmt.Sprintf("created space %q", name))
	return ks
}


func (s *Server) getSpace(name string) (*KnowledgeSpace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks, ok := s.spaces[name]
	return ks, ok
}

func (s *Server) listSpaceNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.spaces))
	for name := range s.spaces {
		names = append(names, name)
	}
	return names
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, missionControlHTML)
}

func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type spaceSummary struct {
		Name       string    `json:"name"`
		AgentCount int       `json:"agent_count"`
		CreatedAt  time.Time `json:"created_at"`
		UpdatedAt  time.Time `json:"updated_at"`
	}

	s.mu.RLock()
	summaries := make([]spaceSummary, 0, len(s.spaces))
	for _, ks := range s.spaces {
		summaries = append(summaries, spaceSummary{
			Name:       ks.Name,
			AgentCount: len(ks.Agents),
			CreatedAt:  ks.CreatedAt,
			UpdatedAt:  ks.UpdatedAt,
		})
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleSpaceRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/spaces/")
	parts := strings.Split(path, "/")

	spaceName := parts[0]
	if spaceName == "" {
		s.handleListSpaces(w, r)
		return
	}

	if len(parts) == 1 || (len(parts) == 2 && parts[1] == "") {
		if r.Method == http.MethodDelete {
			s.handleDeleteSpace(w, r, spaceName)
			return
		}
		if r.Method == http.MethodPut {
			s.handleCreateSpace(w, r, spaceName)
			return
		}
		s.handleSpaceView(w, r, spaceName)
		return
	}

	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	subRoute := parts[1]

	switch subRoute {
	case "events":
		s.handleSpaceSSE(w, r, spaceName)
	case "raw":
		s.handleSpaceRaw(w, r, spaceName)
	case "contracts":
		s.handleSpaceContracts(w, r, spaceName)
	case "archive":
		s.handleSpaceArchive(w, r, spaceName)
	case "agent":
		if len(parts) < 3 {
			http.Error(w, "missing agent name", http.StatusBadRequest)
			return
		}
		agentName := parts[2]
		if len(parts) >= 4 {
			// Handle document path: /spaces/{space}/agent/{agent}/{slug}
			documentSlug := strings.TrimRight(parts[3], "/")
			s.handleAgentDocument(w, r, spaceName, agentName, documentSlug)
		} else {
			// Handle agent updates: /spaces/{space}/agent/{agent}
			agentName = strings.TrimRight(agentName, "/")
			s.handleSpaceAgent(w, r, spaceName, agentName)
		}
	case "api":
		if len(parts) == 3 {
			switch strings.TrimRight(parts[2], "/") {
			case "agents":
				s.handleSpaceAgentsJSON(w, r, spaceName)
			case "events":
				s.handleSpaceEventsJSON(w, r)
			case "session-status":
				s.handleSpaceSessionStatus(w, r, spaceName)
			default:
				http.NotFound(w, r)
			}
		} else {
			http.NotFound(w, r)
		}
	case "ignition":
		agentName := ""
		if len(parts) == 3 {
			agentName = strings.TrimRight(parts[2], "/")
		}
		s.handleIgnition(w, r, spaceName, agentName)
	case "broadcast":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleSingleBroadcast(w, r, spaceName, agentName)
		} else {
			s.handleBroadcast(w, r, spaceName)
		}
	case "reply":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleReplyAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "launch":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleLaunchAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "stop":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleStopAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "edit":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleEditAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "delete":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleDeleteAgent(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "metrics":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleAgentMetrics(w, r, spaceName, agentName)
		} else if len(parts) == 2 {
			// Space-level metrics (Issue #43)
			s.handleMetrics(w, r, spaceName)
		} else {
			http.Error(w, "invalid metrics path", http.StatusBadRequest)
		}
	case "transcript":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleAgentTranscript(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "dismiss":
		if len(parts) == 3 {
			agentName := strings.TrimRight(parts[2], "/")
			s.handleDismissQuestion(w, r, spaceName, agentName)
		} else {
			http.Error(w, "agent name required", http.StatusBadRequest)
		}
	case "factory":
		factorySub := ""
		if len(parts) == 3 {
			factorySub = strings.TrimRight(parts[2], "/")
		}
		switch factorySub {
		case "", "interrupts":
			s.handleInterrupts(w, r, spaceName)
		case "metrics":
			s.handleInterruptMetrics(w, r, spaceName)
		default:
			http.NotFound(w, r)
		}
	case "dashboard":
		if len(parts) == 3 && parts[2] == "metrics" {
			s.handleMetricsDashboard(w, r, spaceName)
		} else {
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSpaceView(w http.ResponseWriter, r *http.Request, spaceName string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.handleSpaceJSON(w, r, spaceName)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, missionControlHTML)
}

func (s *Server) handleSpaceJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method == http.MethodDelete {
		s.handleDeleteSpace(w, r, spaceName)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks)
}

func (s *Server) handleCreateSpace(w http.ResponseWriter, _ *http.Request, spaceName string) {
	ks := s.getOrCreateSpace(spaceName)
	s.logEvent(fmt.Sprintf("space %q created via API", spaceName))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"name": ks.Name, "status": "created"})
}

func (s *Server) handleDeleteSpace(w http.ResponseWriter, _ *http.Request, spaceName string) {
	s.mu.Lock()
	_, ok := s.spaces[spaceName]
	if !ok {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	delete(s.spaces, spaceName)
	s.mu.Unlock()

	os.Remove(s.spacePath(spaceName))
	os.Remove(s.spaceMarkdownPath(spaceName))

	s.logEvent(fmt.Sprintf("space %q deleted", spaceName))
	s.broadcastSSE(spaceName, "space_deleted", spaceName)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "deleted space %q", spaceName)
}

func (s *Server) handleSpaceRaw(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.RenderMarkdown())

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.SharedContracts = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] shared contracts updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceContracts(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.SharedContracts)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.SharedContracts = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] contracts updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceArchive(w http.ResponseWriter, r *http.Request, spaceName string) {
	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, ks.Archive)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		ks := s.getOrCreateSpace(spaceName)
		s.mu.Lock()
		ks.Archive = sanitizeInput(string(body))
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s] archive updated (%d bytes)", spaceName, len(body)))
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpaceAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if agentName == "" {
		http.Error(w, "missing agent name", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		canonical := resolveAgentName(ks, agentName)
		s.mu.RLock()
		agent, exists := ks.Agents[canonical]
		s.mu.RUnlock()
		if !exists {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "{}")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agent)

	case http.MethodPost:
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			http.Error(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			http.Error(w, fmt.Sprintf("agent %q cannot post to %q's channel", callerName, agentName), http.StatusForbidden)
			return
		}

		ks := s.getOrCreateSpace(spaceName)
		canonical := resolveAgentName(ks, agentName)

		contentType := r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var update AgentUpdate

		if strings.Contains(contentType, "application/json") {
			if err := json.Unmarshal(body, &update); err != nil {
				http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
				return
			}
		} else {
			update = AgentUpdate{
				Status:   StatusActive,
				Summary:  truncateLine(string(body), 120),
				FreeText: string(body),
			}
		}

		sanitizeAgentUpdate(&update)

		if err := update.Validate(); err != nil {
			http.Error(w, fmt.Sprintf("validation: %v", err), http.StatusBadRequest)
			return
		}

		update.UpdatedAt = time.Now().UTC()

		s.mu.Lock()
		var oldStatus string
		if existing, ok := ks.Agents[canonical]; ok {
			oldStatus = string(existing.Status)
			if update.ACPSessionID == "" && existing.ACPSessionID != "" {
				update.ACPSessionID = existing.ACPSessionID
			}
			if update.RepoURL == "" && existing.RepoURL != "" {
				update.RepoURL = existing.RepoURL
			}
		}

		// Validate status against ACP backend session state (Issue #15)
		// Don't allow agents to report "done" if their ACP session is still running
		if acpAvailable(s.acpConfig) && update.ACPSessionID != "" && update.Status == StatusDone {
			phase, err := acpGetSessionPhase(s.acpConfig, update.ACPSessionID)
			if err == nil && phase == "running" {
				s.logEvent(fmt.Sprintf("[%s/%s] overriding status 'done' to 'active' (ACP session still running)", spaceName, canonical))
				update.Status = StatusActive
			}
		}

		ks.Agents[canonical] = &update

		// Record metrics for status change (Issue #43)
		if ks.MetricsStore != nil {
			ks.MetricsStore.RecordAgentStatusChange(canonical, oldStatus, string(update.Status))
		}

		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] %s: %s", spaceName, canonical, update.Status, update.Summary))
		s.recordDecisionInterrupts(spaceName, canonical, &update)
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical, "status": string(update.Status), "summary": update.Summary})
		s.broadcastSSE(spaceName, "agent_updated", string(sseData))
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "accepted for [%s] in space %q", canonical, spaceName)

	case http.MethodDelete:
		ks, ok := s.getSpace(spaceName)
		if !ok {
			http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
			return
		}
		canonical := resolveAgentName(ks, agentName)
		s.mu.Lock()
		delete(ks.Agents, canonical)
		ks.UpdatedAt = time.Now().UTC()
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.logEvent(fmt.Sprintf("[%s/%s] agent removed", spaceName, canonical))
		sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
		s.broadcastSSE(spaceName, "agent_removed", string(sseData))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "removed [%s] from space %q", canonical, spaceName)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentDocument(w http.ResponseWriter, r *http.Request, spaceName, agentName, documentSlug string) {
	agentName = strings.TrimRight(agentName, "/")
	
	// Agent name enforcement - ensure X-Agent-Name header matches for writes
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		callerName := r.Header.Get("X-Agent-Name")
		if callerName == "" {
			http.Error(w, "missing X-Agent-Name header: agents must identify themselves", http.StatusBadRequest)
			return
		}
		if !strings.EqualFold(callerName, agentName) {
			http.Error(w, fmt.Sprintf("agent %q cannot post to %q's documents", callerName, agentName), http.StatusForbidden)
			return
		}
	}
	
	// Sanitize document slug
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(documentSlug) {
		http.Error(w, "invalid document slug: must be alphanumeric with underscores and dashes only", http.StatusBadRequest)
		return
	}

	// Create agent document directory
	agentDir := filepath.Join(s.dataDir, spaceName, agentName)
	docPath := filepath.Join(agentDir, documentSlug+".md")

	switch r.Method {
	case http.MethodGet:
		content, err := os.ReadFile(docPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "document not found", http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("read document: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Write(content)

	case http.MethodPost, http.MethodPut:
		contentType := r.Header.Get("Content-Type")
		if !strings.Contains(contentType, "text/markdown") && !strings.Contains(contentType, "text/plain") {
			http.Error(w, "Content-Type must be text/markdown or text/plain", http.StatusBadRequest)
			return
		}

		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Create agent directory if it doesn't exist
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			http.Error(w, fmt.Sprintf("create directory: %v", err), http.StatusInternalServerError)
			return
		}

		// Write document
		if err := os.WriteFile(docPath, content, 0644); err != nil {
			http.Error(w, fmt.Sprintf("write document: %v", err), http.StatusInternalServerError)
			return
		}

		// Update agent's documents list in the knowledge space
		ks := s.getOrCreateSpace(spaceName)
		canonical := resolveAgentName(ks, agentName)
		
		s.mu.Lock()
		if ks.Agents[canonical] == nil {
			ks.Agents[canonical] = &AgentUpdate{
				Status: StatusActive,
				Summary: "Document uploaded",
				UpdatedAt: time.Now().UTC(),
			}
		}
		
		agent := ks.Agents[canonical]
		
		// Add or update document in the list
		found := false
		for i, doc := range agent.Documents {
			if doc.Slug == documentSlug {
				agent.Documents[i].Content = string(content)
				found = true
				break
			}
		}
		if !found {
			agent.Documents = append(agent.Documents, AgentDocument{
				Slug:    documentSlug,
				Title:   documentSlug, // Default title is the slug, agents can override via JSON
				Content: string(content),
			})
		}
		
		agent.UpdatedAt = time.Now().UTC()
		ks.UpdatedAt = time.Now().UTC()
		
		if err := s.saveSpace(ks); err != nil {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf("save space: %v", err), http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()

		s.logEvent(fmt.Sprintf("[%s/%s] document %q uploaded", spaceName, canonical, documentSlug))
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "document %q saved for [%s] in space %q", documentSlug, canonical, spaceName)

	case http.MethodDelete:
		if err := os.Remove(docPath); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "document not found", http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("delete document: %v", err), http.StatusInternalServerError)
			return
		}

		// Remove document from agent's list
		ks, ok := s.getSpace(spaceName)
		if ok {
			canonical := resolveAgentName(ks, agentName)
			s.mu.Lock()
			if agent := ks.Agents[canonical]; agent != nil {
				for i, doc := range agent.Documents {
					if doc.Slug == documentSlug {
						agent.Documents = append(agent.Documents[:i], agent.Documents[i+1:]...)
						break
					}
				}
				agent.UpdatedAt = time.Now().UTC()
				ks.UpdatedAt = time.Now().UTC()
				s.saveSpace(ks)
			}
			s.mu.Unlock()
		}

		s.logEvent(fmt.Sprintf("[%s/%s] document %q deleted", spaceName, agentName, documentSlug))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "document %q deleted", documentSlug)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// buildIgnition generates the full ignition document for an agent.
// Caller must ensure the space exists (e.g. via getOrCreateSpace).
// Acquires s.mu.RLock internally.
func (s *Server) buildIgnition(spaceName, agentName string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bossURL := s.externalURL()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Agent Ignition: %s\n\n", agentName))
	b.WriteString(fmt.Sprintf("You are **%s**, an agent working in workspace **%s**.\n\n", agentName, spaceName))

	b.WriteString("## Coordinator\n\n")
	b.WriteString(fmt.Sprintf("- Boss URL: `%s` (also available as `$BOSS_URL` env var)\n", bossURL))
	b.WriteString(fmt.Sprintf("- Workspace: `%s` (also available as `$BOSS_SPACE` env var)\n", spaceName))
	b.WriteString(fmt.Sprintf("- Your agent name: `%s` (also available as `$BOSS_AGENT` env var)\n", agentName))
	b.WriteString(fmt.Sprintf("- Your channel: `POST /spaces/%s/agent/%s`\n", spaceName, agentName))
	b.WriteString(fmt.Sprintf("- Read blackboard: `GET /spaces/%s/raw`\n", spaceName))
	b.WriteString(fmt.Sprintf("- Dashboard: `%s/spaces/%s/`\n", bossURL, spaceName))
	b.WriteString("\n")

	b.WriteString("## Protocol\n\n")
	b.WriteString("1. **Read before write.** GET /raw first to see what others are doing.\n")
	b.WriteString(fmt.Sprintf("2. **Post to your channel only.** POST to `/spaces/%s/agent/%s` with `-H 'X-Agent-Name: %s'`.\n", spaceName, agentName, agentName))
	b.WriteString("3. **Tag questions** with `[?BOSS]` — they render highlighted in the dashboard.\n")
	b.WriteString("4. **Include location fields** in every POST: `branch`, `pr`, `test_count`.\n")
	b.WriteString("5. **Use environment variables.** `$BOSS_URL`, `$BOSS_SPACE`, and `$BOSS_AGENT` are injected by the platform.\n")
	b.WriteString("\n")

	// Need the space object for peer agents and previous state — look it up under the lock we already hold.
	ks := s.spaces[spaceName]

	b.WriteString("## Peer Agents\n\n")
	if ks == nil || len(ks.Agents) == 0 {
		b.WriteString("No agents have posted yet.\n\n")
	} else {
		b.WriteString("| Agent | Status | Summary |\n")
		b.WriteString("| ----- | ------ | ------- |\n")
		for name, agent := range ks.Agents {
			b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", name, agent.Status, agent.Summary))
		}
		b.WriteString("\n")
	}

	if ks != nil {
		canonical := resolveAgentName(ks, agentName)
		existing, hasExisting := ks.Agents[canonical]
		if hasExisting {
			b.WriteString("## Your Last State\n\n")
			b.WriteString(fmt.Sprintf("- Status: %s\n", existing.Status))
			b.WriteString(fmt.Sprintf("- Summary: %s\n", existing.Summary))
			if existing.Branch != "" {
				b.WriteString(fmt.Sprintf("- Branch: `%s`\n", existing.Branch))
			}
			if existing.PR != "" {
				b.WriteString(fmt.Sprintf("- PR: %s\n", existing.PR))
			}
			if existing.Phase != "" {
				b.WriteString(fmt.Sprintf("- Phase: %s\n", existing.Phase))
			}
			if existing.NextSteps != "" {
				b.WriteString(fmt.Sprintf("- Next steps: %s\n", existing.NextSteps))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## JSON Post Template\n\n")
	b.WriteString("```bash\n")
	b.WriteString(fmt.Sprintf("curl -s -X POST %s/spaces/%s/agent/%s \\\n", bossURL, spaceName, agentName))
	b.WriteString("  -H 'Content-Type: application/json' \\\n")
	b.WriteString(fmt.Sprintf("  -H 'X-Agent-Name: %s' \\\n", agentName))
	b.WriteString("  -d '{\n")
	b.WriteString("    \"status\": \"active\",\n")
	b.WriteString(fmt.Sprintf("    \"summary\": \"%s: working on ...\",\n", agentName))
	b.WriteString("    \"branch\": \"feat/...\",\n")
	b.WriteString("    \"items\": [\"...\"]\n")
	b.WriteString("  }'\n")
	b.WriteString("```\n")

	return b.String()
}

func (s *Server) handleIgnition(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if agentName == "" {
		http.Error(w, "missing agent name: GET /spaces/{space}/ignition/{agent}", http.StatusBadRequest)
		return
	}

	s.getOrCreateSpace(spaceName)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, s.buildIgnition(spaceName, agentName))
}

// tryAcquireBroadcast checks if a broadcast is allowed for the given key
// (space name or space/agent). Returns true and records the timestamp if
// the cooldown has elapsed; returns false and the remaining wait time otherwise.
func (s *Server) tryAcquireBroadcast(key string) (bool, time.Duration) {
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
	now := time.Now()
	if last, ok := s.broadcastLast[key]; ok {
		remaining := broadcastCooldown - now.Sub(last)
		if remaining > 0 {
			return false, remaining
		}
	}
	s.broadcastLast[key] = now
	return true, 0
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ok, remaining := s.tryAcquireBroadcast(spaceName)
	if !ok {
		s.logEvent(fmt.Sprintf("[%s] broadcast rejected: cooldown %s remaining", spaceName, remaining.Round(time.Second)))
		http.Error(w, fmt.Sprintf("broadcast cooldown: retry after %s", remaining.Round(time.Second)), http.StatusTooManyRequests)
		return
	}

	go func() {
		result := s.BroadcastCheckIn(spaceName)
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "broadcast initiated for space %q", spaceName)
}

func (s *Server) handleSingleBroadcast(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := spaceName + "/" + agentName
	ok, remaining := s.tryAcquireBroadcast(key)
	if !ok {
		s.logEvent(fmt.Sprintf("[%s/%s] check-in rejected: cooldown %s remaining", spaceName, agentName, remaining.Round(time.Second)))
		http.Error(w, fmt.Sprintf("check-in cooldown: retry after %s", remaining.Round(time.Second)), http.StatusTooManyRequests)
		return
	}

	go func() {
		result := s.SingleAgentCheckIn(spaceName, agentName)
		sseData, _ := json.Marshal(result)
		s.broadcastSSE(spaceName, "broadcast_complete", string(sseData))
	}()

	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, "check-in initiated for agent %q in space %q", agentName, spaceName)
}

func (s *Server) handleSpaceAgentsJSON(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ks.Agents)
}

func (s *Server) handleSpaceEventsJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	events := s.RecentEvents(50)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

type sessionAgentStatus struct {
	Agent        string `json:"agent"`
	ACPSessionID string `json:"acp_session_id,omitempty"`
	Registered   bool   `json:"registered"`
	Phase        string `json:"phase,omitempty"`
}

func (s *Server) handleSpaceSessionStatus(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	if acpAvailable(s.acpConfig) {
		s.ACPAutoDiscover(spaceName)
	}

	s.mu.RLock()
	type agentEntry struct {
		name      string
		sessionID string
	}
	var entries []agentEntry
	for name, agent := range ks.Agents {
		entries = append(entries, agentEntry{name: name, sessionID: agent.ACPSessionID})
	}
	s.mu.RUnlock()

	var results []sessionAgentStatus
	for _, e := range entries {
		st := sessionAgentStatus{
			Agent:        e.name,
			ACPSessionID: e.sessionID,
			Registered:   e.sessionID != "",
		}
		if acpAvailable(s.acpConfig) && st.Registered {
			if phase, err := acpGetSessionPhase(s.acpConfig, e.sessionID); err == nil {
				st.Phase = phase
			}
		}
		results = append(results, st)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) handleReplyAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	var acpSessionID string
	if exists {
		acpSessionID = agent.ACPSessionID
	}
	s.mu.RUnlock()
	if !exists {
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if acpSessionID == "" {
		http.Error(w, canonical+": no ACP session registered", http.StatusBadRequest)
		return
	}
	if !acpAvailable(s.acpConfig) {
		http.Error(w, "ACP not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Message) == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if err := acpSendMessage(s.acpConfig, acpSessionID, payload.Message); err != nil {
		http.Error(w, canonical+": send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logEvent(fmt.Sprintf("[%s/%s] boss reply sent via dashboard", spaceName, canonical))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent", "agent": canonical})
}

func (s *Server) handleLaunchAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !acpAvailable(s.acpConfig) {
		http.Error(w, "ACP not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Prompt string   `json:"prompt"`
		Repos  []string `json:"repos,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Register the session ID with the agent
	ks := s.getOrCreateSpace(spaceName)
	canonical := resolveAgentName(ks, agentName)

	ignition := s.buildIgnition(spaceName, canonical)
	// If no prompt provided, use a default message to resume previous work
	taskPrompt := strings.TrimSpace(payload.Prompt)
	if taskPrompt == "" {
		taskPrompt = "Resume your previous work."
	}
	fullPrompt := taskPrompt + "\n\n---\n\n" + ignition

	sessionID, err := acpCreateSession(s.acpConfig, canonical, spaceName, fullPrompt, payload.Repos)
	if err != nil {
		http.Error(w, "create session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if ks.Agents[canonical] == nil {
		ks.Agents[canonical] = &AgentUpdate{
			Status:     StatusIdle,
			Summary:    canonical + ": ACP session launched",
			TaskPrompt: taskPrompt,
			RepoList:   payload.Repos,
			UpdatedAt:  time.Now().UTC(),
		}
	}
	ks.Agents[canonical].ACPSessionID = sessionID
	ks.Agents[canonical].TaskPrompt = taskPrompt
	ks.Agents[canonical].RepoList = payload.Repos
	ks.UpdatedAt = time.Now().UTC()
	s.saveSpace(ks)
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] ACP session launched: %s", spaceName, canonical, sessionID))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical, "session_id": sessionID})
	s.broadcastSSE(spaceName, "agent_launched", string(sseData))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"session_id": sessionID, "agent": canonical, "status": "launching"})
}

func (s *Server) handleStopAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
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
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID != "" && acpAvailable(s.acpConfig) {
		if err := acpStopSession(s.acpConfig, sessionID); err != nil {
			http.Error(w, "stop session: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	agent.Phase = "idle"
	agent.UpdatedAt = time.Now().UTC()
	ks.Agents[canonical] = agent
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()
	s.logEvent(fmt.Sprintf("[%s/%s] agent stopped: %s", spaceName, canonical, sessionID))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, "agent_stopped", string(sseData))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "agent": canonical, "session_id": sessionID})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
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
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID != "" && acpAvailable(s.acpConfig) {
		if err := acpDeleteSession(s.acpConfig, sessionID); err != nil {
			http.Error(w, "delete session: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	delete(ks.Agents, canonical)
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()
	s.logEvent(fmt.Sprintf("[%s/%s] agent deleted: %s", spaceName, canonical, sessionID))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, "agent_removed", string(sseData))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "agent": canonical, "session_id": sessionID})
}

func (s *Server) handleEditAgent(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	s.mu.RLock()
	canonical := resolveAgentName(ks, agentName)
	_, exists := ks.Agents[canonical]
	s.mu.RUnlock()
	if !exists {
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		TaskPrompt        string   `json:"task_prompt"`
		RepoList          []string `json:"repo_list"`
		HeartbeatInterval *int     `json:"heartbeat_interval,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	agent := ks.Agents[canonical]
	agent.TaskPrompt = payload.TaskPrompt
	agent.RepoList = payload.RepoList
	if payload.HeartbeatInterval != nil {
		agent.HeartbeatInterval = *payload.HeartbeatInterval
	}
	agent.UpdatedAt = time.Now().UTC()
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("save: %v", err), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] agent details updated", spaceName, canonical))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, "agent_updated", string(sseData))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated", "agent": canonical})
}

func (s *Server) handleAgentMetrics(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !acpAvailable(s.acpConfig) {
		http.Error(w, "ACP not configured", http.StatusServiceUnavailable)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
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
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID == "" {
		http.Error(w, canonical+": no ACP session registered", http.StatusBadRequest)
		return
	}
	metrics, err := acpGetMetrics(s.acpConfig, sessionID)
	if err != nil {
		http.Error(w, "get metrics: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func (s *Server) handleAgentTranscript(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !acpAvailable(s.acpConfig) {
		http.Error(w, "ACP not configured", http.StatusServiceUnavailable)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
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
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	if sessionID == "" {
		http.Error(w, canonical+": no ACP session registered", http.StatusBadRequest)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	transcript, err := acpGetTranscript(s.acpConfig, sessionID, format)
	if err != nil {
		http.Error(w, "get transcript: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(transcript)
}

func (s *Server) handleDismissQuestion(w http.ResponseWriter, r *http.Request, spaceName, agentName string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var payload struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	canonical := resolveAgentName(ks, agentName)
	agent, exists := ks.Agents[canonical]
	if !exists {
		s.mu.Unlock()
		http.Error(w, "agent not found: "+agentName, http.StatusNotFound)
		return
	}
	switch payload.Type {
	case "question":
		if payload.Index < 0 || payload.Index >= len(agent.Questions) {
			s.mu.Unlock()
			http.Error(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Questions = append(agent.Questions[:payload.Index], agent.Questions[payload.Index+1:]...)
	case "blocker":
		if payload.Index < 0 || payload.Index >= len(agent.Blockers) {
			s.mu.Unlock()
			http.Error(w, "index out of range", http.StatusBadRequest)
			return
		}
		agent.Blockers = append(agent.Blockers[:payload.Index], agent.Blockers[payload.Index+1:]...)
	default:
		s.mu.Unlock()
		http.Error(w, "type must be 'question' or 'blocker'", http.StatusBadRequest)
		return
	}
	ks.UpdatedAt = time.Now().UTC()
	if err := s.saveSpace(ks); err != nil {
		s.mu.Unlock()
		http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.logEvent(fmt.Sprintf("[%s/%s] boss dismissed %s #%d via dashboard", spaceName, canonical, payload.Type, payload.Index))
	sseData, _ := json.Marshal(map[string]string{"space": spaceName, "agent": canonical})
	s.broadcastSSE(spaceName, "agent_updated", string(sseData))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "dismissed", "agent": canonical})
}

func (s *Server) broadcastSSE(space, event, data string) {
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	payload := []byte(msg)
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for c := range s.sseClients {
		if c.space == "" || c.space == space {
			select {
			case c.ch <- payload:
			default:
			}
		}
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	s.serveSSE(w, r, "")
}

func (s *Server) handleSpaceSSE(w http.ResponseWriter, r *http.Request, spaceName string) {
	s.serveSSE(w, r, spaceName)
}

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, space string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := &sseClient{ch: make(chan []byte, 64), space: space}
	s.sseMu.Lock()
	s.sseClients[client] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, client)
		s.sseMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-client.ch:
			w.Write(msg)
			flusher.Flush()
		}
	}
}

func (s *Server) livenessLoop() {
	// Poll ACP session phases every 30 seconds (not every second — ACP is remote)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopLiveness:
			return
		case <-ticker.C:
			s.checkAllSessionLiveness()
		}
	}
}

func (s *Server) heartbeatLoop() {
	// Check heartbeat intervals every 60 seconds
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopHeartbeat:
			return
		case <-ticker.C:
			s.checkHeartbeats()
		}
	}
}

func (s *Server) checkHeartbeats() {
	if !acpAvailable(s.acpConfig) {
		return
	}
	s.mu.RLock()
	type heartbeatCheck struct {
		space, agent   string
		interval       int
		lastUpdate     time.Time
		acpSessionID   string
	}
	var checks []heartbeatCheck
	now := time.Now().UTC()
	for spaceName, ks := range s.spaces {
		for agentName, agent := range ks.Agents {
			if agent.HeartbeatInterval > 0 && agent.ACPSessionID != "" {
				checks = append(checks, heartbeatCheck{
					space:        spaceName,
					agent:        agentName,
					interval:     agent.HeartbeatInterval,
					lastUpdate:   agent.UpdatedAt,
					acpSessionID: agent.ACPSessionID,
				})
			}
		}
	}
	s.mu.RUnlock()

	for _, check := range checks {
		elapsed := now.Sub(check.lastUpdate)
		threshold := time.Duration(check.interval) * time.Minute
		if elapsed >= threshold {
			s.sendHeartbeatCheckIn(check.space, check.agent, check.acpSessionID)
		}
	}
}

func (s *Server) sendHeartbeatCheckIn(spaceName, agentName, sessionID string) {
	if s.bossExternalURL == "" {
		return
	}
	message := fmt.Sprintf("BOSS CHECK-IN: Read the blackboard at %s/spaces/%s/raw then POST your current status following the protocol. Include status, summary, branch, pr, items, next_steps. Resume your previous work after.",
		s.bossExternalURL, url.PathEscape(spaceName))

	if err := acpSendMessage(s.acpConfig, sessionID, message); err != nil {
		s.logEvent(fmt.Sprintf("[%s/%s] heartbeat check-in failed: %v", spaceName, agentName, err))
	} else {
		s.logEvent(fmt.Sprintf("[%s/%s] heartbeat check-in sent (interval: %dmin)", spaceName, agentName, 0))
	}
}

func (s *Server) checkAllSessionLiveness() {
	if !acpAvailable(s.acpConfig) {
		return
	}
	s.mu.RLock()
	type probe struct {
		space, agent, sessionID string
	}
	var probes []probe
	for spaceName, ks := range s.spaces {
		for name, agent := range ks.Agents {
			if agent.ACPSessionID != "" {
				probes = append(probes, probe{spaceName, name, agent.ACPSessionID})
			}
		}
	}
	s.mu.RUnlock()

	type statusEntry struct {
		agent, sessionID string
		phase            string
	}
	spaceResults := make(map[string][]statusEntry)
	for _, p := range probes {
		phase, err := acpGetSessionPhase(s.acpConfig, p.sessionID)
		if err != nil {
			continue
		}
		spaceResults[p.space] = append(spaceResults[p.space], statusEntry{
			agent:     p.agent,
			sessionID: p.sessionID,
			phase:     phase,
		})
	}

	for space, entries := range spaceResults {
		payload := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			payload[i] = map[string]interface{}{
				"agent":      e.agent,
				"session_id": e.sessionID,
				"phase":      e.phase,
			}
		}
		data, _ := json.Marshal(payload)
		s.broadcastSSE(space, "session_liveness", string(data))
	}
}

func (s *Server) recordDecisionInterrupts(spaceName, agentName string, update *AgentUpdate) {
	for _, q := range update.Questions {
		ctx := map[string]string{}
		if update.Branch != "" {
			ctx["branch"] = update.Branch
		}
		if update.PR != "" {
			ctx["pr"] = update.PR
		}
		if update.Phase != "" {
			ctx["phase"] = update.Phase
		}
		s.interrupts.Record(spaceName, agentName, InterruptDecision, q, ctx)
	}
}

func (s *Server) handleInterrupts(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	interrupts := s.interrupts.LoadAll(spaceName)
	if interrupts == nil {
		interrupts = []Interrupt{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(interrupts)
}

func (s *Server) handleInterruptMetrics(w http.ResponseWriter, r *http.Request, spaceName string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	metrics := s.interrupts.Metrics(spaceName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func resolveAgentName(ks *KnowledgeSpace, raw string) string {
	for existing := range ks.Agents {
		if strings.EqualFold(existing, raw) {
			return existing
		}
	}
	return strings.ToUpper(raw[:1]) + strings.ToLower(raw[1:])
}

var devNullPattern = regexp.MustCompile(`\s*<\s*/dev/null\s*`)

func sanitizeInput(s string) string {
	return devNullPattern.ReplaceAllString(s, "")
}

func sanitizeAgentUpdate(u *AgentUpdate) {
	u.Summary = sanitizeInput(u.Summary)
	u.Phase = sanitizeInput(u.Phase)
	u.FreeText = sanitizeInput(u.FreeText)
	u.NextSteps = sanitizeInput(u.NextSteps)
	for i, item := range u.Items {
		u.Items[i] = sanitizeInput(item)
	}
	for i, q := range u.Questions {
		u.Questions[i] = sanitizeInput(q)
	}
	for i, b := range u.Blockers {
		u.Blockers[i] = sanitizeInput(b)
	}
	for si := range u.Sections {
		u.Sections[si].Title = sanitizeInput(u.Sections[si].Title)
		for i, item := range u.Sections[si].Items {
			u.Sections[si].Items[i] = sanitizeInput(item)
		}
	}
}

func truncateLine(s string, maxLen int) string {
	line := strings.SplitN(s, "\n", 2)[0]
	line = strings.TrimSpace(line)
	if len(line) > maxLen {
		return line[:maxLen-3] + "..."
	}
	return line
}

// handleMetrics serves metrics data as JSON (Issue #43)
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request, spaceName string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	if ks.MetricsStore == nil {
		http.Error(w, "metrics not enabled for this space", http.StatusServiceUnavailable)
		return
	}

	// Get current snapshot and history
	snapshot := ks.MetricsStore.GetSnapshot()
	history := ks.MetricsStore.GetHistory()

	response := map[string]interface{}{
		"space":             spaceName,
		"timestamp":         time.Now().UTC(),
		"current_snapshot":  snapshot,
		"history_count":     len(history),
		"history":           history,
		"retention_period":  ks.MetricsStore.Config.RetentionPeriod.String(),
		"snapshot_interval": ks.MetricsStore.Config.SnapshotInterval.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleMetricsDashboard serves the metrics dashboard HTML (Issue #43)
func (s *Server) handleMetricsDashboard(w http.ResponseWriter, r *http.Request, spaceName string) {
	ks, ok := s.getSpace(spaceName)
	if !ok {
		http.Error(w, fmt.Sprintf("space %q not found", spaceName), http.StatusNotFound)
		return
	}

	if ks.MetricsStore == nil {
		http.Error(w, "metrics not enabled for this space", http.StatusServiceUnavailable)
		return
	}

	// Simple HTML dashboard (will be enhanced with Chart.js)
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Metrics Dashboard - %s</title>
	<style>
		body {
			font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
			margin: 0;
			padding: 20px;
			background: #f5f5f5;
		}
		.container {
			max-width: 1200px;
			margin: 0 auto;
		}
		h1 {
			color: #333;
		}
		.metrics-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
			gap: 20px;
			margin-top: 20px;
		}
		.metric-card {
			background: white;
			border-radius: 8px;
			padding: 20px;
			box-shadow: 0 2px 4px rgba(0,0,0,0.1);
		}
		.metric-card h2 {
			margin: 0 0 15px 0;
			font-size: 18px;
			color: #666;
		}
		.metric-value {
			font-size: 32px;
			font-weight: bold;
			color: #333;
		}
		.metric-label {
			font-size: 14px;
			color: #888;
			margin-top: 5px;
		}
		.agent-list {
			list-style: none;
			padding: 0;
		}
		.agent-list li {
			padding: 8px;
			border-bottom: 1px solid #eee;
			display: flex;
			justify-content: space-between;
		}
		.status-active { color: #4caf50; }
		.status-idle { color: #ff9800; }
		.status-blocked { color: #f44336; }
	</style>
</head>
<body>
	<div class="container">
		<h1>Metrics Dashboard: %s</h1>
		<div class="metrics-grid" id="metrics-grid">
			<div class="metric-card">
				<h2>Loading metrics...</h2>
			</div>
		</div>
	</div>
	<script>
		// Fetch and display metrics
		fetch('/spaces/%s/metrics')
			.then(response => response.json())
			.then(data => {
				const grid = document.getElementById('metrics-grid');
				grid.innerHTML = '';

				// Coordination metrics
				const coord = data.current_snapshot.coordination;
				let html = '<div class="metric-card"><h2>Active Agents</h2>';
				html += '<div class="metric-value">' + coord.active_agents + '</div>';
				html += '<div class="metric-label">of ' + coord.total_agents + ' total</div></div>';

				html += '<div class="metric-card"><h2>Idle Agents</h2>';
				html += '<div class="metric-value">' + coord.idle_agents + '</div></div>';

				html += '<div class="metric-card"><h2>Blocked Agents</h2>';
				html += '<div class="metric-value">' + coord.blocked_agents + '</div></div>';

				html += '<div class="metric-card"><h2>Concurrent Work</h2>';
				html += '<div class="metric-value">' + coord.concurrent_work_items + '</div>';
				html += '<div class="metric-label">active tasks</div></div>';

				// System metrics
				const sys = data.current_snapshot.system;
				html += '<div class="metric-card"><h2>Request Count</h2>';
				html += '<div class="metric-value">' + sys.request_count + '</div></div>';

				html += '<div class="metric-card"><h2>Error Count</h2>';
				html += '<div class="metric-value">' + sys.error_count + '</div></div>';

				html += '<div class="metric-card"><h2>Avg Response Time</h2>';
				html += '<div class="metric-value">' + sys.avg_response_time_ms.toFixed(2) + 'ms</div></div>';

				html += '<div class="metric-card"><h2>P95 Response Time</h2>';
				html += '<div class="metric-value">' + sys.p95_response_time_ms.toFixed(2) + 'ms</div></div>';

				// Agent metrics
				const agents = data.current_snapshot.agent_metrics;
				if (agents && Object.keys(agents).length > 0) {
					html += '<div class="metric-card" style="grid-column: 1 / -1;"><h2>Agent Metrics</h2><ul class="agent-list">';
					for (const [name, metrics] of Object.entries(agents)) {
						const statusClass = 'status-' + metrics.current_status;
						html += '<li><span>' + name + '</span>';
						html += '<span class="' + statusClass + '">' + metrics.current_status;
						html += ' (' + metrics.total_status_updates + ' updates)</span></li>';
					}
					html += '</ul></div>';
				}

				grid.innerHTML = html;
			})
			.catch(err => {
				document.getElementById('metrics-grid').innerHTML =
					'<div class="metric-card"><h2>Error loading metrics</h2><p>' + err + '</p></div>';
			});

		// Auto-refresh every 30 seconds
		setInterval(() => location.reload(), 30000);
	</script>
</body>
</html>`, spaceName, spaceName, spaceName)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}
