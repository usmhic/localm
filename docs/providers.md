# Provider setup

`localm` talks to inference servers through their OpenAI-compatible `/v1`
interface. One instance can use one legacy environment-configured server or
several named connections. The global `ALLOWED_MODELS` always remains the
outer exposure boundary.

## Docker networking

Inside a container, `127.0.0.1` means the `localm` container itself. The bundled
Compose file maps `host.docker.internal` to the Docker host, so a runtime on the
host is reachable there. If both services share a Compose network, use the
runtime service name instead.

## Ollama

Start a model:

```bash
ollama run qwen3:8b
```

Host binary:

```dotenv
LLM_PROVIDER=ollama
UPSTREAM_BASE_URL=http://127.0.0.1:11434/v1
ALLOWED_MODELS=qwen3:8b
```

Docker:

```dotenv
LLM_PROVIDER=ollama
UPSTREAM_BASE_URL=http://host.docker.internal:11434/v1
ALLOWED_MODELS=qwen3:8b
```

The older `OLLAMA_BASE_URL` setting is still accepted; `localm` adds `/v1`
when necessary.

## LM Studio

Load a model, start the local server from LM Studio’s Developer tab, and use:

```dotenv
LLM_PROVIDER=lmstudio
UPSTREAM_BASE_URL=http://host.docker.internal:1234/v1
ALLOWED_MODELS=your-loaded-model-id
```

If LM Studio authentication is enabled, also set `UPSTREAM_API_KEY`.

## llama.cpp

Start `llama-server` with an alias so clients get a friendly model name:

```bash
llama-server -m ./model.gguf --alias local-model --host 0.0.0.0 --port 8080 --jinja
```

Then configure:

```dotenv
LLM_PROVIDER=llamacpp
UPSTREAM_BASE_URL=http://host.docker.internal:8080/v1
ALLOWED_MODELS=local-model
```

`--jinja` is important when the selected model and chat template support tool
calling.

## LocalAI

After installing a model in LocalAI:

```dotenv
LLM_PROVIDER=localai
UPSTREAM_BASE_URL=http://host.docker.internal:8080/v1
ALLOWED_MODELS=qwen3-4b
```

Set `UPSTREAM_API_KEY` when LocalAI API-key authentication is enabled.

## Custom OpenAI-compatible server

Use `custom` for vLLM, OpenAI-compatible cloud services,
text-generation-inference adapters, remote private
endpoints, or another compatible implementation:

```dotenv
LLM_PROVIDER=custom
UPSTREAM_BASE_URL=https://inference.internal.example/v1
UPSTREAM_API_KEY=
ALLOWED_MODELS=my-model
```

The URL must be absolute, use `http` or `https`, and must not contain embedded
credentials.

## Multiple providers

Set `LOCALM_CONFIG` to a strict YAML file when several providers should share
one client-facing API. Connections are ordered by priority and filtered by
model and required capability. See [Configuration](configuration.md) and the
tracked `localm.example.yaml`.

Provider presets declare a compatible endpoint baseline. `/v1/capabilities` performs
lazy `GET /models` discovery and uses model-level `capabilities` or
`supported_features` metadata when a runtime provides it. For custom servers,
declare advanced features explicitly instead of assuming tools, structured
output, or reasoning support. The bundled provider presets include chat,
Responses, embeddings, and streaming; an older runtime can override that list.

Use separate LocalM deployments when providers belong to different security or
operational trust boundaries. Named connections share client keys, rate limits,
and concurrency controls within one process.

## Endpoint compatibility

LocalM forwards these native compatible operations:

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/embeddings`
- `/v1/models`

An upstream that lacks an endpoint should omit its capability. LocalM does not
translate Responses requests into chat requests, emulate embeddings, or remove
unsupported fields. Provider HTTP errors are normalized and their bodies are
not exposed to clients.

## Runtime documentation

- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [LM Studio API](https://lmstudio.ai/docs/developer/rest)
- [llama.cpp HTTP server](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
- [LocalAI quickstart](https://localai.io/basics/getting_started/)
