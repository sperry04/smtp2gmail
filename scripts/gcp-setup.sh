#!/usr/bin/env bash
# Automates the scriptable portion of docs/google-workspace-setup.md:
# project creation, enabling the Gmail API, service account + key creation,
# and the project-scoped org-policy exception for key creation.
#
# What this script does NOT do (Google doesn't expose an API for it):
# grant domain-wide delegation in Workspace Admin. This script prints the
# exact values you need for that one remaining manual step at the end.
#
# Prerequisites:
#   - gcloud CLI installed and authenticated as a user with rights to create
#     projects and set organization policies in your Cloud organization
#     (`gcloud auth login`, using your Workspace super admin account).
#
# Usage:
#   ./gcp-setup.sh PROJECT_ID [SERVICE_ACCOUNT_NAME]
#
# Example:
#   ./gcp-setup.sh smtp2gmail-urabus smtp2gmail-sender

set -euo pipefail

if ! command -v gcloud >/dev/null 2>&1; then
  echo "error: gcloud CLI not found on PATH." >&2
  echo "Install it first: https://cloud.google.com/sdk/docs/install" >&2
  echo "  Ubuntu/Debian via apt:" >&2
  echo "    sudo apt-get update && sudo apt-get install -y apt-transport-https ca-certificates gnupg curl" >&2
  echo "    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg" >&2
  echo "    echo \"deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main\" | sudo tee /etc/apt/sources.list.d/google-cloud-sdk.list" >&2
  echo "    sudo apt-get update && sudo apt-get install -y google-cloud-cli" >&2
  echo "  (or via snap: sudo snap install google-cloud-cli --classic)" >&2
  exit 1
fi

PROJECT_ID="${1:?Usage: $0 PROJECT_ID [SERVICE_ACCOUNT_NAME]}"
SA_NAME="${2:-smtp2gmail-sender}"
SA_EMAIL="${SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
KEY_FILE="./${SA_NAME}-key.json"

echo "==> Creating project ${PROJECT_ID}..."
if gcloud projects describe "${PROJECT_ID}" >/dev/null 2>&1; then
  echo "    Project already exists and is visible to you, skipping creation."
else
  if ! CREATE_OUTPUT=$(gcloud projects create "${PROJECT_ID}" 2>&1); then
    echo "${CREATE_OUTPUT}" >&2
    if echo "${CREATE_OUTPUT}" | grep -qi "already in use"; then
      cat >&2 <<EOF

note: project IDs are globally unique across ALL Google Cloud customers, not
just your account/org -- a short, generic ID like '${PROJECT_ID}' is very
likely already claimed by an unrelated project you can't see. Try a more
specific ID, e.g.: ${PROJECT_ID}-$(date +%s | tail -c 6)
EOF
    fi
    exit 1
  fi
  echo "${CREATE_OUTPUT}"
fi

echo "==> Setting active project for this session..."
gcloud config set project "${PROJECT_ID}" >/dev/null

echo ""
echo "==> Billing: Gmail API usage here is free, but Google Cloud may still"
echo "    require a billing account attached to the project before some"
echo "    operations succeed. If a later step fails asking for billing, run:"
echo "      gcloud billing accounts list"
echo "      gcloud billing projects link ${PROJECT_ID} --billing-account=BILLING_ACCOUNT_ID"
echo ""

echo "==> Enabling the Gmail API..."
gcloud services enable gmail.googleapis.com --project="${PROJECT_ID}"

echo "==> Creating service account ${SA_NAME}..."
if gcloud iam service-accounts describe "${SA_EMAIL}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  echo "    Service account already exists, skipping creation."
else
  gcloud iam service-accounts create "${SA_NAME}" \
    --project="${PROJECT_ID}" \
    --display-name="smtp2gmail sender"
fi

echo "==> Granting project-scoped exceptions to allow service account key creation..."
echo "    (scoped to this project only — does not weaken the org-wide default policy)"
echo "    Google has both a legacy constraint and a newer 'managed constraint' for"
echo "    this; some orgs enforce one, some the other, some both — so this overrides both."
for CONSTRAINT in iam.disableServiceAccountKeyCreation iam.managed.disableServiceAccountKeyCreation; do
  POLICY_FILE="$(mktemp)"
  cat > "${POLICY_FILE}" <<EOF
name: projects/${PROJECT_ID}/policies/${CONSTRAINT}
spec:
  rules:
    - enforce: false
EOF
  if ! ERR="$(gcloud org-policies set-policy "${POLICY_FILE}" 2>&1)"; then
    if echo "${ERR}" | grep -qi "not found"; then
      echo "    (${CONSTRAINT}: not defined in this org, nothing to override, skipping)"
    else
      echo "${ERR}" >&2
      rm -f "${POLICY_FILE}"
      exit 1
    fi
  else
    echo "    ${CONSTRAINT}: overridden for this project"
  fi
  rm -f "${POLICY_FILE}"
done

echo "==> Creating JSON key at ${KEY_FILE}..."
if [ -s "${KEY_FILE}" ]; then
  echo "    ${KEY_FILE} already exists locally and is non-empty — refusing to overwrite. Delete it first if you want a new key."
else
  if [ -f "${KEY_FILE}" ]; then
    echo "    ${KEY_FILE} exists but is empty (likely left over from a previous failed attempt) — removing it and retrying."
    rm -f "${KEY_FILE}"
  fi
  gcloud iam service-accounts keys create "${KEY_FILE}" \
    --iam-account="${SA_EMAIL}" \
    --project="${PROJECT_ID}"
fi

echo "==> Reading OAuth Client ID (needed for domain-wide delegation)..."
CLIENT_ID="$(gcloud iam service-accounts describe "${SA_EMAIL}" --project="${PROJECT_ID}" --format='value(uniqueId)')"

cat <<EOF

--------------------------------------------------------------------------
Scriptable setup is done. Everything below this line is the one remaining
step Google requires to be done by hand in Workspace Admin:

  1. Go to https://admin.google.com -> Security -> Access and data control
     -> API Controls -> Domain-wide Delegation -> Manage Domain Wide
     Delegation -> Add new
  2. Client ID:   ${CLIENT_ID}
  3. OAuth scope: https://www.googleapis.com/auth/gmail.send
  4. Click Authorize.

Service account JSON key saved to: ${KEY_FILE}
Treat this file as sensitive — see docs/security.md. Do not commit it.
--------------------------------------------------------------------------
EOF
