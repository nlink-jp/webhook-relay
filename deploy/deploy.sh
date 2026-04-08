#!/usr/bin/env bash
# deploy.sh — Deploy webhook-relay with VPC network isolation
#
# Creates: VPC, subnet, Cloud Run Service (with Direct VPC egress),
#          Artifact Registry, service account, IAM, Secret Manager.
#
# Usage: ./deploy/deploy.sh <config-file>

set -euo pipefail

log()  { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
err()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\n\033[1;33m── %s ──\033[0m\n' "$*"; }

CONFIG_FILE="${1:-}"
[[ -z "$CONFIG_FILE" ]] && err "Usage: $0 <config-file>"
[[ -f "$CONFIG_FILE" ]] || err "Config file not found: $CONFIG_FILE"
# shellcheck source=/dev/null
source "$CONFIG_FILE"

# ── Required variables ───────────────────────────────────────────────
: "${PROJECT_ID:?}"
: "${REGION:?}"
: "${GCS_BUCKET:?}"
: "${SERVICE_NAME:=webhook-relay}"
: "${SA_NAME:=webhook-relay-sa}"
: "${REPO_NAME:=webhook-relay}"
: "${VPC_NAME:=webhook-relay-vpc}"
: "${SUBNET_NAME:=webhook-relay-subnet}"
: "${SUBNET_RANGE:=10.100.0.0/28}"
: "${MAX_INSTANCES:=3}"
: "${MIN_INSTANCES:=0}"
: "${RATE_LIMIT_RPS:=10}"
: "${RATE_LIMIT_BURST:=20}"
: "${MAX_REQUEST_BYTES:=26214400}"
: "${ALLOWED_EXTENSIONS:=.eml,.msg}"

_IAM_DOMAIN="iam.gserviceaccount.com"
SA_EMAIL="${SA_NAME}@${PROJECT_ID}.${_IAM_DOMAIN}"
IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${REPO_NAME}/${SERVICE_NAME}:latest"

log "Project:  $PROJECT_ID"
log "Region:   $REGION"
log "Service:  $SERVICE_NAME"
log "VPC:      $VPC_NAME"
log "Bucket:   $GCS_BUCKET"

# ── Enable APIs ──────────────────────────────────────────────────────
step "Enabling required APIs"
gcloud services enable \
  run.googleapis.com \
  artifactregistry.googleapis.com \
  storage.googleapis.com \
  secretmanager.googleapis.com \
  compute.googleapis.com \
  vpcaccess.googleapis.com \
  --project="$PROJECT_ID" --quiet

# ── VPC Network ──────────────────────────────────────────────────────
step "Setting up VPC network"
if ! gcloud compute networks describe "$VPC_NAME" --project="$PROJECT_ID" &>/dev/null; then
  gcloud compute networks create "$VPC_NAME" \
    --project="$PROJECT_ID" \
    --subnet-mode=custom \
    --bgp-routing-mode=regional
  log "Created VPC: $VPC_NAME"
else
  log "VPC already exists: $VPC_NAME"
fi

# ── Subnet with Private Google Access ────────────────────────────────
step "Setting up subnet"
if ! gcloud compute networks subnets describe "$SUBNET_NAME" \
    --region="$REGION" --project="$PROJECT_ID" &>/dev/null; then
  gcloud compute networks subnets create "$SUBNET_NAME" \
    --project="$PROJECT_ID" \
    --network="$VPC_NAME" \
    --region="$REGION" \
    --range="$SUBNET_RANGE" \
    --enable-private-ip-google-access
  log "Created subnet: $SUBNET_NAME (Private Google Access ON)"
else
  # Ensure Private Google Access is enabled
  gcloud compute networks subnets update "$SUBNET_NAME" \
    --region="$REGION" \
    --project="$PROJECT_ID" \
    --enable-private-ip-google-access \
    --quiet 2>/dev/null || true
  log "Subnet already exists: $SUBNET_NAME"
fi

# ── Firewall: deny all ingress (Cloud Run handles its own) ──────────
step "Configuring firewall"
DENY_RULE="${VPC_NAME}-deny-all-ingress"
if ! gcloud compute firewall-rules describe "$DENY_RULE" --project="$PROJECT_ID" &>/dev/null; then
  gcloud compute firewall-rules create "$DENY_RULE" \
    --project="$PROJECT_ID" \
    --network="$VPC_NAME" \
    --direction=INGRESS \
    --action=DENY \
    --rules=all \
    --source-ranges="0.0.0.0/0" \
    --priority=65534
  log "Created firewall rule: $DENY_RULE"
else
  log "Firewall rule already exists: $DENY_RULE"
fi

# ── Service Account ─────────────────────────────────────────────────
step "Setting up service account"
if ! gcloud iam service-accounts describe "$SA_EMAIL" --project="$PROJECT_ID" &>/dev/null; then
  gcloud iam service-accounts create "$SA_NAME" \
    --display-name="webhook-relay service account" \
    --project="$PROJECT_ID"
  log "Created service account: $SA_EMAIL"
else
  log "Service account already exists: $SA_EMAIL"
fi

ROLES=(
  "roles/storage.objectCreator"
  "roles/secretmanager.secretAccessor"
  "roles/logging.logWriter"
)
for role in "${ROLES[@]}"; do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:${SA_EMAIL}" \
    --role="$role" \
    --condition=None \
    --quiet &>/dev/null
done
log "IAM roles assigned (objectCreator — write only, no read/delete)"

# ── GCS Bucket ───────────────────────────────────────────────────────
step "Setting up GCS bucket"
if ! gcloud storage buckets describe "gs://${GCS_BUCKET}" --project="$PROJECT_ID" &>/dev/null; then
  gcloud storage buckets create "gs://${GCS_BUCKET}" \
    --project="$PROJECT_ID" \
    --location="$REGION" \
    --uniform-bucket-level-access
  log "Created bucket: gs://${GCS_BUCKET}"
else
  log "Bucket already exists: gs://${GCS_BUCKET}"
fi

# ── Artifact Registry ───────────────────────────────────────────────
step "Setting up Artifact Registry"
if ! gcloud artifacts repositories describe "$REPO_NAME" \
    --location="$REGION" --project="$PROJECT_ID" &>/dev/null; then
  gcloud artifacts repositories create "$REPO_NAME" \
    --repository-format=docker \
    --location="$REGION" \
    --project="$PROJECT_ID"
  log "Created repository: $REPO_NAME"
else
  log "Repository already exists: $REPO_NAME"
fi

# ── Build & Push Container ──────────────────────────────────────────
step "Building and pushing container"
gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet
docker build -t "$IMAGE" -f deploy/Dockerfile .
docker push "$IMAGE"
log "Pushed: $IMAGE"

# ── API Key Secret ───────────────────────────────────────────────────
step "Setting up API key secret"
SECRET_NAME="webhook-relay-api-key"
if ! gcloud secrets describe "$SECRET_NAME" --project="$PROJECT_ID" &>/dev/null; then
  gcloud secrets create "$SECRET_NAME" --project="$PROJECT_ID" --replication-policy=automatic
  log "Created secret: $SECRET_NAME"
  log "Add the API key value:"
  log "  openssl rand -hex 32 | gcloud secrets versions add $SECRET_NAME --data-file=- --project=$PROJECT_ID"
else
  log "Secret already exists: $SECRET_NAME"
fi

# ── Cloud Run Service ───────────────────────────────────────────────
step "Deploying Cloud Run Service"

ENV_VARS="WEBHOOK_RELAY_GCS_BUCKET=${GCS_BUCKET}"
ENV_VARS="${ENV_VARS},WEBHOOK_RELAY_GCS_PROJECT=${PROJECT_ID}"
ENV_VARS="${ENV_VARS},WEBHOOK_RELAY_RATE_LIMIT_RPS=${RATE_LIMIT_RPS}"
ENV_VARS="${ENV_VARS},WEBHOOK_RELAY_RATE_LIMIT_BURST=${RATE_LIMIT_BURST}"
ENV_VARS="${ENV_VARS},WEBHOOK_RELAY_MAX_REQUEST_BYTES=${MAX_REQUEST_BYTES}"
ENV_VARS="${ENV_VARS},WEBHOOK_RELAY_ALLOWED_EXTENSIONS=${ALLOWED_EXTENSIONS}"

gcloud run deploy "$SERVICE_NAME" \
  --image="$IMAGE" \
  --region="$REGION" \
  --project="$PROJECT_ID" \
  --service-account="$SA_EMAIL" \
  --set-env-vars="$ENV_VARS" \
  --set-secrets="WEBHOOK_RELAY_API_KEY=${SECRET_NAME}:latest" \
  --network="$VPC_NAME" \
  --subnet="$SUBNET_NAME" \
  --vpc-egress=all-traffic \
  --ingress=all \
  --allow-unauthenticated \
  --cpu-throttling \
  --min-instances="$MIN_INSTANCES" \
  --max-instances="$MAX_INSTANCES" \
  --memory=256Mi \
  --cpu=1 \
  --timeout=60 \
  --quiet

SERVICE_URL=$(gcloud run services describe "$SERVICE_NAME" \
  --region="$REGION" --project="$PROJECT_ID" \
  --format='value(status.url)')

# ── Summary ──────────────────────────────────────────────────────────
step "Deployment complete"
log ""
log "Resources:"
log "  VPC:      $VPC_NAME"
log "  Subnet:   $SUBNET_NAME ($SUBNET_RANGE, Private Google Access)"
log "  Service:  $SERVICE_NAME"
log "  URL:      $SERVICE_URL"
log "  SA:       $SA_EMAIL"
log "  Bucket:   gs://${GCS_BUCKET}"
log ""
log "Next steps:"
log "  1. Generate and store the API key:"
log "     openssl rand -hex 32 | gcloud secrets versions add ${SECRET_NAME} --data-file=- --project=${PROJECT_ID}"
log "  2. Test the webhook:"
log '     curl -X POST "${SERVICE_URL}/ingest/gcs/inbox/test.eml" \'
log '       -H "X-API-Key: YOUR_API_KEY" \'
log '       -H "Content-Type: application/octet-stream" \'
log '       --data-binary @test.eml'
log "  3. Configure Power Automate HTTP action with:"
log "     URL:    ${SERVICE_URL}/ingest/gcs/inbox/{{filename}}"
log "     Method: POST"
log "     Header: X-API-Key = YOUR_API_KEY"
log "     Body:   Email content (binary)"
