You are joining a multi-agent coordination project. Execute these steps exactly.

**Arguments:** `$ARGUMENTS` is `<your-agent-name> <space-name>` (two words, space-separated). Parse them: the FIRST word is your agent name, the SECOND word is the workspace/space name. If only one word is provided, it is the space name — use your `$BOSS_AGENT` env var for your agent name.

Your identity, space, and coordinator URL are available as environment variables:
- `$BOSS_URL` — coordinator URL
- `$BOSS_SPACE` — workspace name
- `$BOSS_AGENT` — your agent name

If these env vars are set, prefer them over `$ARGUMENTS`.

## Step 1: Fetch your ignition prompt

Using your agent name and the space name:

```bash
curl -s "$BOSS_URL/spaces/$BOSS_SPACE/ignition/$BOSS_AGENT"
```

Or if env vars aren't set, parse from `$ARGUMENTS`:

```bash
curl -s "http://localhost:8899/spaces/SPACE_NAME/ignition/AGENT_NAME"
```

This returns your identity, peer agents, the full protocol, and a POST template.

## Step 2: Read the blackboard

```bash
curl -s "$BOSS_URL/spaces/$BOSS_SPACE/raw"
```

This shows what every agent is doing, what decisions have been made, and what standing orders exist.

## Step 3: Post your initial status

Using the protocol and template from Step 1, post your initial status to your channel. Include `status`, `summary`, `branch`, `items`, and `next_steps`.

## Rules

- **Never contradict shared contracts** — these are agreed API surfaces and architectural decisions all agents must respect.
- **Tag questions with `[?BOSS]`** when you need the human to make a decision.
- **Post to your own channel only** — the server rejects cross-channel posts.
- **Use `$BOSS_URL`, `$BOSS_SPACE`, `$BOSS_AGENT`** env vars for all coordinator communication.
