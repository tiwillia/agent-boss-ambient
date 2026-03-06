# BOSS Multi-Agent Workflow Design

## Executive Summary

This document proposes a structured ACP workflow to replace the current ad-hoc approach where agents use `curl` commands and receive task prompts with BOSS coordination instructions. The workflow will provide a schema'd, versioned method for agents to participate in the multi-agent BOSS coordination space.

## Current State Analysis

### Existing Approach
- Agents communicate with BOSS API via `curl` commands in bash
- Instructions injected into agent task prompts at creation time
- Protocol defined in `internal/coordinator/protocol.md`
- Agents manually:
  - Read blackboard: `curl https://boss-coordinator.../spaces/{space}/raw`
  - POST status updates: `curl -X POST .../spaces/{space}/agent/{name}` with JSON payload
  - Follow check-in protocol manually

### Pain Points
1. **No standardization**: Each agent implementation varies slightly
2. **Manual coordination**: Agents must remember protocol details
3. **Error-prone**: Easy to forget required fields or format incorrectly
4. **Hard to evolve**: Changes require updating all agent prompts
5. **No workflow reuse**: Knowledge not shareable across sessions

## Proposed Solution: BOSS Coordination Workflow

### Overview

Create an ACP workflow that encapsulates all BOSS multi-agent coordination knowledge and provides structured commands for agents to interact with the coordination system.

### Workflow Structure

```
workflows/boss-coordination/
├── .ambient/
│   └── ambient.json              # ACP workflow configuration
├── .claude/
│   ├── commands/
│   │   ├── boss.check-in.md      # Perform BOSS check-in
│   │   ├── boss.post.md          # Post status update
│   │   ├── boss.read.md          # Read blackboard
│   │   └── boss.ignite.md        # Start new work item
│   ├── skills/
│   │   ├── protocol/
│   │   │   └── SKILL.md          # BOSS protocol reference
│   │   └── status-formatter/
│   │       └── SKILL.md          # Format status updates
│   └── settings.json             # Tool permissions
├── templates/
│   ├── status-update.json        # Template for status POSTs
│   └── interrupt-request.json   # Template for interrupt requests
├── CLAUDE.md                     # Persistent agent behavior
└── README.md                     # Workflow documentation
```

### Key Components

#### 1. `ambient.json` Configuration

```json
{
  "name": "BOSS Multi-Agent Coordination",
  "description": "Structured workflow for participating in BOSS (Blackboard-Oriented Software System) multi-agent spaces. Provides commands for check-ins, status updates, and coordinated work execution.",
  "systemPrompt": "You are an agent participating in a BOSS multi-agent coordination space...",
  "startupPrompt": "Welcome to the BOSS Multi-Agent Coordination workflow...",
  "results": {
    "Status Updates": "artifacts/boss/status/**/*.json",
    "Work Artifacts": "artifacts/boss/work/**/*",
    "Coordination Logs": "artifacts/boss/logs/**/*.log"
  }
}
```

#### 2. System Prompt Design

The `systemPrompt` should define:

**Core Identity:**
- Agent role in the BOSS coordination space
- Understanding of blackboard architecture
- Collaboration principles with other agents

**Key Responsibilities:**
- Monitor blackboard for assignments
- Post status updates following protocol
- Execute assigned work systematically
- Coordinate with Reviewer for PR approval
- Respect Leader assignments and coordination

**Protocol Knowledge:**
- Blackboard read/write patterns
- Status update format (status, summary, context fields)
- Check-in frequency and triggers
- Interrupt handling

**Workspace Layout:**
- BOSS API endpoint: `$BOSS_URL` environment variable
- Space name: Configured at workflow activation
- Agent name: `$BOSS_AGENT` environment variable
- Commands available: `/boss.check-in`, `/boss.post`, `/boss.read`

#### 3. Startup Prompt Design

Should welcome agent and:
- Confirm BOSS space connection
- Display current agent identity
- Show available commands
- Guide initial check-in

Example:
```
🤖 BOSS Multi-Agent Coordination Activated

Connected to space: agent-boss-ambient
Agent identity: Software-engineer
BOSS API: https://boss-coordinator.../

Available commands:
- /boss.check-in - Perform full check-in cycle
- /boss.read - Read current blackboard state
- /boss.post - Post status update
- /boss.ignite - Start new work assignment

Run /boss.check-in to begin monitoring the blackboard.
```

#### 4. Commands (Slash Commands)

##### `/boss.check-in`
**Purpose:** Execute complete check-in cycle
**Actions:**
1. Read blackboard via API
2. Analyze current status and assignments
3. Format status update per protocol
4. POST status to blackboard
5. Resume previous work or await assignment

**File:** `.claude/commands/boss.check-in.md`

##### `/boss.read`
**Purpose:** Read and display blackboard state
**Actions:**
1. Fetch blackboard markdown
2. Parse agent sections
3. Display formatted summary
4. Highlight relevant assignments

**File:** `.claude/commands/boss.read.md`

##### `/boss.post`
**Purpose:** Post status update to blackboard
**Parameters:** status, summary, branch, pr, items, next_steps
**Actions:**
1. Validate required fields
2. Format JSON payload per protocol
3. POST to agent's blackboard channel
4. Confirm successful update

**File:** `.claude/commands/boss.post.md`

##### `/boss.ignite`
**Purpose:** Signal readiness for new work
**Actions:**
1. Check current status
2. Review available issues/tasks on blackboard
3. POST readiness status
4. Await Leader assignment

**File:** `.claude/commands/boss.ignite.md`

#### 5. Skills (Reusable Knowledge)

##### `protocol` Skill
**File:** `.claude/skills/protocol/SKILL.md`
**Contains:**
- Complete BOSS protocol specification
- Status update schema
- Interrupt types and handling
- Blackboard structure reference
- API endpoints documentation

##### `status-formatter` Skill
**File:** `.claude/skills/status-formatter/SKILL.md`
**Contains:**
- JSON formatting utilities
- Field validation rules
- Context object structure
- Example status updates

#### 6. CLAUDE.md (Persistent Context)

```markdown
# BOSS Agent Behavior Guidelines

## Core Principles

1. **Blackboard-First**: Always read blackboard before taking action
2. **Protocol Compliance**: Follow status update schema exactly
3. **Coordination Respect**: Honor Leader assignments and Reviewer decisions
4. **Transparency**: Post clear, honest status updates
5. **Autonomy within Bounds**: Work independently on assigned tasks, coordinate for decisions

## Status Update Protocol

Every check-in must include:
- `status`: "active" | "idle" | "blocked" | "done"
- `summary`: Concise one-line status
- `branch`: Current git branch
- `pr`: Related PR number or status
- `items`: Array of recent work items
- `next_steps`: Planned actions

## Work Execution Flow

1. Read blackboard
2. Check for assignments from Leader
3. If assigned: transition to "active", begin work
4. If idle: post idle status, await assignment
5. On completion: post "done", create PR, request Reviewer
6. After merge: transition to "idle", ready for next task

## Team Coordination

- **Leader**: Assigns work, merges PRs, coordinates overall progress
- **Reviewer**: Reviews code, posts APPROVED/REJECTED, ensures quality
- **Secretary**: Monitors docs impact, maintains repository health
- **Engineers**: Execute assigned work, create PRs, follow workflow

## Never Do

- Never assign yourself work without Leader approval
- Never merge PRs without Reviewer APPROVED status
- Never skip check-ins when instructed
- Never post incomplete or misleading status updates
```

#### 7. Templates

##### `templates/status-update.json`
```json
{
  "status": "idle",
  "summary": "{agent-name}: {brief-status-description}",
  "branch": "main",
  "pr": "{pr-number or status}",
  "items": ["{recent-work-item-1}", "{recent-work-item-2}"],
  "next_steps": "{planned-next-actions}"
}
```

##### `templates/interrupt-request.json`
```json
{
  "type": "decision",
  "question": "{question-for-human}",
  "context": {
    "situation": "{current-situation}",
    "options": "{available-options}",
    "recommendation": "{agent-recommendation}"
  }
}
```

### Environment Configuration

The workflow expects:
- `BOSS_URL`: Base URL of BOSS coordinator (e.g., `http://localhost:8899` or `https://boss-coordinator.../`)
- `BOSS_SPACE`: Space name (e.g., `agent-boss-ambient`)
- `BOSS_AGENT`: Agent identifier (e.g., `Software-engineer`)

These can be:
1. Set in `.claude/settings.json` as environment variables
2. Passed at workflow activation
3. Prompted for at startup

### Usage Patterns

#### Pattern 1: Regular Check-In Agent
```
1. User activates "BOSS Multi-Agent Coordination" workflow
2. Workflow prompts for space name and agent name
3. Agent runs /boss.check-in automatically or on trigger
4. Agent reads blackboard, posts status, resumes work
5. Repeats on interval or manual trigger
```

#### Pattern 2: Task-Focused Engineer
```
1. User activates workflow with assignment context
2. Agent runs /boss.read to understand current state
3. Agent begins assigned work (e.g., Issue #8)
4. Agent posts updates via /boss.post during work
5. On completion: creates PR, posts status, runs /boss.check-in
6. After merge: returns to idle, runs /boss.check-in, awaits new assignment
```

#### Pattern 3: Coordinator (Leader)
```
1. Leader workflow variant with additional commands:
   - /boss.assign {agent} {issue} - Assign work to agent
   - /boss.merge {pr} - Merge approved PR
   - /boss.broadcast {message} - Send message to all agents
2. Leader monitors all agent status
3. Leader makes assignment decisions
4. Leader coordinates merge workflow
```

## Benefits of Workflow Approach

### For Agents
1. **Standardized Participation**: No need to remember curl commands or JSON formats
2. **Guided Actions**: Slash commands with built-in validation
3. **Reduced Errors**: Templates and formatters ensure protocol compliance
4. **Knowledge Preservation**: Workflow persists across sessions

### For System
1. **Version Control**: Workflow changes tracked in git
2. **Evolution**: Update once, all agents benefit
3. **Documentation**: README and SKILL.md serve as living docs
4. **Extensibility**: Easy to add new commands or capabilities

### For Humans
1. **Visibility**: Artifacts show agent coordination history
2. **Configuration**: Single place to modify agent behavior
3. **Debugging**: Logs and templates aid troubleshooting
4. **Onboarding**: New agents use workflow immediately

## Migration Strategy

### Phase 1: Workflow Creation (Week 1)
1. Implement basic workflow structure
2. Create core commands: check-in, read, post
3. Define protocol skill with complete specification
4. Test with single agent in isolated space

### Phase 2: Multi-Agent Testing (Week 2)
1. Deploy to 2-agent test space
2. Verify coordination patterns
3. Refine status update formats
4. Validate Leader-Reviewer-Engineer flow

### Phase 3: Rollout (Week 3)
1. Document migration guide
2. Train existing agents on new workflow
3. Transition production spaces
4. Deprecate curl-based approach

### Phase 4: Enhancement (Week 4+)
1. Add interrupt handling commands
2. Implement coordinator variant for Leader
3. Create visualization commands
4. Add metrics and reporting

## Open Questions for Review

**Critical for ALL agents to address:**

1. **Command Granularity**: Should we have one `/boss.check-in` command that does everything, or separate `/boss.read`, `/boss.analyze`, `/boss.post`, `/boss.resume` commands?

2. **Agent Identity**: How should agent name be determined?
   - Workflow parameter at activation?
   - Environment variable?
   - Prompted at startup?

3. **Space Discovery**: Should workflow include commands to:
   - List available spaces?
   - Create new spaces?
   - Switch between spaces?

4. **Interrupt Handling**: How should workflow handle BOSS interrupts (questions for human)?
   - Dedicated `/boss.interrupt` command?
   - Built into check-in flow?
   - Separate skill?

5. **Leader Variant**: Should Leader have a separate workflow with additional commands, or one workflow with conditional commands based on role?

6. **Artifact Storage**: Where should coordination artifacts live?
   - `artifacts/boss/` (workflow-owned)?
   - Workspace root?
   - Separate coordination repo?

7. **Real-Time vs Polling**: Should agents:
   - Poll blackboard on interval?
   - Use SSE/WebSocket for real-time updates?
   - Wait for external trigger (human command)?

8. **State Management**: Should workflow maintain state about:
   - Last check-in time?
   - Current assignment?
   - Work history?

## Implementation Considerations

### Technical Requirements
- BOSS API must be accessible (network/auth)
- Agent must have git access for PR creation
- Workspace must support artifact generation
- Commands must handle API errors gracefully

### Security & Authorization
- Agent credentials for BOSS API
- GitHub token for PR operations
- Space-level access control
- Audit logging of agent actions

### Performance
- Blackboard read frequency (avoid API rate limits)
- Status update batching (if many agents)
- Artifact storage limits
- Network latency handling

### Monitoring
- Agent health checks
- Protocol compliance validation
- Coordination metrics (check-in frequency, response times)
- Error rate tracking

## Success Criteria

A successful BOSS workflow implementation will:

1. **Replace curl commands**: No agent needs to construct raw API calls
2. **Enforce protocol**: All status updates conform to schema
3. **Simplify participation**: New agents activate workflow and begin immediately
4. **Enable evolution**: Protocol changes update in one place
5. **Improve reliability**: Fewer errors, better coordination
6. **Provide visibility**: Clear artifacts show what agents are doing
7. **Support all roles**: Works for Leader, Reviewer, Secretary, Engineers

## Next Steps

**CRITICAL: This design requires review and approval from ALL agents before implementation.**

1. **Review Process** (ALL agents required):
   - Software-engineer: Review design for implementability
   - Software-engineer-2: Review for usability and clarity
   - Leader: Review for coordination feasibility
   - Reviewer: Review for quality and completeness
   - Secretary: Review for documentation impact

2. **Design Discussion**:
   - Address open questions above
   - Gather feedback on proposed structure
   - Refine based on multi-agent input
   - Achieve consensus on approach

3. **Implementation Plan** (only after design approval):
   - Create workflow repository structure
   - Implement core commands
   - Write protocol skill
   - Test in isolated environment
   - Document usage guide

4. **No Implementation Without Approval**:
   - Per Issue #22: "No agent should implement the design until explicit human instruction is given to do so"
   - Design must be reviewed by ALL agents first
   - Human approval required before any coding begins

---

**Document Version**: 1.0
**Created**: 2026-03-06
**Author**: Software-engineer
**Status**: Draft - Awaiting ALL agent review
**Issue**: #22 (Design a workflow)
