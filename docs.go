package main

import "net/http"

func docsRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	http.Redirect(w, r, "/docs/", http.StatusTemporaryRedirect)
}

func swaggerDocs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/docs" {
		http.Redirect(w, r, "/docs/", http.StatusTemporaryRedirect)
		return
	}
	if r.URL.Path != "/docs/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline' https://cdn.jsdelivr.net; script-src 'unsafe-inline' https://cdn.jsdelivr.net; img-src data:; connect-src 'self'")
	_, _ = w.Write([]byte(swaggerHTML))
}

func openAPIDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(openAPISpec))
}

const swaggerHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>localm API documentation</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/openapi.json",
      dom_id: "#swagger-ui",
      deepLinking: true,
      displayRequestDuration: true,
      tryItOutEnabled: true
    });
  </script>
</body>
</html>`

const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "localm API",
    "version": "1.0.0",
    "description": "A secure OpenAI-compatible gateway for local LLM runtimes."
  },
  "servers": [{"url": "/", "description": "Current server"}],
  "paths": {
    "/healthz": {
      "get": {
        "summary": "Liveness check",
        "operationId": "healthCheck",
        "responses": {
          "200": {
            "description": "Service is running",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Status"}}}
          }
        }
      }
    },
    "/readyz": {
      "get": {
        "summary": "Readiness check",
        "description": "Checks whether the configured inference server is reachable.",
        "operationId": "readinessCheck",
        "responses": {
          "200": {
            "description": "Service is ready",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Status"}}}
          },
          "503": {"$ref": "#/components/responses/APIError"}
        }
      }
    },
    "/v1/models": {
      "get": {
        "summary": "List allowed models",
        "description": "Returns the models exposed by the gateway allowlist.",
        "operationId": "listModels",
        "security": [{"bearerAuth": []}],
        "responses": {
          "200": {
            "description": "Model list",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ModelList"}}}
          },
          "401": {"$ref": "#/components/responses/APIError"}
        }
      }
    },
    "/v1/chat/completions": {
      "post": {
        "summary": "Create a chat completion",
        "description": "Forwards an OpenAI-compatible request to the configured local inference server. Streaming responses use server-sent events; compatible extension fields pass through.",
        "operationId": "createChatCompletion",
        "security": [{"bearerAuth": []}],
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {"$ref": "#/components/schemas/ChatCompletionRequest"},
              "example": {
                "model": "qwen3:8b",
                "messages": [{"role": "user", "content": "Hello"}],
                "stream": false
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Completion response or an SSE stream when stream is true",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/ChatCompletionResponse"}},
              "text/event-stream": {"schema": {"type": "string"}}
            }
          },
          "400": {"$ref": "#/components/responses/APIError"},
          "401": {"$ref": "#/components/responses/APIError"},
          "429": {"$ref": "#/components/responses/APIError"},
          "502": {"$ref": "#/components/responses/APIError"},
          "503": {"$ref": "#/components/responses/APIError"}
        }
      }
    }
  },
  "components": {
    "securitySchemes": {
      "bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "API key"}
    },
    "responses": {
      "APIError": {
        "description": "Error response",
        "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
      }
    },
    "schemas": {
      "Status": {
        "type": "object",
        "required": ["status"],
        "properties": {"status": {"type": "string", "example": "ok"}}
      },
      "ModelList": {
        "type": "object",
        "required": ["object", "data"],
        "properties": {
          "object": {"type": "string", "example": "list"},
          "data": {
            "type": "array",
            "items": {
              "type": "object",
              "required": ["id", "object", "owned_by"],
              "properties": {
                "id": {"type": "string", "example": "qwen3:8b"},
                "object": {"type": "string", "example": "model"},
                "created": {"type": "integer", "format": "int64"},
                "owned_by": {"type": "string", "example": "ollama"}
              }
            }
          }
        }
      },
      "ChatMessage": {
        "type": "object",
        "required": ["role"],
        "properties": {
          "role": {"type": "string", "enum": ["system", "user", "assistant", "tool"]},
          "content": {
            "description": "Text or compatible multimodal content parts",
            "nullable": true,
            "oneOf": [{"type": "string"}, {"type": "array", "items": {"type": "object"}}]
          },
          "tool_call_id": {"type": "string"},
          "tool_calls": {"type": "array", "items": {"type": "object"}}
        }
      },
      "ChatCompletionRequest": {
        "type": "object",
        "required": ["model", "messages"],
        "properties": {
          "model": {"type": "string", "example": "qwen3:8b"},
          "messages": {"type": "array", "minItems": 1, "items": {"$ref": "#/components/schemas/ChatMessage"}},
          "stream": {"type": "boolean", "default": false},
          "max_tokens": {"type": "integer", "minimum": 1, "example": 512},
          "max_completion_tokens": {"type": "integer", "minimum": 1},
          "tools": {"type": "array", "items": {"type": "object"}},
          "tool_choice": {},
          "response_format": {"type": "object"},
          "user": {"type": "string"},
          "metadata": {"type": "object", "additionalProperties": true}
        }
      },
      "ChatCompletionResponse": {
        "type": "object",
        "required": ["id", "object", "created", "model", "choices"],
        "properties": {
          "id": {"type": "string"},
          "object": {"type": "string", "example": "chat.completion"},
          "created": {"type": "integer", "format": "int64"},
          "model": {"type": "string"},
          "choices": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "index": {"type": "integer"},
                "message": {"$ref": "#/components/schemas/ChatMessage"},
                "finish_reason": {"type": "string"}
              }
            }
          },
          "usage": {
            "type": "object",
            "additionalProperties": {"type": "integer"}
          }
        }
      },
      "ErrorEnvelope": {
        "type": "object",
        "required": ["error"],
        "properties": {
          "error": {
            "type": "object",
            "required": ["message", "type"],
            "properties": {
              "message": {"type": "string"},
              "type": {"type": "string"},
              "param": {"type": "string"},
              "code": {"type": "string"}
            }
          }
        }
      }
    }
  }
}`
