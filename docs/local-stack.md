# Local stack

`deploy/stack` runs a complete local AI service on one machine: models,
embeddings and web research, behind LocalM as the only open port. An optional
Cloudflare tunnel gives it an HTTPS address, so an app hosted elsewhere can use
your GPU without opening a port on your router.

```text
 your app ──(HTTPS + key)──► tunnel ──► LocalM :8080 ─┬─► Ollama    /v1/*        models + embeddings
 (anywhere)                             (this machine) └─► SearXNG   /v1/search   web research
```

- **Ollama** serves the models. It has no published port.
- **SearXNG** is a private search engine for [`/v1/search`](search.md). No
  published port either.
- **LocalM** checks the key, limits and queues requests, routes them, and reads
  search results. Without a key you get `401`.
- **The tunnel** (Cloudflare, optional) puts LocalM on an HTTPS address.

## Start in three commands

You need Docker. An NVIDIA GPU is used automatically on Linux (with the NVIDIA
Container Toolkit). On a Mac, see [Apple silicon](#apple-silicon).

```bash
scripts/stack.sh up --size medium --tunnel   # start, pull models, open a tunnel
scripts/stack.sh check                       # auth, chat, JSON, tools, embeddings, search
scripts/stack.sh env                         # the settings for your client
```

`up` creates an API key and a SearXNG secret in `deploy/stack/.env` (git-ignored,
readable only by you), builds LocalM from this checkout, pulls the models and
warms the main one up.

## Pick models

| `--size` | Main model | Light model | GPU memory |
| --- | --- | --- | --- |
| `small` | `qwen3:8b` | `qwen3:4b` | 8–12 GB, or a 16 GB Mac |
| `medium` | `qwen3:14b` | `qwen3:4b` | 16–24 GB, or a 32 GB Mac |
| `large` | `qwen3:32b` | `qwen3:8b` | 40 GB+, or a 64 GB Mac |

The embedding model defaults to `nomic-embed-text`. Choose your own with
`--model`, `--light-model` and `--embedding-model`; `up` adds them to LocalM's
allowlist. Agent frameworks need a main model with **tool calling** and **JSON
schema output**; `check` tests both.

## Behaviour under load

| Setting (in `deploy/stack/.env`) | Default | What it does |
| --- | --- | --- |
| `OLLAMA_NUM_PARALLEL` | `2` | Requests the GPU runs at once; LocalM's `MAX_CONCURRENT_LLM` follows it |
| `MAX_QUEUED_LLM` | `6` | Requests that wait for a free slot instead of getting a `429` |
| `QUEUE_TIMEOUT` | `60s` | How long a request may wait in that line |
| `UPSTREAM_TIMEOUT` | `10m` | How long one answer may take |
| `MAX_TOKENS` / `DEFAULT_MAX_TOKENS` | `8192` / `2048` | Output limits for long answers |

When the line is full, LocalM answers `429` at once, so a client with a cloud
backup can send the request there instead of waiting. `GET /v1/capabilities`
shows the current load, each connection's health, and whether search is on.

## Reaching it from elsewhere

- **Cloudflare quick tunnel** (`--tunnel`): instant, no account. The address
  changes on every restart, so run `scripts/stack.sh env` again after one.
- **Cloudflare named tunnel**: a stable hostname. Create a tunnel in the
  Cloudflare dashboard, route it to `http://localm:8080`, put its token in
  `CLOUDFLARE_TUNNEL_TOKEN` in `deploy/stack/.env`, then run `up --tunnel`.
- **Tailscale or WireGuard**: put both machines on one private network, set
  `LOCALM_BIND_ADDRESS=0.0.0.0` in `deploy/stack/.env`, and use
  `http://<machine>:8080/v1`.
- **Same machine**: `http://127.0.0.1:8080/v1`.

## Apple silicon

Docker can't use a Mac's GPU, so run Ollama natively and keep the rest in Docker:

```bash
brew install ollama && ollama serve   # or the app from ollama.com
scripts/stack.sh up --native-ollama --size medium --tunnel
```

## Day to day

```bash
scripts/stack.sh pull    # update the models
scripts/stack.sh logs    # everything, live
scripts/stack.sh url     # the tunnel's current address
scripts/stack.sh down    # stop (models stay downloaded)
```

After changing models, run `up` again so LocalM's allowlist follows. Anyone with
the key and the address can use your models, so treat the key like a password.
To rotate it, delete the `LOCALM_API_KEYS` line from `deploy/stack/.env`, run
`up` again and update your clients.
