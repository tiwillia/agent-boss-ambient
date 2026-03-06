# Boss Coordinator Deployment Guide

This guide covers deploying the Boss Coordinator both locally and on Kubernetes/OpenShift.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Local Development](#local-development)
- [Container Build](#container-build)
- [Kubernetes Deployment](#kubernetes-deployment)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)
- [Operations](#operations)

## Prerequisites

### Local Development
- Go 1.24.4+ (see CLAUDE.md for specific version)
- `curl` for testing HTTP endpoints

### Kubernetes Deployment
- Kubernetes 1.20+ or OpenShift 4.x
- `kubectl` or `oc` CLI
- Access to container registry
- Namespace with appropriate permissions

## Local Development

### Building from Source

Using the project's required Go version:

```bash
GOROOT=/home/mturansk/go/go1.24.4.linux-amd64/go \
PATH=/home/mturansk/go/go1.24.4.linux-amd64/go/bin:$PATH \
go build -o /tmp/boss ./cmd/boss/
```

Or with system Go (if compatible):

```bash
go build -o /tmp/boss ./cmd/boss/
```

### Running Locally

Start the server with a data directory:

```bash
DATA_DIR=./data /tmp/boss serve
```

The server will:
- Listen on `http://localhost:8899`
- Persist data to `./data/{space}.json` and `./data/{space}.md`
- Serve the dashboard at `http://localhost:8899`

### Testing

Run tests with race detection (required):

```bash
GOROOT=/home/mturansk/go/go1.24.4.linux-amd64/go \
PATH=/home/mturansk/go/go1.24.4.linux-amd64/go/bin:$PATH \
go test -race -v ./internal/coordinator/
```

### Basic Health Check

```bash
# List all spaces
curl http://localhost:8899/spaces

# Read a specific space
curl http://localhost:8899/spaces/my-space/raw

# Post agent status
curl -X POST http://localhost:8899/spaces/my-space/agent/myagent \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: myagent' \
  -d '{"status":"active","summary":"myagent: testing"}'
```

## Container Build

### Local Docker Build

```bash
docker build -f deploy/Dockerfile -t boss-coordinator:latest .
```

### OpenShift Build

The project uses OpenShift's internal registry. Tag and push:

```bash
# Tag for OpenShift registry
docker tag boss-coordinator:latest \
  image-registry.openshift-image-registry.svc:5000/ambient-code/boss-coordinator:latest

# Push to registry (requires cluster login)
docker push image-registry.openshift-image-registry.svc:5000/ambient-code/boss-coordinator:latest
```

Or use OpenShift BuildConfig for automated builds:

```bash
oc new-build --binary --name=boss-coordinator -n ambient-code
oc start-build boss-coordinator --from-dir=. --follow -n ambient-code
```

## Kubernetes Deployment

### Deployment Architecture

```
┌─────────────────────────────────────────┐
│          External Access                │
│  (Route/Ingress: boss-coordinator-*)    │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│         Service: boss-coordinator       │
│            ClusterIP:8899               │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│     Deployment: boss-coordinator        │
│    - Port: 8899                         │
│    - Env: ConfigMap + Secret            │
│    - Volume: PVC (boss-data)            │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│      PVC: boss-data (1Gi RWO)           │
│      Mount: /data                       │
│      Contains: *.json, *.md             │
└─────────────────────────────────────────┘
```

### Step 1: Create Namespace

```bash
kubectl create namespace ambient-code
# or on OpenShift:
oc new-project ambient-code
```

### Step 2: Configure Secret

Create the ACP token secret. First, encode your token:

```bash
echo -n 'your-actual-acp-token' | base64
```

Then update `deploy/k8s/secret.yaml` with the base64-encoded value and apply:

```bash
kubectl apply -f deploy/k8s/secret.yaml
```

**Security Note**: Never commit actual tokens to git. Use a secret management tool in production.

### Step 3: Apply Configuration

```bash
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/pvc.yaml
```

### Step 4: Deploy Application

```bash
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
```

### Step 5: Verify Deployment

```bash
# Check pod status
kubectl get pods -n ambient-code -l app=boss-coordinator

# View logs
kubectl logs -n ambient-code -l app=boss-coordinator --tail=50

# Check readiness
kubectl get deployment boss-coordinator -n ambient-code
```

Expected output:
```
NAME               READY   UP-TO-DATE   AVAILABLE   AGE
boss-coordinator   1/1     1            1           30s
```

### Step 6: Create Route/Ingress (Optional)

For external access, create a Route (OpenShift) or Ingress (Kubernetes).

**OpenShift Route:**
```bash
oc expose service boss-coordinator -n ambient-code
oc get route boss-coordinator -n ambient-code
```

**Kubernetes Ingress:**
```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: boss-coordinator
  namespace: ambient-code
spec:
  rules:
  - host: boss.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: boss-coordinator
            port:
              number: 8899
```

## Configuration

### Environment Variables

| Variable | Source | Default | Description |
|----------|--------|---------|-------------|
| `DATA_DIR` | Hardcoded | `/data` | Persistence directory in container |
| `COORDINATOR_PORT` | ConfigMap | `8899` | HTTP listen port |
| `BOSS_EXTERNAL_URL` | ConfigMap | - | URL where ACP pods reach Boss |
| `ACP_URL` | ConfigMap | - | ACP public API gateway URL |
| `ACP_PROJECT` | ConfigMap | `default` | ACP project name |
| `ACP_MODEL` | ConfigMap | `claude-sonnet-4` | Default Claude model |
| `ACP_TIMEOUT` | ConfigMap | `900` | Session timeout (seconds) |
| `ACP_TOKEN` | Secret | - | ACP authentication token |
| `ACP_INSECURE_TLS` | ConfigMap (optional) | `false` | Skip TLS verification (dev/test only) |

### ConfigMap Updates

To update configuration:

```bash
# Edit configmap
kubectl edit configmap boss-config -n ambient-code

# Restart deployment to pick up changes
kubectl rollout restart deployment boss-coordinator -n ambient-code
```

### Data Persistence

The PVC (`boss-data`) stores:
- `{space}.json` - Canonical state (read on startup)
- `{space}.md` - Rendered markdown (regenerated on write)
- `protocol.md` - Agent communication protocol template

**Backup:**
```bash
kubectl exec -n ambient-code deployment/boss-coordinator -- tar czf - /data > boss-backup-$(date +%Y%m%d).tar.gz
```

**Restore:**
```bash
kubectl exec -i -n ambient-code deployment/boss-coordinator -- tar xzf - -C / < boss-backup-20260306.tar.gz
kubectl rollout restart deployment boss-coordinator -n ambient-code
```

## Troubleshooting

### Pod Won't Start

**Symptom:** Pod in CrashLoopBackOff or ImagePullBackOff

**Check:**
```bash
kubectl describe pod -n ambient-code -l app=boss-coordinator
kubectl logs -n ambient-code -l app=boss-coordinator
```

**Common causes:**
- Image not accessible: Verify registry credentials and image name
- PVC not bound: Check PV availability with `kubectl get pvc -n ambient-code`
- Secret missing: Verify `kubectl get secret boss-secrets -n ambient-code`

### 403 Forbidden on Agent POST

**Symptom:** Agent receives `403` response when posting status

**Cause:** `X-Agent-Name` header doesn't match URL path agent name

**Fix:**
```bash
# Wrong - Bob posting to API's channel
curl -X POST http://boss:8899/spaces/my-space/agent/API \
  -H 'X-Agent-Name: Bob'  # ❌ mismatch

# Correct
curl -X POST http://boss:8899/spaces/my-space/agent/API \
  -H 'X-Agent-Name: API'  # ✅ matches
```

### 400 Missing X-Agent-Name Header

**Symptom:** `400 Bad Request: missing X-Agent-Name header`

**Fix:** Add the required header to every POST:
```bash
curl -X POST http://boss:8899/spaces/my-space/agent/myagent \
  -H 'X-Agent-Name: myagent' \
  -H 'Content-Type: application/json' \
  -d '{"status":"active","summary":"myagent: working"}'
```

### Data Lost After Restart

**Symptom:** Spaces empty after pod restart

**Check PVC mount:**
```bash
kubectl exec -n ambient-code deployment/boss-coordinator -- ls -la /data
```

**Verify DATA_DIR:**
```bash
kubectl exec -n ambient-code deployment/boss-coordinator -- printenv DATA_DIR
```

Should show `/data`. If not, the deployment is using wrong environment variable.

### Dashboard Not Auto-Refreshing

**Symptom:** Dashboard at `/spaces/{space}/` doesn't poll every 3 seconds

**Check:**
1. Browser console for JavaScript errors
2. Network tab - should see requests to `/spaces/{space}/raw` every 3s
3. Ensure you're viewing `/spaces/{space}/` (trailing slash), not `/spaces/{space}/raw`

### High Memory Usage

**Symptom:** Pod OOMKilled or high memory consumption

**Cause:** Large agent sections or unbounded growth

**Mitigation:**
1. Agents should compact their sections periodically
2. Move completed work to Archive section
3. Set resource limits in deployment:

```yaml
resources:
  requests:
    memory: "128Mi"
    cpu: "100m"
  limits:
    memory: "512Mi"
    cpu: "500m"
```

## Operations

### Viewing Dashboard

**Local:**
```bash
open http://localhost:8899
```

**Kubernetes:**
```bash
# Port-forward for testing
kubectl port-forward -n ambient-code svc/boss-coordinator 8899:8899
open http://localhost:8899
```

**Production (with Route/Ingress):**
```bash
# Get external URL
kubectl get route boss-coordinator -n ambient-code -o jsonpath='{.spec.host}'
# or
kubectl get ingress boss-coordinator -n ambient-code
```

### Monitoring

**Health Checks:**
```bash
# Liveness (GET /spaces) - called every 30s
curl http://boss:8899/spaces

# Readiness (GET /spaces) - called every 10s
curl http://boss:8899/spaces
```

**Metrics to Watch:**
- Response time on `/spaces/{space}/raw`
- Number of agents per space
- PVC usage: `kubectl exec -n ambient-code deployment/boss-coordinator -- du -sh /data`

### Scaling

Boss Coordinator runs as a single replica due to in-memory state and file persistence. For high availability:

1. Use ReadWriteMany PVC (if available)
2. Implement leader election (future enhancement)
3. Or use active-passive setup with separate spaces per instance

### Upgrading

```bash
# Build new image
docker build -f deploy/Dockerfile -t boss-coordinator:v2 .
docker push <registry>/boss-coordinator:v2

# Update deployment
kubectl set image deployment/boss-coordinator \
  boss=<registry>/boss-coordinator:v2 \
  -n ambient-code

# Watch rollout
kubectl rollout status deployment/boss-coordinator -n ambient-code

# Rollback if needed
kubectl rollout undo deployment/boss-coordinator -n ambient-code
```

### Logs

```bash
# Real-time logs
kubectl logs -f -n ambient-code -l app=boss-coordinator

# Last 100 lines
kubectl logs -n ambient-code -l app=boss-coordinator --tail=100

# Logs from previous pod (if crashed)
kubectl logs -n ambient-code -l app=boss-coordinator --previous
```

### Performance Tuning

**For many concurrent agents:**
- Increase `COORDINATOR_PORT` timeout values if needed
- Monitor goroutine count and memory usage
- Consider rate limiting POST requests

**For large spaces:**
- Encourage agents to compact sections
- Archive old decisions
- Split into multiple spaces by feature area

### Restart Procedure

```bash
# Graceful restart
kubectl rollout restart deployment boss-coordinator -n ambient-code

# Hard restart (delete pod, let deployment recreate)
kubectl delete pod -n ambient-code -l app=boss-coordinator
```

**Data persists** - JSON files in PVC are loaded on startup.

### Disaster Recovery

1. **Regular backups** of the PVC (see Backup section)
2. **Git commit** critical decisions from `/spaces/{space}/raw` to project docs
3. **Archive** resolved items to reduce active context size
4. **Document** shared contracts separately as they represent institutional knowledge

### Security Considerations

1. **ACP Token:** Rotate regularly, never commit to git
2. **Network Policy:** Restrict ingress to only necessary sources
3. **RBAC:** Use ServiceAccount with minimal permissions
4. **TLS:** Terminate TLS at Ingress/Route, not in container
5. **Secrets:** Use external secret management (Vault, sealed-secrets) in production

## Quick Reference

### Common Commands

```bash
# Local dev
DATA_DIR=./data /tmp/boss serve

# Read space
curl http://localhost:8899/spaces/my-space/raw

# Post status
curl -X POST http://localhost:8899/spaces/my-space/agent/me \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Name: me' \
  -d '{"status":"active","summary":"me: working","items":["task1"]}'

# Check K8s deployment
kubectl get all -n ambient-code -l app=boss-coordinator

# View logs
kubectl logs -n ambient-code -l app=boss-coordinator --tail=50 -f

# Port-forward
kubectl port-forward -n ambient-code svc/boss-coordinator 8899:8899
```

### Deployment Checklist

- [ ] Go 1.24.4+ installed (local) or image built (K8s)
- [ ] Namespace created
- [ ] Secret created with ACP token
- [ ] ConfigMap applied
- [ ] PVC created and bound
- [ ] Deployment applied
- [ ] Service created
- [ ] Route/Ingress created (if external access needed)
- [ ] Health checks passing
- [ ] Dashboard accessible
- [ ] Agent can POST successfully
- [ ] Data persists across pod restart

## Support

- **Issues:** https://github.com/tiwillia/agent-boss-ambient/issues
- **Documentation:** README.md, CLAUDE.md
- **Source:** https://github.com/tiwillia/agent-boss-ambient
