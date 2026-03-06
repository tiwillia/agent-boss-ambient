STOP. This is a mechanical status sync. Do NOT plan or analyze. Execute these 3 steps literally, then STOP.

**Parse `$ARGUMENTS`:** `$ARGUMENTS` contains two words separated by a space. The FIRST word is your agent name. The SECOND word is the space name. Example: if `$ARGUMENTS` is `Overlord sdk-backend-replacement`, then your agent name is `Overlord` and the space name is `sdk-backend-replacement`.

Prefer environment variables if set:
- `$BOSS_AGENT` — your agent name
- `$BOSS_SPACE` — space name
- `$BOSS_URL` — coordinator URL (default: `http://localhost:8899`)

## Step 1: Read the blackboard

```bash
curl -s "$BOSS_URL/spaces/$BOSS_SPACE/raw"
```

Replace `$BOSS_URL` and `$BOSS_SPACE` with values from env vars or `$ARGUMENTS`. Scan for anything addressed to you. Do NOT analyze other agents.

**Important rule**: Always use `curl`, never use Fetch tool. Fetch will *not* work on localhost. **Always** use curl. This is important!

## Step 2: Write your status JSON and POST it

Create `/tmp/boss_checkin.json` reflecting your CURRENT state. Do not change your work — just report what you are doing right now.

```bash
cat > /tmp/boss_checkin.json << 'CHECKIN'
{
  "status": "active",
  "summary": "AGENT_NAME: <one-line description of what you are currently doing>",
  "branch": "<your current git branch or empty string>",
  "pr": "<your open MR number e.g. #748 or empty string>",
  "repo_url": "<full HTTPS URL of your GitLab repo e.g. https://gitlab.cee.redhat.com/ocm/platform>",
  "phase": "<your current phase or empty string>",
  "test_count": 0,
  "items": ["<what you have done or are doing>"],
  "next_steps": "<what you will do next>"
}
CHECKIN
```

Replace AGENT_NAME with your agent name. Keep summary under 120 chars. Include `"pr"` and `"repo_url"` if you have an open merge request — the dashboard links them. Both are **sticky** (sent once, preserved automatically). Add `"blockers"` array only if you are genuinely blocked. Add `"questions"` array with `[?BOSS]` prefix only if you need the human to decide something.

Then POST it:

```bash
curl -s -X POST "$BOSS_URL/spaces/$BOSS_SPACE/agent/$BOSS_AGENT" \
  -H 'Content-Type: application/json' \
  -H "X-Agent-Name: $BOSS_AGENT" \
  -d @/tmp/boss_checkin.json
```

You MUST see `accepted for` in the response. If you do not, something is wrong — retry once.

## Step 3: STOP

Do not start any work. Do not analyze the blackboard. Do not make plans. STOP HERE.
