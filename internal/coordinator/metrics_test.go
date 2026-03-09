package coordinator

import (
	"testing"
	"time"
)

func TestNewMetricsStore(t *testing.T) {
	store := NewMetricsStore(nil)
	if store == nil {
		t.Fatal("NewMetricsStore returned nil")
	}
	if store.Config == nil {
		t.Fatal("MetricsStore.Config is nil")
	}
	if !store.Config.Enabled {
		t.Error("Metrics should be enabled by default")
	}
	if store.CurrentSnapshot == nil {
		t.Fatal("CurrentSnapshot is nil")
	}
}

func TestDefaultMetricsConfig(t *testing.T) {
	cfg := DefaultMetricsConfig()
	if cfg == nil {
		t.Fatal("DefaultMetricsConfig returned nil")
	}
	if !cfg.Enabled {
		t.Error("Default config should be enabled")
	}
	if cfg.RetentionPeriod != 24*time.Hour {
		t.Errorf("Expected retention period 24h, got %v", cfg.RetentionPeriod)
	}
	if cfg.SnapshotInterval != 1*time.Minute {
		t.Errorf("Expected snapshot interval 1m, got %v", cfg.SnapshotInterval)
	}
}

func TestRecordAgentStatusChange(t *testing.T) {
	store := NewMetricsStore(nil)

	// Record initial status
	store.RecordAgentStatusChange("test-agent", "", "idle")

	// Verify agent metrics created
	metrics, exists := store.CurrentSnapshot.AgentMetrics["test-agent"]
	if !exists {
		t.Fatal("Agent metrics not created")
	}
	if metrics.CurrentStatus != "idle" {
		t.Errorf("Expected status 'idle', got %s", metrics.CurrentStatus)
	}
	if metrics.TotalStatusUpdates != 1 {
		t.Errorf("Expected 1 status update, got %d", metrics.TotalStatusUpdates)
	}

	// Record status change
	time.Sleep(10 * time.Millisecond)
	store.RecordAgentStatusChange("test-agent", "idle", "active")

	// Verify status changed and time tracked
	if metrics.CurrentStatus != "active" {
		t.Errorf("Expected status 'active', got %s", metrics.CurrentStatus)
	}
	if metrics.TotalStatusUpdates != 2 {
		t.Errorf("Expected 2 status updates, got %d", metrics.TotalStatusUpdates)
	}
	if metrics.TimeInIdle == 0 {
		t.Error("TimeInIdle should be greater than 0")
	}
}

func TestRecordTaskCompletion(t *testing.T) {
	store := NewMetricsStore(nil)

	// Record task completion
	completionTime := 5 * time.Minute
	store.RecordTaskCompletion("test-agent", completionTime)

	metrics, exists := store.CurrentSnapshot.AgentMetrics["test-agent"]
	if !exists {
		t.Fatal("Agent metrics not created")
	}
	if metrics.TasksCompleted != 1 {
		t.Errorf("Expected 1 task completed, got %d", metrics.TasksCompleted)
	}
	if metrics.AverageCompletionTime != completionTime {
		t.Errorf("Expected avg completion time %v, got %v", completionTime, metrics.AverageCompletionTime)
	}

	// Record another task
	completionTime2 := 3 * time.Minute
	store.RecordTaskCompletion("test-agent", completionTime2)

	if metrics.TasksCompleted != 2 {
		t.Errorf("Expected 2 tasks completed, got %d", metrics.TasksCompleted)
	}
	// Average should be updated (exponential moving average)
	if metrics.AverageCompletionTime == completionTime {
		t.Error("Average completion time should have been updated")
	}
}

func TestRecordRequestLatency(t *testing.T) {
	store := NewMetricsStore(nil)

	// Record some latencies
	store.RecordRequestLatency(10.5)
	store.RecordRequestLatency(20.3)
	store.RecordRequestLatency(15.7)

	sys := store.CurrentSnapshot.System
	if sys.RequestCount != 3 {
		t.Errorf("Expected 3 requests, got %d", sys.RequestCount)
	}
	if sys.AvgResponseTimeMs == 0 {
		t.Error("AvgResponseTimeMs should be greater than 0")
	}
}

func TestRecordError(t *testing.T) {
	store := NewMetricsStore(nil)

	store.RecordError()
	store.RecordError()

	if store.CurrentSnapshot.System.ErrorCount != 2 {
		t.Errorf("Expected 2 errors, got %d", store.CurrentSnapshot.System.ErrorCount)
	}
}

func TestTakeSnapshot(t *testing.T) {
	store := NewMetricsStore(nil)

	// Add some metrics
	store.RecordAgentStatusChange("agent1", "", "active")
	store.RecordAgentStatusChange("agent2", "", "idle")
	store.RecordRequestLatency(10.0)

	// Take snapshot
	store.TakeSnapshot()

	// Verify snapshot added to history
	if len(store.History) != 1 {
		t.Errorf("Expected 1 snapshot in history, got %d", len(store.History))
	}

	snapshot := store.History[0]
	if len(snapshot.AgentMetrics) != 2 {
		t.Errorf("Expected 2 agents in snapshot, got %d", len(snapshot.AgentMetrics))
	}
}

func TestGetSnapshot(t *testing.T) {
	store := NewMetricsStore(nil)

	store.RecordAgentStatusChange("test-agent", "", "active")

	snapshot := store.GetSnapshot()
	if len(snapshot.AgentMetrics) != 1 {
		t.Errorf("Expected 1 agent in snapshot, got %d", len(snapshot.AgentMetrics))
	}

	// Verify snapshot contains correct data
	agentMetrics, exists := snapshot.AgentMetrics["test-agent"]
	if !exists {
		t.Fatal("test-agent not found in snapshot")
	}
	if agentMetrics.CurrentStatus != "active" {
		t.Errorf("Expected status 'active', got %s", agentMetrics.CurrentStatus)
	}
}

func TestGetHistory(t *testing.T) {
	store := NewMetricsStore(nil)

	store.RecordAgentStatusChange("agent1", "", "active")
	store.TakeSnapshot()

	store.RecordAgentStatusChange("agent2", "", "idle")
	store.TakeSnapshot()

	history := store.GetHistory()
	if len(history) != 2 {
		t.Errorf("Expected 2 snapshots in history, got %d", len(history))
	}
}

func TestMetricsStoreDisabled(t *testing.T) {
	config := &MetricsConfig{
		Enabled:          false,
		RetentionPeriod:  1 * time.Hour,
		SnapshotInterval: 1 * time.Minute,
	}
	store := NewMetricsStore(config)

	// When disabled, recording should be no-op
	store.RecordAgentStatusChange("test-agent", "", "active")
	store.RecordTaskCompletion("test-agent", 5*time.Minute)
	store.RecordRequestLatency(10.0)
	store.RecordError()

	// Snapshot should still exist but empty
	if len(store.CurrentSnapshot.AgentMetrics) != 0 {
		t.Error("Agent metrics should be empty when disabled")
	}
	if store.CurrentSnapshot.System.RequestCount != 0 {
		t.Error("Request count should be 0 when disabled")
	}
}

func TestHistoryCircularBuffer(t *testing.T) {
	config := &MetricsConfig{
		Enabled:          true,
		RetentionPeriod:  3 * time.Minute,
		SnapshotInterval: 1 * time.Minute,
	}
	store := NewMetricsStore(config)

	// Max history size should be 3 (retention / interval)
	if store.MaxHistorySize < 3 {
		t.Errorf("Expected MaxHistorySize >= 3, got %d", store.MaxHistorySize)
	}

	// Take more snapshots than max size
	for i := 0; i < store.MaxHistorySize+5; i++ {
		store.RecordAgentStatusChange("agent", "", "active")
		store.TakeSnapshot()
	}

	// History should not exceed max size
	if len(store.History) > store.MaxHistorySize {
		t.Errorf("History size %d exceeds max %d", len(store.History), store.MaxHistorySize)
	}
}

func TestUpdateCoordinationMetrics(t *testing.T) {
	store := NewMetricsStore(nil)

	// Add agents in different states
	store.RecordAgentStatusChange("agent1", "", "active")
	store.RecordAgentStatusChange("agent2", "", "idle")
	store.RecordAgentStatusChange("agent3", "", "blocked")
	store.RecordAgentStatusChange("agent4", "", "active")

	// Update coordination metrics
	store.updateCoordinationMetrics()

	coord := store.CurrentSnapshot.Coordination
	if coord.TotalAgents != 4 {
		t.Errorf("Expected 4 total agents, got %d", coord.TotalAgents)
	}
	if coord.ActiveAgents != 2 {
		t.Errorf("Expected 2 active agents, got %d", coord.ActiveAgents)
	}
	if coord.IdleAgents != 1 {
		t.Errorf("Expected 1 idle agent, got %d", coord.IdleAgents)
	}
	if coord.BlockedAgents != 1 {
		t.Errorf("Expected 1 blocked agent, got %d", coord.BlockedAgents)
	}
	if coord.ConcurrentWorkItems != 2 {
		t.Errorf("Expected 2 concurrent work items, got %d", coord.ConcurrentWorkItems)
	}
}
