#!/usr/bin/env bash
#
# Simulate the Argo CD repo-server environment locally and prove that the
# argocd-sops-cmp plugin renders a chart AND decrypts SOPS secrets.

set -euo pipefail

IMAGE="${IMAGE:-miladzanganeh/argocd-sops-cmp:v0.1.0}"
PASSPHRASE="${PASSPHRASE:-demo-passphrase}"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/argocd-sops-sim.XXXXXX")"
cleanup() { [ "${KEEP:-0}" = "1" ] || rm -rf "$SANDBOX"; }
trap cleanup EXIT

USERSPEC="$(id -u):$(id -g)"

echo ">> sandbox: $SANDBOX"
echo ">> image:   $IMAGE"
echo

# ---------------------------------------------------------------------------
# Step 1 — build fixtures inside the image.
# ---------------------------------------------------------------------------
echo ">> [1/2] generating GPG key, demo app, and SOPS-encrypting the secret..."
docker run --rm -i \
  --user "$USERSPEC" \
  -e HOME=/work \
  -e GNUPGHOME=/work/gnupg \
  -e PASSPHRASE="$PASSPHRASE" \
  -v "$SANDBOX":/work \
  --entrypoint bash \
  "$IMAGE" -s <<'SETUP'
set -euo pipefail

mkdir -p "$GNUPGHOME" /work/keys /work/app/templates /work/app/secrets
chmod 700 "$GNUPGHOME"
echo "allow-loopback-pinentry" > "$GNUPGHOME/gpg-agent.conf"

# --- A passphrase-protected key: primary (sign) + encryption subkey. --------
cat > /work/keyparams <<PARAMS
%echo generating demo key
Key-Type: RSA
Key-Length: 3072
Subkey-Type: RSA
Subkey-Length: 3072
Subkey-Usage: encrypt
Name-Real: argocd-sops-cmp demo
Name-Email: demo@example.com
Expire-Date: 0
Passphrase: ${PASSPHRASE}
%commit
%echo done
PARAMS
gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
    --gen-key /work/keyparams

# Primary fingerprint = the SOPS recipient.
FPR=$(gpg --with-colons --list-keys demo@example.com | awk -F: '/^fpr:/{print $10; exit}')

# Keygrip of the ENCRYPTION subkey — this is what the plugin presets the
# passphrase against so `sops --decrypt` can run unattended.
KEYGRIP=$(gpg --with-colons --with-keygrip --list-secret-keys demo@example.com | awk -F: '
  /^ssb:/ { enc = index($12, "e") }
  /^grp:/ { if (enc) { print $10; exit } }')

printf '%s' "$FPR"     > /work/fingerprint
printf '%s' "$KEYGRIP" > /work/keygrip

# Export the armored private key exactly like the argocd-sops-gpg Secret would.
gpg --batch --yes --pinentry-mode loopback --passphrase "$PASSPHRASE" \
    --armor --export-secret-keys "$FPR" > /work/keys/private-key.asc

# --- A tiny Helm chart so the demo shows rendering + decryption together. ----
cat > /work/app/Chart.yaml <<'CHART'
apiVersion: v2
name: demo
version: 0.1.0
CHART

cat > /work/app/values.yaml <<'VALUES'
greeting: hello-from-helm
VALUES

cat > /work/app/templates/configmap.yaml <<'TMPL'
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-config
data:
  greeting: {{ .Values.greeting | quote }}
TMPL

# --- A sample Secret, then encrypt it in place with SOPS + PGP. -------------
cat > /work/app/secrets/db-credentials.yaml <<'SECRET'
apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
type: Opaque
stringData:
  username: app
  password: s3cr3t-from-sops
SECRET

sops --encrypt --in-place --pgp "$FPR" /work/app/secrets/db-credentials.yaml

echo "   key fingerprint: $FPR"
echo "   subkey keygrip:  $KEYGRIP"
echo "   encrypted secret head:"
sed -n '1,6p' /work/app/secrets/db-credentials.yaml | sed 's/^/     /'
SETUP

KEYGRIP="$(cat "$SANDBOX/keygrip")"
echo
echo ">> ciphertext committed to Git would look like the above (no plaintext)."
echo

# ---------------------------------------------------------------------------
# Step 2 — run the plugin's `generate` phase as Argo CD would.
# ---------------------------------------------------------------------------
echo ">> [2/2] running 'argocd-sops-cmp generate' with the Argo CD environment..."
echo "-------------------------------------------------------------------------"
docker run --rm \
  --user "$USERSPEC" \
  -e HOME=/work \
  -e TMPDIR=/tmp \
  -e SOPS_GPG_KEY_FILE=/work/keys/private-key.asc \
  -e SOPS_GPG_KEYGRIP="$KEYGRIP" \
  -e SOPS_GPG_PASSPHRASE="$PASSPHRASE" \
  -e ARGOCD_APP_NAME=demo-app \
  -e ARGOCD_APP_NAMESPACE=demo \
  -w /work/app \
  -v "$SANDBOX":/work \
  --entrypoint argocd-sops-cmp \
  "$IMAGE" generate
echo "-------------------------------------------------------------------------"
echo
echo ">> Done. The ConfigMap above was rendered by Helm; the Secret below the"
echo "   '---' marker was decrypted from ciphertext by SOPS inside the sidecar."
[ "${KEEP:-0}" = "1" ] && echo ">> sandbox kept at: $SANDBOX"
exit 0

