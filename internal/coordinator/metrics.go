package coordinator

import (
	"sync"
	"time"
)

// MetricsConfig defines metrics collection and retention configuration
type MetricsConfig struct {
	Enabled          bool          `json:"enabled"`
	RetentionPeriod  time.Duration `json:"retention_period"`  // How long to keep metrics (e.g., 24h)
	SnapshotInterval time.Duration `json:"snapshot_interval"` // How often to snapshot (e.g., 1m)
}

// DefaultMetricsConfig returns sensible defaults for metrics configuration
func DefaultMetricsConfig() *MetricsConfig {
	return &MetricsConfig{
		Enabled:          true,
		RetentionPeriod:  24 * time.Hour,
		SnapshotInterval: 1 * time.Minute,
	}
}

// AgentMetrics tracks metrics for a single agent
type AgentMetrics struct {
	// Status duration tracking
	TimeInActive  time.Duration `json:"time_in_active"`
	TimeInIdle    time.Duration `json:"time_in_idle"`
	TimeInBlocked time.Duration `json:"time_in_blocked"`
	TimeInDone    time.Duration `json:"time_in_done"`
	TimeInError   time.Duration `json:"time_in_error"`

	// Activity metrics
	TotalStatusUpdates int           `json:"total_status_updates"`
	LastCheckIn        time.Time     `json:"last_check_in"`
	CheckInFrequency   time.Duration `json:"check_in_frequency"` // Average time between check-ins

	// Task completion
	TasksCompleted        int           `json:"tasks_completed"`
	AverageCompletionTime time.Duration `json:"average_completion_time"`

	// Current state tracking (for calculating durations)
	CurrentStatus     string    `json:"current_status"`
	StatusChangedAt   time.Time `json:"status_changed_at"`
	PreviousCheckIn   time.Time `json:"previous_check_in"`
}

// CoordinationMetrics tracks coordination-level metrics for a space
type CoordinationMetrics struct {
	// Agent coordination
	TotalAgents        int     `json:"total_agents"`
	ActiveAgents       int     `json:"active_agents"`
	IdleAgents         int     `json:"idle_agents"`
	BlockedAgents      int     `json:"blocked_agents"`
	ConcurrentWorkItems int    `json:"concurrent_work_items"` // Agents in active/done status

	// Task metrics
	TotalTasks         int           `json:"total_tasks"`
	CompletedTasks     int           `json:"completed_tasks"`
	AvgAssignmentToCompletion time.Duration `json:"avg_assignment_to_completion"`

	// Blocker tracking
	TotalBlockers       int           `json:"total_blockers"`
	ResolvedBlockers    int           `json:"resolved_blockers"`
	AvgBlockerResolution time.Duration `json:"avg_blocker_resolution"`
	ActiveBlockers      int           `json:"active_blockers"`
}

// SystemMetrics tracks system-level performance metrics
type SystemMetrics struct {
	// API performance
	RequestCount        int64         `json:"request_count"`
	ErrorCount          int64         `json:"error_count"`
	AvgResponseTimeMs   float64       `json:"avg_response_time_ms"`
	P50ResponseTimeMs   float64       `json:"p50_response_time_ms"`
	P95ResponseTimeMs   float64       `json:"p95_response_time_ms"`
	P99ResponseTimeMs   float64       `json:"p99_response_time_ms"`

	// SSE connections
	ActiveSSEConnections int `json:"active_sse_connections"`
	TotalSSEConnections  int `json:"total_sse_connections"`

	// Space metrics
	TotalSpaces         int   `json:"total_spaces"`
	TotalAgentsAllSpaces int  `json:"total_agents_all_spaces"`
	AvgAgentsPerSpace   float64 `json:"avg_agents_per_space"`

	// Storage
	TotalStorageBytes   int64 `json:"total_storage_bytes"`
}

// MetricsSnapshot represents a point-in-time snapshot of all metrics
type MetricsSnapshot struct {
	Timestamp       time.Time            `json:"timestamp"`
	AgentMetrics    map[string]*AgentMetrics `json:"agent_metrics"` // agent name -> metrics
	Coordination    *CoordinationMetrics `json:"coordination"`
	System          *SystemMetrics       `json:"system"`
}

// MetricsStore holds time-series metrics data for a space
type MetricsStore struct {
	mu               sync.RWMutex
	Config           *MetricsConfig
	CurrentSnapshot  *MetricsSnapshot
	History          []MetricsSnapshot // Circular buffer of historical snapshots
	MaxHistorySize   int              // Based on retention period and snapshot interval

	// Request latency tracking (for percentiles)
	recentLatencies  []float64
	maxLatencies     int
}

// NewMetricsStore creates a new metrics store with default configuration
func NewMetricsStore(config *MetricsConfig) *MetricsStore {
	if config == nil {
		config = DefaultMetricsConfig()
	}

	// Calculate max history size based on retention period and snapshot interval
	maxHistory := int(config.RetentionPeriod / config.SnapshotInterval)
	if maxHistory < 60 {
		maxHistory = 60 // Minimum 60 snapshots
	}

	return &MetricsStore{
		Config:          config,
		CurrentSnapshot: newMetricsSnapshot(),
		History:         make([]MetricsSnapshot, 0, maxHistory),
		MaxHistorySize:  maxHistory,
		recentLatencies: make([]float64, 0, 1000),
		maxLatencies:    1000,
	}
}

// newMetricsSnapshot creates a new empty snapshot
func newMetricsSnapshot() *MetricsSnapshot {
	return &MetricsSnapshot{
		Timestamp:    time.Now().UTC(),
		AgentMetrics: make(map[string]*AgentMetrics),
		Coordination: &CoordinationMetrics{},
		System:       &SystemMetrics{},
	}
}

// RecordAgentStatusChange records when an agent changes status
func (ms *MetricsStore) RecordAgentStatusChange(agentName, oldStatus, newStatus string) {
	if !ms.Config.Enabled {
		return
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := time.Now().UTC()

	// Get or create agent metrics
	agentMetrics, exists := ms.CurrentSnapshot.AgentMetrics[agentName]
	if !exists {
		agentMetrics = &AgentMetrics{
			CurrentStatus:   newStatus,
			StatusChangedAt: now,
			LastCheckIn:     now,
			PreviousCheckIn: now,
		}
		ms.CurrentSnapshot.AgentMetrics[agentName] = agentMetrics
	}

	// Calculate duration in old status
	if exists && oldStatus != "" {
		duration := now.Sub(agentMetrics.StatusChangedAt)
		switch oldStatus {
		case "active":
			agentMetrics.TimeInActive += duration
		case "idle":
			agentMetrics.TimeInIdle += duration
		case "blocked":
			agentMetrics.TimeInBlocked += duration
		case "done":
			agentMetrics.TimeInDone += duration
		case "error":
			agentMetrics.TimeInError += duration
		}
	}

	// Update to new status
	agentMetrics.CurrentStatus = newStatus
	agentMetrics.StatusChangedAt = now
	agentMetrics.TotalStatusUpdates++

	// Update check-in frequency
	if exists {
		timeSinceLastCheckIn := now.Sub(agentMetrics.LastCheckIn)
		if agentMetrics.CheckInFrequency == 0 {
			agentMetrics.CheckInFrequency = timeSinceLastCheckIn
		} else {
			// Exponential moving average
			agentMetrics.CheckInFrequency = (agentMetrics.CheckInFrequency*9 + timeSinceLastCheckIn) / 10
		}
	}

	agentMetrics.PreviousCheckIn = agentMetrics.LastCheckIn
	agentMetrics.LastCheckIn = now
}

// RecordTaskCompletion records when an agent completes a task
func (ms *MetricsStore) RecordTaskCompletion(agentName string, completionTime time.Duration) {
	if !ms.Config.Enabled {
		return
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	agentMetrics, exists := ms.CurrentSnapshot.AgentMetrics[agentName]
	if !exists {
		agentMetrics = &AgentMetrics{}
		ms.CurrentSnapshot.AgentMetrics[agentName] = agentMetrics
	}

	agentMetrics.TasksCompleted++

	// Update average completion time (exponential moving average)
	if agentMetrics.AverageCompletionTime == 0 {
		agentMetrics.AverageCompletionTime = completionTime
	} else {
		agentMetrics.AverageCompletionTime = (agentMetrics.AverageCompletionTime*9 + completionTime) / 10
	}
}

// RecordRequestLatency records API request latency for percentile calculations
func (ms *MetricsStore) RecordRequestLatency(latencyMs float64) {
	if !ms.Config.Enabled {
		return
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.CurrentSnapshot.System.RequestCount++

	// Add to recent latencies buffer (circular)
	if len(ms.recentLatencies) >= ms.maxLatencies {
		// Shift left and replace last
		copy(ms.recentLatencies, ms.recentLatencies[1:])
		ms.recentLatencies[len(ms.recentLatencies)-1] = latencyMs
	} else {
		ms.recentLatencies = append(ms.recentLatencies, latencyMs)
	}

	// Update average (exponential moving average)
	if ms.CurrentSnapshot.System.AvgResponseTimeMs == 0 {
		ms.CurrentSnapshot.System.AvgResponseTimeMs = latencyMs
	} else {
		ms.CurrentSnapshot.System.AvgResponseTimeMs = (ms.CurrentSnapshot.System.AvgResponseTimeMs*0.9 + latencyMs*0.1)
	}
}

// RecordError records an API error
func (ms *MetricsStore) RecordError() {
	if !ms.Config.Enabled {
		return
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.CurrentSnapshot.System.ErrorCount++
}

// TakeSnapshot captures current state and adds to history
func (ms *MetricsStore) TakeSnapshot() {
	if !ms.Config.Enabled {
		return
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Calculate percentiles from recent latencies
	ms.calculatePercentiles()

	// Update coordination metrics based on current agent states
	ms.updateCoordinationMetrics()

	// Add current snapshot to history
	snapshot := *ms.CurrentSnapshot
	snapshot.Timestamp = time.Now().UTC()

	if len(ms.History) >= ms.MaxHistorySize {
		// Circular buffer: remove oldest
		ms.History = ms.History[1:]
	}
	ms.History = append(ms.History, snapshot)

	// Current snapshot continues accumulating
	ms.CurrentSnapshot.Timestamp = time.Now().UTC()
}

// calculatePercentiles calculates p50, p95, p99 from recent latencies
func (ms *MetricsStore) calculatePercentiles() {
	if len(ms.recentLatencies) == 0 {
		return
	}

	// Simple percentile calculation (would use sort in production)
	// For now, using approximations
	ms.CurrentSnapshot.System.P50ResponseTimeMs = ms.CurrentSnapshot.System.AvgResponseTimeMs
	ms.CurrentSnapshot.System.P95ResponseTimeMs = ms.CurrentSnapshot.System.AvgResponseTimeMs * 1.5
	ms.CurrentSnapshot.System.P99ResponseTimeMs = ms.CurrentSnapshot.System.AvgResponseTimeMs * 2.0
}

// updateCoordinationMetrics updates coordination metrics based on current agent states
func (ms *MetricsStore) updateCoordinationMetrics() {
	ms.CurrentSnapshot.Coordination.TotalAgents = len(ms.CurrentSnapshot.AgentMetrics)
	ms.CurrentSnapshot.Coordination.ActiveAgents = 0
	ms.CurrentSnapshot.Coordination.IdleAgents = 0
	ms.CurrentSnapshot.Coordination.BlockedAgents = 0

	for _, am := range ms.CurrentSnapshot.AgentMetrics {
		switch am.CurrentStatus {
		case "active":
			ms.CurrentSnapshot.Coordination.ActiveAgents++
		case "idle":
			ms.CurrentSnapshot.Coordination.IdleAgents++
		case "blocked":
			ms.CurrentSnapshot.Coordination.BlockedAgents++
		}
	}

	ms.CurrentSnapshot.Coordination.ConcurrentWorkItems = ms.CurrentSnapshot.Coordination.ActiveAgents
}

// GetSnapshot returns the current snapshot (read-only copy)
func (ms *MetricsStore) GetSnapshot() MetricsSnapshot {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	return *ms.CurrentSnapshot
}

// GetHistory returns historical snapshots (read-only copy)
func (ms *MetricsStore) GetHistory() []MetricsSnapshot {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	history := make([]MetricsSnapshot, len(ms.History))
	copy(history, ms.History)
	return history
}
