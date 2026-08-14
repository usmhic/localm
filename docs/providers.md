# Provider setup

`localm` talks to inference servers through their OpenAI-compatible `/v1`
interface. One `localm` instance targets one upstream server, while that server
may expose any number of models allowed by `ALLOWED_MODELS`.

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

Use `custom` for vLLM, text-generation-inference adapters, remote private
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

For hard isolation and predictable limits, run one `localm` instance per
provider and give each instance its own hostname, allowlist, and API keys. This
keeps routing explicit and lets each provider scale independently.

## Runtime documentation

- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [LM Studio API](https://lmstudio.ai/docs/developer/rest)
- [llama.cpp HTTP server](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
- [LocalAI quickstart](https://localai.io/basics/getting_started/)
