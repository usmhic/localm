#!/usr/bin/env bash
# Serve this machine's Ollama models through LocalM, optionally over a Cloudflare tunnel.
# Ollama runs natively (ollama.com/download) so it gets the GPU. See docs/laptop.md.
#
#   scripts/serve.sh up [--models m1,m2,...] [--tunnel]   start LocalM (and the tunnel)
#   scripts/serve.sh env                                  print the URL and key for your client
#   scripts/serve.sh check                                test auth, chat, tools and embeddings
#   scripts/serve.sh logs | down
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/.env"
OLLAMA="${OLLAMA_URL:-http://127.0.0.1:11434}"

say() { printf '\033[1m›\033[0m %s\n' "$*"; }
ok() { printf '\033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m %s\n' "$*"; }
fail() { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

get() { [ -f "$ENV_FILE" ] && grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true; }
put() {
  touch "$ENV_FILE"; chmod 600 "$ENV_FILE"
  local tmp; tmp="$(mktemp)"; grep -vE "^$1=" "$ENV_FILE" > "$tmp" || true; mv "$tmp" "$ENV_FILE"; chmod 600 "$ENV_FILE"
  printf '%s=%s\n' "$1" "$2" >> "$ENV_FILE"
}
secret() { openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

compose() {
  local files=(-f "$ROOT/docker-compose.yml")
  [ "$(uname -s)" = "Linux" ] && files+=(-f "$ROOT/docker-compose.linux.yml")
  docker compose --project-directory "$ROOT" "${files[@]}" "$@"
}

local_url() { echo "http://127.0.0.1:$(get LOCALM_PORT | grep . || echo 8080)"; }
key() { get API_KEYS | cut -d, -f1; }

# The models Ollama has, as a comma-separated list.
ollama_models() {
  curl -fsS "$OLLAMA/api/tags" | python3 -c 'import json,sys; print(",".join(m["name"] for m in json.load(sys.stdin)["models"]))'
}

tunnel_url() {
  if [ -n "$(get CLOUDFLARE_TUNNEL_TOKEN)" ]; then echo "(your named tunnel's hostname)"; return; fi
  local url=""
  for _ in $(seq 1 30); do
    url="$(compose --profile tunnel logs tunnel 2>/dev/null | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true)"
    [ -n "$url" ] && break
    sleep 2
  done
  [ -n "$url" ] || fail "No tunnel address yet: scripts/serve.sh logs"
  echo "$url"
}

cmd_env() {
  local base
  if compose --profile tunnel ps --status running tunnel 2>/dev/null | grep -q tunnel; then base="$(tunnel_url)"; else base="$(local_url)"; fi
  cat <<EOF

Point your OpenAI-compatible client at:

  Base URL  $base/v1
  API key   $(key)
  Models    $(get ALLOWED_MODELS)

EOF
}

cmd_up() {
  local models="" tunnel=false
  while [ $# -gt 0 ]; do
    case "$1" in
      --models) models="$2"; shift 2 ;;
      --tunnel) tunnel=true; shift ;;
      *) fail "Unknown option $1" ;;
    esac
  done
  command -v docker >/dev/null || fail "Docker is required (or run the binary: docs/laptop.md)."
  curl -fsS "$OLLAMA/api/tags" >/dev/null 2>&1 || fail "Ollama isn't answering on $OLLAMA. Install it (ollama.com/download) and run: ollama serve"

  [ -n "$(key)" ] || { put API_KEYS "$(secret)"; ok "Created an API key"; }
  if [ -n "$models" ]; then put ALLOWED_MODELS "$models"; fi
  if [ -z "$(get ALLOWED_MODELS)" ]; then
    local found; found="$(ollama_models)"
    [ -n "$found" ] || fail "Ollama has no models yet. Pull one first, e.g.: ollama pull qwen3:8b"
    put ALLOWED_MODELS "$found"
  fi
  # Room for agents: long prompts, long answers and slow first tokens. Edit .env to change them.
  [ -n "$(get MAX_TOKENS)" ] || put MAX_TOKENS 8192
  [ -n "$(get DEFAULT_MAX_TOKENS)" ] || put DEFAULT_MAX_TOKENS 2048
  [ -n "$(get MAX_BODY_BYTES)" ] || put MAX_BODY_BYTES 8388608
  [ -n "$(get UPSTREAM_TIMEOUT)" ] || put UPSTREAM_TIMEOUT 10m
  [ -n "$(get SERVER_WRITE_TIMEOUT)" ] || put SERVER_WRITE_TIMEOUT 11m
  if [ -n "$(get CLOUDFLARE_TUNNEL_TOKEN)" ]; then put CLOUDFLARE_TUNNEL_COMMAND "tunnel --no-autoupdate run"; fi

  say "Starting LocalM"
  compose up -d --build bridge
  for _ in $(seq 1 60); do curl -fsS "$(local_url)/healthz" >/dev/null 2>&1 && break; sleep 1; done
  curl -fsS "$(local_url)/healthz" >/dev/null 2>&1 || fail "LocalM didn't start: scripts/serve.sh logs"
  ok "LocalM on $(local_url), serving $(get ALLOWED_MODELS)"

  if $tunnel; then
    say "Opening the tunnel"
    compose --profile tunnel up -d tunnel
  fi
  cmd_env
}

cmd_check() {
  local base; base="$(local_url)"
  local model; model="$(get ALLOWED_MODELS | cut -d, -f1)"
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$base/v1/models")" = "401" ] && ok "Refuses requests without the key" || fail "LocalM answered without a key."
  curl -fsS -H "Authorization: Bearer $(key)" -H 'content-type: application/json' "$base/v1/chat/completions" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with one word: ready\"}],\"max_tokens\":200}" \
    | grep -q '"content"' && ok "$model answers" || fail "$model didn't answer."
  curl -fsS -H "Authorization: Bearer $(key)" -H 'content-type: application/json' "$base/v1/chat/completions" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"What is the temperature in Rabat?\"}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"get_temperature\",\"parameters\":{\"type\":\"object\",\"properties\":{\"city\":{\"type\":\"string\"}},\"required\":[\"city\"]}}}]}" \
    | grep -q 'tool_calls' && ok "$model calls tools" || warn "$model answered without calling the tool: agents need a model with tool support."
  ok "All set."
}

case "${1:-}" in
  up) shift; cmd_up "$@" ;;
  env) cmd_env ;;
  check) cmd_check ;;
  logs) compose --profile tunnel logs -f --tail 100 ;;
  down) compose --profile tunnel down ;;
  *) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
