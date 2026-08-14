# Agents and tool calling

`localm` preserves OpenAI-compatible request and response fields instead of
reducing messages to plain text. This makes it usable with agent frameworks
that send `tools`, `tool_choice`, tool-call history, JSON schemas, or multimodal
message content.

The gateway never executes a tool. Your application remains responsible for
validating arguments, applying permissions, running the tool, and returning its
result to the model.

## Minimal tool loop

```python
import json
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=os.environ["LOCALM_API_KEY"],
)

tools = [{
    "type": "function",
    "function": {
        "name": "get_temperature",
        "description": "Get the temperature for a city",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
            "additionalProperties": False,
        },
    },
}]

messages = [{"role": "user", "content": "What is the temperature in Rabat?"}]
response = client.chat.completions.create(
    model="qwen3:8b",
    messages=messages,
    tools=tools,
)
message = response.choices[0].message
messages.append(message)

for call in message.tool_calls or []:
    arguments = json.loads(call.function.arguments)
    if call.function.name != "get_temperature":
        raise ValueError("tool is not allowed")

    # Replace this example value with your real, permission-checked tool.
    result = {"city": arguments["city"], "temperature_c": 24}
    messages.append({
        "role": "tool",
        "tool_call_id": call.id,
        "content": json.dumps(result),
    })

final = client.chat.completions.create(
    model="qwen3:8b",
    messages=messages,
    tools=tools,
)
print(final.choices[0].message.content)
```

## Runtime support

Tool quality depends on both the model and its chat template.

- Ollama supports single, parallel, multi-turn, and streaming tool calls for
  compatible models.
- LM Studio exposes tool use through its OpenAI-compatible chat endpoint.
- llama.cpp requires `--jinja` and a suitable tool-aware template/model.
- LocalAI supports OpenAI-style functions and tools across supported backends.

If plain chat works but tools do not, verify the model’s tool-use capability and
runtime configuration before changing the gateway.

## Safety checklist

- Treat model-generated arguments as untrusted input.
- Allowlist tools and validate arguments against a schema.
- Apply authorization again at tool-execution time.
- Use timeouts and output-size limits for every tool.
- Require confirmation for destructive or expensive actions.
- Never place upstream or client API keys inside prompts or tool results.
