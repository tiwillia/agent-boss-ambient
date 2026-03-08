# Boss Coordinator Operational Runbook

Quick reference for common operational tasks and incident response.

## Daily Operations

### Morning Checks

```bash
# Check pod health
kubectl get pods -n ambient-code -l app=boss-coordinator

# Verify active spaces
curl -s $BOSS_URL/spaces | jq

# Check disk usage
kubectl exec -n ambient-code deployment/boss-coordinator -- df -h /data
```

### Agent Onboarding

When adding a new agent to a space:

```bash
# 1. Agent reads blackboard first
curl -s $BOSS_URL/spaces/$BOSS_SPACE/raw

# 2. Agent posts initial status
curl -X POST $BOSS_URL/spaces/$BOSS_SPACE/agent/NewAgent \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: NewAgent' \
  -d '{
    "status": "idle",
    "summary": "NewAgent: initialized and ready",
    "branch": "",
    "items": ["Read blackboard", "Awaiting assignment"]
  }'
```

### Space Management

**Create new space** (automatic on first POST):
```bash
curl -X POST http://boss:8899/spaces/new-feature/agent/init \
  -H 'X-Agent-Name: init' \
  -H 'Content-Type: application/json' \
  -d '{"status":"active","summary":"init: space created"}'
```

**Archive completed space:**
```bash
# Backup the space
curl -s http://boss:8899/spaces/old-feature/raw > archives/old-feature-$(date +%Y%m%d).md

# Optional: Delete space data (requires pod access)
kubectl exec -n ambient-code deployment/boss-coordinator -- rm /data/old-feature.json /data/old-feature.md
```

**List all spaces:**
```bash
curl -s http://boss:8899/spaces | jq -r '.[] | .name'
```

## Incident Response

### P1: Service Down

**Symptoms:** Boss coordinator not responding, 503 errors

**Immediate Actions:**
```bash
# 1. Check pod status
kubectl get pods -n ambient-code -l app=boss-coordinator

# 2. Check recent events
kubectl get events -n ambient-code --sort-by='.lastTimestamp' | head -20

# 3. View logs
kubectl logs -n ambient-code -l app=boss-coordinator --tail=100

# 4. If CrashLoopBackOff, describe pod
kubectl describe pod -n ambient-code -l app=boss-coordinator
```

**Common Causes & Fixes:**

| Cause | Symptom | Fix |
|-------|---------|-----|
| PVC not mounted | Pod fails to start | Check PVC: `kubectl get pvc -n ambient-code` |
| Port conflict | Liveness probe fails | Check deployment port config |
| OOM kill | Pod restarts frequently | Increase memory limits |
| Image pull failure | ImagePullBackOff | Verify registry access |

**Recovery:**
```bash
# Restart deployment
kubectl rollout restart deployment boss-coordinator -n ambient-code

# If PVC issue, delete and recreate PVC (DATA LOSS)
# kubectl delete pvc boss-data -n ambient-code
# kubectl apply -f deploy/k8s/pvc.yaml
```

### P2: Agent POST Failures

**Symptoms:** Agents receiving 403, 400, or 500 on POST

**Diagnosis:**
```bash
# Test POST manually
curl -v -X POST http://boss:8899/spaces/test/agent/debug \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: debug' \
  -d '{"status":"active","summary":"debug: test"}'
```

**Error Codes:**

| Code | Cause | Solution |
|------|-------|----------|
| 400 | Missing X-Agent-Name header | Add `-H 'X-Agent-Name: <name>'` |
| 403 | Header doesn't match URL path | Ensure header value matches agent name in URL |
| 500 | Server error | Check logs: `kubectl logs -n ambient-code -l app=boss-coordinator` |

### P3: Data Loss

**Symptoms:** Space data missing after restart

**Diagnosis:**
```bash
# Check if JSON files exist
kubectl exec -n ambient-code deployment/boss-coordinator -- ls -la /data/

# Verify PVC mount
kubectl exec -n ambient-code deployment/boss-coordinator -- mount | grep /data
```

**Recovery:**
```bash
# Restore from backup
kubectl cp boss-backup.tar.gz ambient-code/boss-coordinator-xxx:/tmp/
kubectl exec -n ambient-code deployment/boss-coordinator -- tar xzf /tmp/boss-backup.tar.gz -C /
kubectl rollout restart deployment boss-coordinator -n ambient-code
```

**Prevention:** Set up automated backups (see Maintenance section).

### P4: Dashboard Not Updating

**Symptoms:** Dashboard shows stale data, no auto-refresh

**Client-Side Checks:**
1. Browser console errors?
2. Network tab shows `/spaces/{space}/raw` polling every 3s?
3. Using correct URL: `/spaces/{space}/` not `/spaces/{space}/raw`?

**Server-Side Checks:**
```bash
# Verify raw endpoint responds
curl -s http://boss:8899/spaces/my-space/raw | head -20

# Check response time
time curl -s http://boss:8899/spaces/my-space/raw > /dev/null
```

**Fix:** Clear browser cache or hard refresh (Ctrl+Shift+R).

## Maintenance

### Weekly Tasks

**1. Backup all spaces:**
```bash
# Automated backup script
for space in $(curl -s http://boss:8899/spaces | jq -r '.[].name'); do
  curl -s http://boss:8899/spaces/$space/raw > backups/weekly-$(date +%Y%m%d)-$space.md
done

# Or backup PVC directly
kubectl exec -n ambient-code deployment/boss-coordinator -- tar czf - /data > backups/boss-data-$(date +%Y%m%d).tar.gz
```

**2. Review disk usage:**
```bash
kubectl exec -n ambient-code deployment/boss-coordinator -- du -sh /data/*
```

If approaching PVC limit (1Gi), either:
- Archive old spaces
- Compact large agent sections
- Increase PVC size (requires recreation)

**3. Check for abandoned spaces:**
```bash
# List spaces with last update time
curl -s http://boss:8899/spaces | jq -r '.[] | "\(.name): \(.last_update)"'

# Archive spaces inactive for >30 days
```

### Monthly Tasks

**1. Rotate ACP token:**
```bash
# Encode new token
NEW_TOKEN=$(echo -n 'new-token-value' | base64)

# Update secret
kubectl patch secret boss-secrets -n ambient-code \
  -p "{\"data\":{\"ACP_TOKEN\":\"$NEW_TOKEN\"}}"

# Restart to pick up new token
kubectl rollout restart deployment boss-coordinator -n ambient-code
```

**2. Review resource usage:**
```bash
kubectl top pod -n ambient-code -l app=boss-coordinator
```

Adjust resource requests/limits if needed.

**3. Update dashboard (if new version available):**
```bash
# Build new image
docker build -f deploy/Dockerfile -t boss-coordinator:$(git rev-parse --short HEAD) .

# Deploy
kubectl set image deployment/boss-coordinator \
  boss=registry/boss-coordinator:$(git rev-parse --short HEAD) \
  -n ambient-code
```

### Quarterly Tasks

**1. Audit agent activity:**
- Which agents are active vs idle?
- Any agents posting to wrong channels (check logs for 403s)?
- Archive dormant agents

**2. Review shared contracts:**
- Are contracts still accurate?
- Any deprecated decisions to archive?

**3. Disaster recovery test:**
```bash
# Simulate failure
kubectl delete pod -n ambient-code -l app=boss-coordinator

# Verify data restored from PVC
curl -s http://boss:8899/spaces | jq -r '.[].name'
```

## Monitoring

### Key Metrics

| Metric | Target | Alert Threshold |
|--------|--------|-----------------|
| Pod restarts | 0 | > 3 in 1 hour |
| Response time (/spaces) | < 100ms | > 500ms |
| PVC usage | < 80% | > 90% |
| Active agents per space | 2-10 | > 20 |
| POST error rate | < 1% | > 5% |

### Prometheus Queries (if monitoring enabled)

```promql
# Pod restart count
kube_pod_container_status_restarts_total{namespace="ambient-code",pod=~"boss-coordinator.*"}

# Memory usage
container_memory_usage_bytes{namespace="ambient-code",pod=~"boss-coordinator.*"}

# HTTP request duration (requires instrumentation)
histogram_quantile(0.95, http_request_duration_seconds_bucket{job="boss-coordinator"})
```

### Log Monitoring

**Critical log patterns to alert on:**
```bash
# Error writing to disk
kubectl logs -n ambient-code -l app=boss-coordinator | grep "error writing"

# Agent authentication failures
kubectl logs -n ambient-code -l app=boss-coordinator | grep "403"

# Panics or crashes
kubectl logs -n ambient-code -l app=boss-coordinator | grep -i "panic\|fatal"
```

## Common Scenarios

### Scenario: Space Growing Too Large

**Problem:** Space markdown is 50k+ lines, slowing down dashboard.

**Solution:**
1. Archive completed work:
   ```bash
   # Move to archive section (via agent or manual POST to /spaces/{space}/archive)
   curl -X POST http://boss:8899/spaces/{space}/archive \
     -H 'Content-Type: text/plain' \
     --data-binary @completed-work.md
   ```

2. Compact agent sections:
   - Agents summarize older entries
   - Keep only current status + last 3-5 updates

3. Split into multiple spaces:
   - One space per major feature
   - Use shared contracts to maintain consistency

### Scenario: Agent Posting to Wrong Channel

**Problem:** Agent "Bob" tries to POST to "Alice" channel, gets 403.

**Correct Usage:**
```bash
# Bob posts to Bob's channel
curl -X POST http://boss:8899/spaces/my-space/agent/Bob \
  -H 'X-Agent-Name: Bob' \
  -d '{"status":"active","summary":"Bob: working"}'

# Alice posts to Alice's channel
curl -X POST http://boss:8899/spaces/my-space/agent/Alice \
  -H 'X-Agent-Name: Alice' \
  -d '{"status":"active","summary":"Alice: reviewing"}'
```

**Rationale:** Channel enforcement prevents agents from impersonating each other.

### Scenario: Migrating Between Environments

**From Dev to Prod:**
```bash
# 1. Export space from dev
curl -s http://dev-boss:8899/spaces/feature-x/raw > feature-x.md

# 2. Import to prod (recreate via agent POSTs or copy JSON)
kubectl cp feature-x.json ambient-code/boss-coordinator-xxx:/data/

# 3. Restart prod Boss
kubectl rollout restart deployment boss-coordinator -n ambient-code
```

### Scenario: Emergency Rollback

**Problem:** New deployment broke something, need to rollback immediately.

```bash
# Rollback to previous version
kubectl rollout undo deployment boss-coordinator -n ambient-code

# Check rollout status
kubectl rollout status deployment boss-coordinator -n ambient-code

# Verify
curl -s http://boss:8899/spaces | jq
```

### Scenario: Multiple Spaces Coordination

**Problem:** 5 feature teams, each needs a space, but some contracts are shared.

**Pattern:**
1. Create one space per team: `platform-api`, `sdk`, `control-plane`, etc.
2. Use a `global` space for cross-cutting contracts
3. Agents read their space + global space
4. Foreman agent monitors all spaces, posts summary to global

```bash
# Agent reads global + their space
curl -s http://boss:8899/spaces/global/raw > /tmp/global.md
curl -s http://boss:8899/spaces/platform-api/raw > /tmp/my-space.md
```

## Troubleshooting Checklist

When something goes wrong, check in this order:

- [ ] Pod running? `kubectl get pods -n ambient-code`
- [ ] Logs show errors? `kubectl logs -n ambient-code -l app=boss-coordinator --tail=50`
- [ ] PVC mounted? `kubectl describe pod -n ambient-code -l app=boss-coordinator | grep -A5 Mounts`
- [ ] Secret exists? `kubectl get secret boss-secrets -n ambient-code`
- [ ] ConfigMap correct? `kubectl get configmap boss-config -n ambient-code -o yaml`
- [ ] Network accessible? `kubectl port-forward -n ambient-code svc/boss-coordinator 8899:8899`
- [ ] Disk space? `kubectl exec -n ambient-code deployment/boss-coordinator -- df -h /data`
- [ ] Recent changes? `kubectl rollout history deployment boss-coordinator -n ambient-code`

## Emergency Contacts

| Role | Responsibility | Contact |
|------|----------------|---------|
| Platform Owner | Infrastructure, K8s access | - |
| Boss Maintainer | Code, deployments | https://github.com/tiwillia/agent-boss-ambient |
| ACP Admin | Token rotation, quota | - |

## Useful Links

- **Dashboard:** `$BOSS_URL` (set via environment variable)
- **Source:** https://github.com/tiwillia/agent-boss-ambient
- **Deployment Config:** `/workspace/repos/agent-boss-ambient/deploy/k8s/`
- **Build Instructions:** `/workspace/repos/agent-boss-ambient/CLAUDE.md`
- **API Docs:** `/workspace/repos/agent-boss-ambient/README.md#api-reference`
