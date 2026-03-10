package coordinator

import (
	"fmt"
	"time"
)

// HealthLevel represents the health severity level
type HealthLevel string

const (
	HealthHealthy  HealthLevel = "healthy"
	HealthWarning  HealthLevel = "warning"
	HealthCritical HealthLevel = "critical"
)

// RecoveryAction defines actions to take when health issues are detected
type RecoveryAction string

const (
	ActionNotify          RecoveryAction = "notify"
	ActionNotifyAndFlag   RecoveryAction = "notify_and_flag"
	ActionStopSession     RecoveryAction = "stop_session"
	ActionRestartSession  RecoveryAction = "restart_session"
)

// HealthConfig defines health monitoring configuration for a space
type HealthConfig struct {
	HeartbeatTimeoutWarning  time.Duration                  `json:"heartbeat_timeout_warning"`  // e.g., 15m
	HeartbeatTimeoutCritical time.Duration                  `json:"heartbeat_timeout_critical"` // e.g., 30m
	BlockedTimeout           time.Duration                  `json:"blocked_timeout"`            // e.g., 2h
	RecoveryActions          map[string]RecoveryAction      `json:"recovery_actions"`           // event -> action
	Enabled                  bool                           `json:"enabled"`
}

// DefaultHealthConfig returns a sensible default health configuration
func DefaultHealthConfig() *HealthConfig {
	return &HealthConfig{
		HeartbeatTimeoutWarning:  15 * time.Minute,
		HeartbeatTimeoutCritical: 30 * time.Minute,
		BlockedTimeout:           2 * time.Hour,
		RecoveryActions: map[string]RecoveryAction{
			"heartbeat_timeout_critical": ActionNotify,
			"blocked_timeout":            ActionNotifyAndFlag,
			"session_failed":             ActionNotify,
		},
		Enabled: true,
	}
}

// HealthStatus represents the health status of an agent
type HealthStatus struct {
	Level          HealthLevel   `json:"level"`            // healthy, warning, critical
	LastSeen       time.Time     `json:"last_seen"`        // last update time
	LastChecked    time.Time     `json:"last_checked"`     // last health check time
	Issues         []string      `json:"issues,omitempty"` // list of current health issues
	Flags          []string      `json:"flags,omitempty"`  // persistent flags (e.g., "stuck_blocked")
	SessionPhase   string        `json:"session_phase,omitempty"`
	TimeSinceCheck time.Duration `json:"time_since_check"` // calculated field
}

// HealthEvent represents a health-related event in the history
type HealthEvent struct {
	Timestamp      time.Time      `json:"timestamp"`
	Agent          string         `json:"agent"`
	EventType      string         `json:"event_type"`       // heartbeat_timeout, blocked_timeout, session_failed, etc.
	Level          HealthLevel    `json:"level"`
	Message        string         `json:"message"`
	RecoveryAction RecoveryAction `json:"recovery_action,omitempty"`
}

// AgentHealth holds comprehensive health information for an agent
type AgentHealth struct {
	Status     HealthStatus   `json:"status"`
	RecentEvents []HealthEvent `json:"recent_events,omitempty"` // last N events
}

// SpaceHealthSummary provides an overview of health across all agents in a space
type SpaceHealthSummary struct {
	TotalAgents     int                        `json:"total_agents"`
	HealthyAgents   int                        `json:"healthy_agents"`
	WarningAgents   int                        `json:"warning_agents"`
	CriticalAgents  int                        `json:"critical_agents"`
	AgentHealth     map[string]*HealthStatus   `json:"agent_health"`  // agent name -> health status
	RecentEvents    []HealthEvent              `json:"recent_events"` // recent health events across all agents
	Config          *HealthConfig              `json:"config"`
}

// NewHealthStatus creates a new HealthStatus with default values
func NewHealthStatus() *HealthStatus {
	now := time.Now().UTC()
	return &HealthStatus{
		Level:          HealthHealthy,
		LastSeen:       now,
		LastChecked:    now,
		Issues:         []string{},
		Flags:          []string{},
		TimeSinceCheck: 0,
	}
}

// UpdateHealth updates the health status based on the agent's last update time and current status
func (hs *HealthStatus) UpdateHealth(lastUpdate time.Time, agentStatus AgentStatus, config *HealthConfig) {
	now := time.Now().UTC()
	hs.LastSeen = lastUpdate
	hs.LastChecked = now
	hs.TimeSinceCheck = now.Sub(lastUpdate)
	hs.Issues = []string{}

	if !config.Enabled {
		hs.Level = HealthHealthy
		return
	}

	// Check heartbeat timeout
	if hs.TimeSinceCheck >= config.HeartbeatTimeoutCritical {
		hs.Level = HealthCritical
		hs.Issues = append(hs.Issues, fmt.Sprintf("No heartbeat for %s (critical threshold: %s)",
			hs.TimeSinceCheck.Round(time.Second), config.HeartbeatTimeoutCritical))
	} else if hs.TimeSinceCheck >= config.HeartbeatTimeoutWarning {
		if hs.Level != HealthCritical {
			hs.Level = HealthWarning
		}
		hs.Issues = append(hs.Issues, fmt.Sprintf("No heartbeat for %s (warning threshold: %s)",
			hs.TimeSinceCheck.Round(time.Second), config.HeartbeatTimeoutWarning))
	}

	// Check for stuck blocked status
	if agentStatus == StatusBlocked && hs.TimeSinceCheck >= config.BlockedTimeout {
		hs.Level = HealthCritical
		hs.Issues = append(hs.Issues, fmt.Sprintf("Stuck in blocked status for %s",
			hs.TimeSinceCheck.Round(time.Second)))

		// Add persistent flag if not already present
		if !contains(hs.Flags, "stuck_blocked") {
			hs.Flags = append(hs.Flags, "stuck_blocked")
		}
	} else {
		// Remove stuck_blocked flag if recovered
		hs.Flags = removeString(hs.Flags, "stuck_blocked")
	}

	// Check for error status
	if agentStatus == StatusError {
		if hs.Level != HealthCritical {
			hs.Level = HealthWarning
		}
		hs.Issues = append(hs.Issues, "Agent in error status")
	}

	// If no issues found, mark as healthy
	if len(hs.Issues) == 0 && hs.Level != HealthHealthy {
		hs.Level = HealthHealthy
	}
}

// ComputeSpaceHealth calculates the overall health summary for a space
func ComputeSpaceHealth(ks *KnowledgeSpace) *SpaceHealthSummary {
	summary := &SpaceHealthSummary{
		TotalAgents:    len(ks.Agents),
		HealthyAgents:  0,
		WarningAgents:  0,
		CriticalAgents: 0,
		AgentHealth:    make(map[string]*HealthStatus),
		RecentEvents:   []HealthEvent{},
		Config:         ks.HealthConfig,
	}

	if ks.HealthConfig == nil {
		ks.HealthConfig = DefaultHealthConfig()
	}

	// Ensure AgentHealth map exists
	if ks.AgentHealth == nil {
		ks.AgentHealth = make(map[string]*HealthStatus)
	}

	// Update health status for each agent
	for agentName, agent := range ks.Agents {
		// Initialize health status if not exists
		if ks.AgentHealth[agentName] == nil {
			ks.AgentHealth[agentName] = NewHealthStatus()
		}

		healthStatus := ks.AgentHealth[agentName]
		healthStatus.UpdateHealth(agent.UpdatedAt, agent.Status, ks.HealthConfig)

		// Count by health level
		switch healthStatus.Level {
		case HealthHealthy:
			summary.HealthyAgents++
		case HealthWarning:
			summary.WarningAgents++
		case HealthCritical:
			summary.CriticalAgents++
		}

		summary.AgentHealth[agentName] = healthStatus
	}

	// Include recent health events (last 20)
	if len(ks.HealthEvents) > 0 {
		start := 0
		if len(ks.HealthEvents) > 20 {
			start = len(ks.HealthEvents) - 20
		}
		summary.RecentEvents = ks.HealthEvents[start:]
	}

	return summary
}

// RecordHealthEvent adds a health event to the space history
func RecordHealthEvent(ks *KnowledgeSpace, agent, eventType string, level HealthLevel, message string, action RecoveryAction) {
	if ks.HealthEvents == nil {
		ks.HealthEvents = []HealthEvent{}
	}

	event := HealthEvent{
		Timestamp:      time.Now().UTC(),
		Agent:          agent,
		EventType:      eventType,
		Level:          level,
		Message:        message,
		RecoveryAction: action,
	}

	ks.HealthEvents = append(ks.HealthEvents, event)

	// Keep only last 100 events
	if len(ks.HealthEvents) > 100 {
		ks.HealthEvents = ks.HealthEvents[len(ks.HealthEvents)-100:]
	}
}

// helper functions
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func removeString(slice []string, item string) []string {
	result := []string{}
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}
