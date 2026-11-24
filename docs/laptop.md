# Serve from a laptop

Use the GPU in your laptop or workstation from a server somewhere else: Ollama
runs the models on your machine, LocalM puts a key and limits in front of them,
and a Cloudflare tunnel gives the server an HTTPS address to call. Your machine
only does inference; whatever calls it (an agent, an app) does everything else.

```text
your server ──HTTPS──> tunnel ──> LocalM :8080 ──> Ollama :11434 ──> GPU
             (Bearer key)          (this machine only)
```

## 1. Ollama

Install it from <https://ollama.com/download> (macOS, Windows, Linux). It runs
natively so it can use the GPU, including Apple silicon, which Docker can't.

Agents send long prompts, so give the models a bigger context than Ollama's
default and keep them loaded:

```bash
# macOS / Linux, in the terminal you start Ollama from
export OLLAMA_CONTEXT_LENGTH=16384   # tokens per request (the default 4096 cuts agent prompts)
export OLLAMA_NUM_PARALLEL=2         # requests served at once
export OLLAMA_KEEP_ALIVE=30m         # keep models in memory between requests
export OLLAMA_MAX_LOADED_MODELS=3    # chat, light and embedding models together
ollama serve
```

On Windows, set the same names under *Settings → System → About → Advanced
system settings → Environment variables*, then restart Ollama from the tray. On
Linux with the systemd service, put them in `sudo systemctl edit ollama` as
`Environment="OLLAMA_CONTEXT_LENGTH=16384"` lines, then
`sudo systemctl restart ollama`. On macOS with the app, use
`launchctl setenv OLLAMA_CONTEXT_LENGTH 16384` (and the others), then restart it.

Leave Ollama on `127.0.0.1`: LocalM is the only way in.

## 2. Models

Pick by GPU memory (on a Mac, unified memory). Agents need a model that calls
tools and returns JSON reliably; the Qwen 3 family does both.

| Your machine | Main model | Light model |
| --- | --- | --- |
| 8–12 GB GPU, or a 16 GB Mac | `qwen3:8b` | `qwen3:4b` |
| 16–24 GB GPU, or a 32 GB Mac | `qwen3:14b` | `qwen3:4b` |
| 40 GB+ GPU, or a 64 GB Mac | `qwen3:32b` | `qwen3:8b` |

Add an embedding model if your client uses embeddings: `nomic-embed-text`
(768 dimensions) or `all-minilm` (384). Use whichever your client expects.

```bash
ollama pull qwen3:14b
ollama pull qwen3:4b
ollama pull all-minilm
```

## 3. LocalM

Needs Docker. From this repository:

```bash
scripts/serve.sh up          # key created, LocalM on 127.0.0.1:8080, every pulled model allowed
scripts/serve.sh check       # refuses without the key, answers, calls tools
```

`up` writes `.env` (git-ignored, owner-only) with a new `API_KEYS` value, the
models Ollama has as `ALLOWED_MODELS` (or `--models a,b,c`), and limits sized
for agents: 8192-token answers, 8 MB requests, 10 minutes per answer. Edit
`.env` and run `up` again to change any of them.

## 4. Reach it from your server

```bash
scripts/serve.sh up --tunnel   # also starts a Cloudflare tunnel
scripts/serve.sh env           # prints the HTTPS base URL and the key
```

Give your client the printed base URL (ending in `/v1`) and the key, like any
OpenAI-compatible API.

Without an account the tunnel gets a random `trycloudflare.com` address that
changes each time it starts. For a fixed one, create a tunnel in the Cloudflare
dashboard, route its hostname to `http://bridge:8080` (on Linux
`http://127.0.0.1:8080`), put its token in `.env` as `CLOUDFLARE_TUNNEL_TOKEN`,
and run `up --tunnel` again. A VPN between the two machines (Tailscale,
WireGuard) works too: then bind LocalM to the VPN address with
`LOCALM_BIND_ADDRESS` and skip the tunnel.

## Day to day

```bash
scripts/serve.sh logs    # follow LocalM and the tunnel
scripts/serve.sh down    # stop both (Ollama keeps running)
```

When the laptop sleeps or goes offline, calls fail until it is back; clients
should treat it as one model source among others and fall back elsewhere.

## Without Docker

Build and run the binary next to Ollama:

```bash
go build -o localm .
API_KEYS=$(openssl rand -hex 32) ALLOWED_MODELS=qwen3:14b,qwen3:4b \
  MAX_TOKENS=8192 UPSTREAM_TIMEOUT=10m SERVER_WRITE_TIMEOUT=11m \
  LISTEN_ADDR=127.0.0.1:8080 ./localm
cloudflared tunnel --url http://127.0.0.1:8080   # optional
```
