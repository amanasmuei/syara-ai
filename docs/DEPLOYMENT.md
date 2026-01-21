# Deployment and Operations Guide

## Deployment Options

### 1. Docker Compose (Development/Staging)

```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
```

### 2. Docker Compose with Production Profile

```bash
# Copy production override
cp docker-compose.override.yml.example docker-compose.override.yml
# Edit with production values

# Start with production settings
docker-compose -f docker-compose.yml -f docker-compose.override.yml up -d
```

### 3. Kubernetes (Production)

See the Kubernetes manifests in the `k8s/` directory.

## Environment Configuration

### Required Environment Variables

```bash
# Server
PORT=8080
ENVIRONMENT=production
SHUTDOWN_TIMEOUT=30

# Database
DB_HOST=postgres
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=<strong-password>
DB_NAME=islamic_banking
DB_SSL_MODE=require
DB_MAX_OPEN_CONNS=25
DB_MAX_IDLE_CONNS=5

# Redis
REDIS_HOST=redis
REDIS_PORT=6379
REDIS_PASSWORD=<redis-password>
REDIS_DB=0

# NATS
NATS_URL=nats://nats:4222
NATS_CLUSTER_ID=islamic-banking

# Object Storage
STORAGE_ENDPOINT=minio:9000
STORAGE_ACCESS_KEY=<access-key>
STORAGE_SECRET_KEY=<secret-key>
STORAGE_BUCKET=sharia-comply
STORAGE_USE_SSL=true
STORAGE_REGION=us-east-1

# LLM Configuration
LLM_PROVIDER=anthropic
ANTHROPIC_API_KEY=<your-api-key>
OPENAI_API_KEY=<your-api-key>
LLM_MODEL=claude-sonnet-4-20250514
EMBEDDING_MODEL=text-embedding-3-small
LLM_MAX_TOKENS=4096

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
```

### Secrets Management

For production, use a secrets manager:

```bash
# AWS Secrets Manager
aws secretsmanager create-secret \
  --name islamic-banking/production \
  --secret-string file://secrets.json

# Kubernetes Secrets
kubectl create secret generic islamic-banking-secrets \
  --from-env-file=.env.production
```

## Docker Compose Deployment

### docker-compose.yml Services

| Service | Port | Description |
|---------|------|-------------|
| `agent` | 8080 | Main API server |
| `worker` | 8081 | Background worker |
| `postgres` | 5432 | PostgreSQL with pgvector |
| `redis` | 6379 | Redis cache |
| `nats` | 4222, 8222 | NATS JetStream |
| `minio` | 9000, 9001 | Object storage |

### Starting Services

```bash
# Start infrastructure only
docker-compose up -d postgres redis nats minio

# Wait for PostgreSQL to be ready
docker-compose exec postgres pg_isready -U postgres

# Run migrations
export DATABASE_URL="postgres://postgres:devpassword@localhost:5432/islamic_banking?sslmode=disable"
migrate -path ./migrations -database "$DATABASE_URL" up

# Start application services
docker-compose up -d agent worker
```

### Health Checks

```bash
# API health
curl http://localhost:8080/health

# API readiness
curl http://localhost:8080/ready

# Worker metrics
curl http://localhost:8081/metrics
```

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (1.28+)
- kubectl configured
- Helm 3.x (optional)

### Namespace Setup

```bash
kubectl create namespace islamic-banking
kubectl config set-context --current --namespace=islamic-banking
```

### Deploy Infrastructure

```yaml
# k8s/postgres.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
spec:
  serviceName: postgres
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
      - name: postgres
        image: pgvector/pgvector:pg16
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_DB
          value: islamic_banking
        - name: POSTGRES_USER
          valueFrom:
            secretKeyRef:
              name: postgres-secret
              key: username
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-secret
              key: password
        volumeMounts:
        - name: postgres-data
          mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
  - metadata:
      name: postgres-data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 50Gi
```

### Deploy Application

```yaml
# k8s/agent-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: islamic-banking-agent
spec:
  replicas: 3
  selector:
    matchLabels:
      app: islamic-banking-agent
  template:
    metadata:
      labels:
        app: islamic-banking-agent
    spec:
      containers:
      - name: agent
        image: your-registry/islamic-banking-agent:latest
        ports:
        - containerPort: 8080
        envFrom:
        - configMapRef:
            name: islamic-banking-config
        - secretRef:
            name: islamic-banking-secrets
        resources:
          requests:
            memory: "512Mi"
            cpu: "250m"
          limits:
            memory: "2Gi"
            cpu: "1000m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
```

### Ingress Configuration

```yaml
# k8s/ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: islamic-banking-ingress
  annotations:
    nginx.ingress.kubernetes.io/rate-limit: "100"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  tls:
  - hosts:
    - api.shariacomply.ai
    secretName: shariacomply-tls
  rules:
  - host: api.shariacomply.ai
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: islamic-banking-agent
            port:
              number: 8080
```

## Monitoring Setup

### Prometheus Metrics

The application exposes metrics at `/metrics`:

```yaml
# k8s/servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: islamic-banking
spec:
  selector:
    matchLabels:
      app: islamic-banking-agent
  endpoints:
  - port: http
    path: /metrics
    interval: 30s
```

### Key Metrics

| Metric | Description |
|--------|-------------|
| `http_requests_total` | Total HTTP requests |
| `http_request_duration_seconds` | Request latency histogram |
| `chat_requests_total` | Chat API requests |
| `rag_retrieval_duration_seconds` | RAG retrieval latency |
| `llm_tokens_used_total` | LLM tokens consumed |

### Grafana Dashboard

Import the provided dashboard from `monitoring/grafana-dashboard.json`.

### Alerting Rules

```yaml
# monitoring/alerts.yaml
groups:
- name: islamic-banking
  rules:
  - alert: HighErrorRate
    expr: rate(http_requests_total{status=~"5.."}[5m]) > 0.1
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: High error rate detected

  - alert: SlowResponses
    expr: histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m])) > 5
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: P95 latency above 5 seconds
```

## Logging

### Log Format

All services use structured JSON logging:

```json
{
  "time": "2024-01-15T10:30:00Z",
  "level": "INFO",
  "msg": "chat request processed",
  "request_id": "abc123",
  "conversation_id": "def456",
  "latency_ms": 1250,
  "tokens_used": 1500
}
```

### Log Aggregation

Configure Fluentd/Fluent Bit to ship logs:

```yaml
# fluentd config
<source>
  @type tail
  path /var/log/containers/islamic-banking-*.log
  tag kubernetes.*
  <parse>
    @type json
  </parse>
</source>

<match kubernetes.**>
  @type elasticsearch
  host elasticsearch.logging.svc
  port 9200
  logstash_format true
</match>
```

## Backup and Recovery

### Database Backup

```bash
# Manual backup
pg_dump -h postgres -U postgres islamic_banking | gzip > backup_$(date +%Y%m%d).sql.gz

# Restore from backup
gunzip -c backup_20240115.sql.gz | psql -h postgres -U postgres islamic_banking
```

### Automated Backups (Kubernetes)

```yaml
# k8s/backup-cronjob.yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: postgres-backup
spec:
  schedule: "0 2 * * *"  # Daily at 2 AM
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: backup
            image: postgres:16
            command:
            - /bin/sh
            - -c
            - |
              pg_dump -h postgres -U postgres islamic_banking | \
              gzip | \
              aws s3 cp - s3://backups/islamic-banking/$(date +%Y%m%d).sql.gz
          restartPolicy: OnFailure
```

## Troubleshooting

### Common Issues

#### 1. Database Connection Failures

```bash
# Check PostgreSQL status
kubectl exec -it postgres-0 -- pg_isready

# Check connection from agent
kubectl exec -it deploy/islamic-banking-agent -- nc -zv postgres 5432
```

#### 2. High Memory Usage

```bash
# Check memory usage
kubectl top pods

# Increase limits if needed
kubectl patch deployment islamic-banking-agent -p '{"spec":{"template":{"spec":{"containers":[{"name":"agent","resources":{"limits":{"memory":"4Gi"}}}]}}}}'
```

#### 3. NATS Connection Issues

```bash
# Check NATS health
kubectl exec -it nats-0 -- nats-server --version

# View stream status
kubectl exec -it nats-0 -- nats stream ls
```

#### 4. Slow Responses

- Check RAG retrieval latency in metrics
- Verify vector index is created: `\di chunks*` in psql
- Check Redis cache hit rate
- Review LLM token usage

### Debug Mode

Enable debug logging:

```bash
kubectl set env deployment/islamic-banking-agent LOG_LEVEL=debug
```

### Log Investigation

```bash
# Recent errors
kubectl logs -l app=islamic-banking-agent --since=1h | grep -i error

# Specific request
kubectl logs -l app=islamic-banking-agent | grep "request_id=abc123"
```

## Scaling

### Horizontal Pod Autoscaler

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: islamic-banking-agent
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: islamic-banking-agent
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

### Database Scaling

For read-heavy workloads, add read replicas:

```bash
# Create read replica
kubectl apply -f k8s/postgres-replica.yaml

# Update app config to use replica for reads
kubectl set env deployment/islamic-banking-agent DB_READ_HOST=postgres-replica
```
