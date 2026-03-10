package coordinator

import (
	"testing"
	"time"
)

func TestHealthLevel(t *testing.T) {
	tests := []struct {
		name  string
		level HealthLevel
	}{
		{"healthy", HealthHealthy},
		{"warning", HealthWarning},
		{"critical", HealthCritical},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.level == "" {
				t.Errorf("HealthLevel should not be empty")
			}
		})
	}
}

func TestRecoveryAction(t *testing.T) {
	tests := []struct {
		name   string
		action RecoveryAction
	}{
		{"notify", ActionNotify},
		{"notify_and_flag", ActionNotifyAndFlag},
		{"stop_session", ActionStopSession},
		{"restart_session", ActionRestartSession},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.action == "" {
				t.Errorf("RecoveryAction should not be empty")
			}
		})
	}
}

func TestDefaultHealthConfig(t *testing.T) {
	config := DefaultHealthConfig()

	if config == nil {
		t.Fatal("DefaultHealthConfig returned nil")
	}

	if config.HeartbeatTimeoutWarning != 15*time.Minute {
		t.Errorf("expected warning timeout 15m, got %v", config.HeartbeatTimeoutWarning)
	}

	if config.HeartbeatTimeoutCritical != 30*time.Minute {
		t.Errorf("expected critical timeout 30m, got %v", config.HeartbeatTimeoutCritical)
	}

	if config.BlockedTimeout != 2*time.Hour {
		t.Errorf("expected blocked timeout 2h, got %v", config.BlockedTimeout)
	}

	if !config.Enabled {
		t.Error("expected health monitoring to be enabled by default")
	}

	if len(config.RecoveryActions) == 0 {
		t.Error("expected default recovery actions to be configured")
	}
}

func TestNewHealthStatus(t *testing.T) {
	hs := NewHealthStatus()

	if hs == nil {
		t.Fatal("NewHealthStatus returned nil")
	}

	if hs.Level != HealthHealthy {
		t.Errorf("expected initial level to be healthy, got %v", hs.Level)
	}

	if hs.Issues == nil {
		t.Error("expected Issues slice to be initialized")
	}

	if hs.Flags == nil {
		t.Error("expected Flags slice to be initialized")
	}
}

func TestUpdateHealth_Healthy(t *testing.T) {
	config := DefaultHealthConfig()
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Agent updated recently - should be healthy
	hs.UpdateHealth(now.Add(-5*time.Minute), StatusActive, config)

	if hs.Level != HealthHealthy {
		t.Errorf("expected healthy level, got %v", hs.Level)
	}

	if len(hs.Issues) != 0 {
		t.Errorf("expected no issues, got %d", len(hs.Issues))
	}
}

func TestUpdateHealth_Warning(t *testing.T) {
	config := DefaultHealthConfig()
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Agent updated 20 minutes ago - should trigger warning
	hs.UpdateHealth(now.Add(-20*time.Minute), StatusActive, config)

	if hs.Level != HealthWarning {
		t.Errorf("expected warning level, got %v", hs.Level)
	}

	if len(hs.Issues) == 0 {
		t.Error("expected at least one issue for warning state")
	}
}

func TestUpdateHealth_Critical(t *testing.T) {
	config := DefaultHealthConfig()
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Agent updated 35 minutes ago - should trigger critical
	hs.UpdateHealth(now.Add(-35*time.Minute), StatusActive, config)

	if hs.Level != HealthCritical {
		t.Errorf("expected critical level, got %v", hs.Level)
	}

	if len(hs.Issues) == 0 {
		t.Error("expected at least one issue for critical state")
	}
}

func TestUpdateHealth_BlockedStatus(t *testing.T) {
	config := DefaultHealthConfig()
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Agent stuck in blocked status for 3 hours
	hs.UpdateHealth(now.Add(-3*time.Hour), StatusBlocked, config)

	if hs.Level != HealthCritical {
		t.Errorf("expected critical level for stuck blocked agent, got %v", hs.Level)
	}

	if !contains(hs.Flags, "stuck_blocked") {
		t.Error("expected stuck_blocked flag to be set")
	}

	if len(hs.Issues) == 0 {
		t.Error("expected issues for stuck blocked agent")
	}

	// Agent recovers - should clear flag
	hs.UpdateHealth(now, StatusActive, config)

	if contains(hs.Flags, "stuck_blocked") {
		t.Error("expected stuck_blocked flag to be cleared on recovery")
	}
}

func TestUpdateHealth_ErrorStatus(t *testing.T) {
	config := DefaultHealthConfig()
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Agent in error status
	hs.UpdateHealth(now.Add(-5*time.Minute), StatusError, config)

	if hs.Level == HealthHealthy {
		t.Error("expected non-healthy level for error status")
	}

	hasErrorIssue := false
	for _, issue := range hs.Issues {
		if issue == "Agent in error status" {
			hasErrorIssue = true
			break
		}
	}
	if !hasErrorIssue {
		t.Error("expected error status issue to be recorded")
	}
}

func TestUpdateHealth_Disabled(t *testing.T) {
	config := DefaultHealthConfig()
	config.Enabled = false
	hs := NewHealthStatus()
	now := time.Now().UTC()

	// Even with old update, should be healthy when disabled
	hs.UpdateHealth(now.Add(-1*time.Hour), StatusActive, config)

	if hs.Level != HealthHealthy {
		t.Errorf("expected healthy when monitoring disabled, got %v", hs.Level)
	}
}

func TestComputeSpaceHealth(t *testing.T) {
	ks := NewKnowledgeSpace("test-space")
	now := time.Now().UTC()

	// Add some agents with different states
	ks.Agents["agent1"] = &AgentUpdate{
		Status:    StatusActive,
		Summary:   "Working",
		UpdatedAt: now.Add(-5 * time.Minute),
	}
	ks.Agents["agent2"] = &AgentUpdate{
		Status:    StatusActive,
		Summary:   "Working but slow",
		UpdatedAt: now.Add(-20 * time.Minute), // Warning
	}
	ks.Agents["agent3"] = &AgentUpdate{
		Status:    StatusBlocked,
		Summary:   "Stuck",
		UpdatedAt: now.Add(-3 * time.Hour), // Critical
	}

	summary := ComputeSpaceHealth(ks)

	if summary.TotalAgents != 3 {
		t.Errorf("expected 3 total agents, got %d", summary.TotalAgents)
	}

	if summary.HealthyAgents != 1 {
		t.Errorf("expected 1 healthy agent, got %d", summary.HealthyAgents)
	}

	if summary.WarningAgents != 1 {
		t.Errorf("expected 1 warning agent, got %d", summary.WarningAgents)
	}

	if summary.CriticalAgents != 1 {
		t.Errorf("expected 1 critical agent, got %d", summary.CriticalAgents)
	}

	if len(summary.AgentHealth) != 3 {
		t.Errorf("expected health status for 3 agents, got %d", len(summary.AgentHealth))
	}
}

func TestRecordHealthEvent(t *testing.T) {
	ks := NewKnowledgeSpace("test-space")

	RecordHealthEvent(ks, "test-agent", "heartbeat_timeout_critical",
		HealthCritical, "No heartbeat for 35 minutes", ActionNotify)

	if len(ks.HealthEvents) != 1 {
		t.Fatalf("expected 1 health event, got %d", len(ks.HealthEvents))
	}

	event := ks.HealthEvents[0]
	if event.Agent != "test-agent" {
		t.Errorf("expected agent 'test-agent', got %s", event.Agent)
	}
	if event.EventType != "heartbeat_timeout_critical" {
		t.Errorf("expected event type 'heartbeat_timeout_critical', got %s", event.EventType)
	}
	if event.Level != HealthCritical {
		t.Errorf("expected critical level, got %v", event.Level)
	}
	if event.RecoveryAction != ActionNotify {
		t.Errorf("expected notify action, got %v", event.RecoveryAction)
	}
}

func TestRecordHealthEvent_MaxEvents(t *testing.T) {
	ks := NewKnowledgeSpace("test-space")

	// Add 150 events (more than max of 100)
	for i := 0; i < 150; i++ {
		RecordHealthEvent(ks, "test-agent", "test_event",
			HealthWarning, "test message", ActionNotify)
	}

	// Should keep only last 100
	if len(ks.HealthEvents) != 100 {
		t.Errorf("expected 100 events (max), got %d", len(ks.HealthEvents))
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("contains", func(t *testing.T) {
		slice := []string{"foo", "bar", "baz"}
		if !contains(slice, "bar") {
			t.Error("expected contains to find 'bar'")
		}
		if contains(slice, "qux") {
			t.Error("expected contains to not find 'qux'")
		}
	})

	t.Run("removeString", func(t *testing.T) {
		slice := []string{"foo", "bar", "baz"}
		result := removeString(slice, "bar")
		if len(result) != 2 {
			t.Errorf("expected 2 elements after removal, got %d", len(result))
		}
		if contains(result, "bar") {
			t.Error("expected 'bar' to be removed")
		}
	})
}

func TestKnowledgeSpace_HealthInitialization(t *testing.T) {
	ks := NewKnowledgeSpace("test")

	if ks.HealthConfig == nil {
		t.Error("expected HealthConfig to be initialized")
	}

	if ks.AgentHealth == nil {
		t.Error("expected AgentHealth map to be initialized")
	}

	if ks.HealthEvents == nil {
		t.Error("expected HealthEvents slice to be initialized")
	}

	if !ks.HealthConfig.Enabled {
		t.Error("expected health monitoring to be enabled by default")
	}
}
