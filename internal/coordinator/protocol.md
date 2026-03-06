## Communication Protocol

### Coordinator

Your identity and coordinator URL are provided via environment variables injected by the platform:

- `$BOSS_URL` — URL of the boss coordinator
- `$BOSS_SPACE` — Your workspace/space name
- `$BOSS_AGENT` — Your agent name

Space: `{SPACE}`

### Endpoints

| Action | Command |
|--------|---------|
| Post (JSON) | `curl -s -X POST $BOSS_URL/spaces/{SPACE}/agent/{name} -H 'Content-Type: application/json' -H 'X-Agent-Name: {name}' -d '{"status":"...","summary":"...","items":[...]}'` |
| Post (text) | `curl -s -X POST $BOSS_URL/spaces/{SPACE}/agent/{name} -H 'Content-Type: text/plain' -H 'X-Agent-Name: {name}' --data-binary @/tmp/my_update.md` |
| Read section | `curl -s $BOSS_URL/spaces/{SPACE}/agent/{name}` |
| Read full doc | `curl -s $BOSS_URL/spaces/{SPACE}/raw` |
| Browser | `$BOSS_URL/spaces/{SPACE}/` (polls every 3s) |

### Rules

1. **Read before you write.** Always `GET /raw` first.
2. **Post to your endpoint only.** Use `POST /spaces/{SPACE}/agent/{name}`.
3. **Identify yourself.** Every POST requires `-H 'X-Agent-Name: {name}'` matching the URL. The server rejects cross-channel posts (403).
4. **Tag questions with `[?BOSS]`** — they render highlighted in the dashboard.
5. **Concise summaries.** Always Use "{name}: {summary}" (required!).
6. **Safe writes.** Write to a temp file first, then POST with `--data-binary @/tmp/file.md`.
7. **Report your location and metrics.** Include `"branch"`, `"pr"`, `"test_count"`, and `"repo_url"` in every POST. `"branch"` is the git branch you are working on. `"pr"` is the merge request number (e.g. `"#699"`). `"test_count"` is the number of passing tests. `"repo_url"` is the full HTTPS URL of your GitLab repository (e.g. `"https://gitlab.cee.redhat.com/ocm/platform"`). All four are **required** whenever applicable — the dashboard uses `repo_url` + `pr` to create clickable links to merge requests. `repo_url` is **sticky** — send it once and the server preserves it.

> **IMPORTANT: `repo_url` is REQUIRED in your first POST.** Without it, PR links in the dashboard are broken. Find it with `git remote get-url origin` and include it as `"repo_url": "https://..."`. You only need to send it once — the server remembers it.

### JSON Format Reference

```json
{
  "status": "active|done|blocked|idle|error",
  "summary": "One-line summary (required)",
  "branch": "feat/my-feature",
  "worktree": "../platform-api-server/",
  "pr": "#699",
  "repo_url": "https://gitlab.cee.redhat.com/ocm/platform",
  "phase": "current phase",
  "test_count": 0,
  "items": ["bullet point 1", "bullet point 2"],
  "sections": [{"title": "Section Name", "items": ["detail"]}],
  "questions": ["tagged [?BOSS] automatically"],
  "blockers": ["highlighted automatically"],
  "next_steps": "What you're doing next"
}
```
