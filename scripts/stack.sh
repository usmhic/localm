#!/usr/bin/env bash
# LocalM local stack: Ollama + SearXNG behind LocalM, with an optional Cloudflare tunnel.
# See docs/local-stack.md.
#
#   scripts/stack.sh up [--size small|medium|large] [--model M] [--light-model M]
#                       [--embedding-model M] [--tunnel] [--native-ollama]
#   scripts/stack.sh pull       pull (or update) the chosen models
#   scripts/stack.sh check      test auth, chat, structured output, tools, embeddings and search
#   scripts/stack.sh env        print the connection settings for your client
#   scripts/stack.sh url        print the tunnel's public address
#   scripts/stack.sh logs | down
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STACK="$ROOT/deploy/stack"
ENV_FILE="$STACK/.env"

say() { printf '\033[1m›\033[0m %s\n' "$*"; }
fail() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }
ok() { printf '\033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m %s\n' "$*"; }

# Reads KEY from the stack's .env (empty if unset).
get() { [ -f "$ENV_FILE" ] && grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true; }
# Writes KEY=VALUE into the stack's .env, replacing an existing line. The file is git-ignored.
put() {
  touch "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  if grep -qE "^$1=" "$ENV_FILE"; then
    local tmp; tmp="$(mktemp)"; grep -vE "^$1=" "$ENV_FILE" > "$tmp"; mv "$tmp" "$ENV_FILE"; chmod 600 "$ENV_FILE"
  fi
  printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"
}

secret() { openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

compose() {
  local files=(-f "$STACK/compose.yaml")
  if [ "$(get NATIVE_OLLAMA)" != "true" ] && command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1; then
    files+=(-f "$STACK/compose.gpu.yaml")
  fi
  docker compose --env-file "$ENV_FILE" "${files[@]}" "$@"
}

port() { local p; p="$(get LOCALM_PORT)"; echo "${p:-8080}"; }
local_url() { echo "http://127.0.0.1:$(port)"; }
auth() { echo "Authorization: Bearer $(get LOCALM_API_KEYS | cut -d, -f1)"; }

# Model sets by GPU memory: a main model for analysis and tool use, a light one for high-volume
# work, and an embedding model. Override any of them with --model, --light-model and
# --embedding-model.
choose_models() {
  case "$1" in
    small)  put LOCALM_MODEL qwen3:8b;  put LOCALM_LIGHT_MODEL qwen3:4b ;;  # 8–12 GB, or a 16 GB Mac
    medium) put LOCALM_MODEL qwen3:14b; put LOCALM_LIGHT_MODEL qwen3:4b ;;  # 16–24 GB, or a 32 GB Mac
    large)  put LOCALM_MODEL qwen3:32b; put LOCALM_LIGHT_MODEL qwen3:8b ;;  # 40 GB+, or a 64 GB Mac
    *) fail "Unknown size '$1': use small, medium or large." ;;
  esac
  [ -n "$(get LOCALM_EMBEDDING_MODEL)" ] || put LOCALM_EMBEDDING_MODEL nomic-embed-text
}

models() { printf '%s\n' "$(get LOCALM_MODEL)" "$(get LOCALM_LIGHT_MODEL)" "$(get LOCALM_EMBEDDING_MODEL)" | grep . | awk '!seen[$0]++'; }
allow_models() { put LOCALM_ALLOWED_MODELS "$(models | paste -sd, -)"; }

ollama_pull() {
  if [ "$(get NATIVE_OLLAMA)" = "true" ]; then ollama pull "$1"; else compose exec -T ollama ollama pull "$1"; fi
}

cmd_pull() {
  while read -r m; do
    say "Pulling $m"
    ollama_pull "$m"
  done < <(models)
}

wait_for_localm() {
  for _ in $(seq 1 90); do
    curl -fsS "$(local_url)/healthz" >/dev/null 2>&1 && return 0
    sleep 2
  done
  fail "LocalM didn't come up on $(local_url). Run: scripts/stack.sh logs"
}

cmd_url() {
  if [ -n "$(get CLOUDFLARE_TUNNEL_TOKEN)" ]; then
    echo "(your named tunnel's hostname, set in the Cloudflare dashboard)"; return
  fi
  local url=""
  for _ in $(seq 1 30); do
    url="$(compose logs tunnel 2>/dev/null | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true)"
    [ -n "$url" ] && break
    sleep 2
  done
  [ -n "$url" ] || fail "No tunnel address yet. Is the tunnel running? (scripts/stack.sh up --tunnel)"
  echo "$url"
}

cmd_env() {
  local base="${1:-}"
  if [ -z "$base" ]; then
    if compose --profile tunnel ps --status running tunnel 2>/dev/null | grep -q tunnel; then base="$(cmd_url)"; else base="$(local_url)"; fi
  fi
  cat <<EOF

Connect any OpenAI-compatible client with:

  LOCALM_BASE_URL=$base/v1
  LOCALM_API_KEY=$(get LOCALM_API_KEYS | cut -d, -f1)
  LOCALM_MODEL=$(get LOCALM_MODEL)
  LOCALM_LIGHT_MODEL=$(get LOCALM_LIGHT_MODEL)
  LOCALM_EMBEDDING_MODEL=$(get LOCALM_EMBEDDING_MODEL)

Web research: POST $base/v1/search with the same key (docs/search.md).
Status:       GET  $base/v1/capabilities
EOF
}

cmd_check() {
  local base; base="$(local_url)"
  local model; model="$(get LOCALM_MODEL)"
  say "LocalM refuses requests without a key"
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$base/v1/models")" = "401" ] && ok "401 without a key" || fail "LocalM answered without a key."

  say "Models"
  curl -fsS -H "$(auth)" "$base/v1/capabilities" | python3 -c '
import json, sys
body = json.load(sys.stdin)
for connection in body["data"]:
    for m in connection["models"]:
        print("  %-28s %s" % (m["id"], "available" if m["available"] else "NOT AVAILABLE (pull it)"))
'

  say "Chat with $model"
  local started; started=$(date +%s)
  curl -fsS -H "$(auth)" -H 'content-type: application/json' "$base/v1/chat/completions" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with the single word: ready\"}],\"max_tokens\":200}" \
    | grep -q '"content"' && ok "answered in $(( $(date +%s) - started ))s" || fail "No answer from $model."

  say "Structured output (json_schema)"
  curl -fsS -H "$(auth)" -H 'content-type: application/json' "$base/v1/chat/completions" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Rate 'great pasta, slow service' from 1 to 5.\"}],\"response_format\":{\"type\":\"json_schema\",\"json_schema\":{\"name\":\"r\",\"schema\":{\"type\":\"object\",\"properties\":{\"rating\":{\"type\":\"integer\"}},\"required\":[\"rating\"]}}}}" \
    | grep -q 'rating' && ok "returns JSON that fits a schema" || fail "Structured output failed."

  say "Tool calling"
  curl -fsS -H "$(auth)" -H 'content-type: application/json' "$base/v1/chat/completions" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"What is the temperature in Rabat?\"}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"get_temperature\",\"description\":\"The current temperature in a city\",\"parameters\":{\"type\":\"object\",\"properties\":{\"city\":{\"type\":\"string\"}},\"required\":[\"city\"]}}}]}" \
    | grep -q 'tool_calls' && ok "calls tools" || warn "The model answered without calling the tool; agents need a model with tool support."

  say "Embeddings"
  local dims
  dims="$(curl -fsS -H "$(auth)" -H 'content-type: application/json' "$base/v1/embeddings" \
    -d "{\"model\":\"$(get LOCALM_EMBEDDING_MODEL)\",\"input\":\"wait times\"}" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["data"][0]["embedding"]))')"
  [ -n "$dims" ] && ok "$(get LOCALM_EMBEDDING_MODEL): $dims dimensions" || fail "Embeddings failed."

  say "Web research"
  curl -fsS -H "$(auth)" -H 'content-type: application/json' "$base/v1/search" \
    -d '{"query":"coffee shop reviews","max_results":3,"read_pages":1}' | python3 -c '
import json, sys
body = json.load(sys.stdin)
read = sum(1 for s in body["data"] if s.get("content"))
print("  %d results, %d page(s) read" % (len(body["data"]), read))
' && ok "search works" || fail "Search failed."
  ok "All set."
}

cmd_up() {
  local size="" tunnel=false native=false model="" light="" embedding=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --size) size="$2"; shift 2 ;;
      --model) model="$2"; shift 2 ;;
      --light-model) light="$2"; shift 2 ;;
      --embedding-model) embedding="$2"; shift 2 ;;
      --tunnel) tunnel=true; shift ;;
      --native-ollama) native=true; shift ;;
      *) fail "Unknown option $1" ;;
    esac
  done
  command -v docker >/dev/null || fail "Docker is required."

  [ -n "$(get LOCALM_API_KEYS)" ] || { put LOCALM_API_KEYS "$(secret)"; ok "Created an API key"; }
  [ -n "$(get SEARXNG_SECRET)" ] || put SEARXNG_SECRET "$(secret)"
  if [ -n "$size" ] || [ -z "$(get LOCALM_MODEL)" ]; then choose_models "${size:-medium}"; fi
  [ -z "$model" ] || put LOCALM_MODEL "$model"
  [ -z "$light" ] || put LOCALM_LIGHT_MODEL "$light"
  [ -z "$embedding" ] || put LOCALM_EMBEDDING_MODEL "$embedding"
  allow_models
  if $native; then
    command -v ollama >/dev/null || fail "--native-ollama needs Ollama installed on this machine (ollama.com/download)."
    put NATIVE_OLLAMA true; put OLLAMA_BASE_URL http://host.docker.internal:11434
  fi
  if [ -n "$(get CLOUDFLARE_TUNNEL_TOKEN)" ]; then put CLOUDFLARE_TUNNEL_COMMAND "tunnel --no-autoupdate run"; fi

  local services=(localm searxng)
  [ "$(get NATIVE_OLLAMA)" = "true" ] || services+=(ollama)
  say "Starting ${services[*]}"
  compose up -d --build "${services[@]}"
  wait_for_localm
  ok "LocalM on $(local_url)"
  cmd_pull

  say "Warming up $(get LOCALM_MODEL)"
  curl -fsS -m 600 -H "$(auth)" -H 'content-type: application/json' "$(local_url)/v1/chat/completions" \
    -d "{\"model\":\"$(get LOCALM_MODEL)\",\"messages\":[{\"role\":\"user\",\"content\":\"ready?\"}],\"max_tokens\":5}" >/dev/null && ok "Model loaded"

  if $tunnel; then
    say "Opening the tunnel"
    compose --profile tunnel up -d tunnel
    cmd_env "$(cmd_url)"
  else
    cmd_env
    say "Client on another machine? Re-run with --tunnel, or put both on one VPN (docs/local-stack.md)."
  fi
}

case "${1:-}" in
  up) shift; cmd_up "$@" ;;
  pull) cmd_pull ;;
  check) cmd_check ;;
  env) cmd_env "${2:-}" ;;
  url) cmd_url ;;
  logs) compose --profile tunnel logs -f --tail 100 ;;
  down) compose --profile tunnel down ;;
  *) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
