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
  <script>SwaggerUIBundle({url:"/openapi.json",dom_id:"#swagger-ui",deepLinking:true,displayRequestDuration:true,tryItOutEnabled:true});</script>
</body>
</html>`

const openAPISpec = `{
  "openapi": "3.0.3",
  "info": {
    "title": "localm API",
    "version": "1.1.0",
    "description": "A secure OpenAI-compatible gateway for local, self-hosted, and explicitly configured remote LLM providers. Unknown compatible request and successful response fields pass through."
  },
  "servers": [{"url": "/", "description": "Current server"}],
  "paths": {
    "/healthz": {"get": {"summary": "Process liveness", "responses": {"200": {"description": "Running"}}}},
    "/readyz": {"get": {"summary": "Connection readiness", "description": "Ready when at least one configured connection answers model discovery.", "responses": {"200": {"description": "At least one connection is ready"}, "503": {"$ref": "#/components/responses/APIError"}}}},
    "/v1/models": {"get": {"summary": "List allowed models", "security": [{"bearerAuth": []}], "responses": {"200": {"description": "Configured model allowlist"}, "401": {"$ref": "#/components/responses/APIError"}}}},
    "/v1/capabilities": {"get": {"summary": "Discover connection and model capabilities", "description": "Returns sanitized connection health, priority, trust, model availability, and effective capability profiles. It never returns base URLs or credentials.", "security": [{"bearerAuth": []}], "responses": {"200": {"description": "Capability list"}, "401": {"$ref": "#/components/responses/APIError"}}}},
    "/v1/chat/completions": {"post": {"summary": "Create a chat completion", "description": "Forwards compatible chat requests, including SSE streams, tool calls, structured output, and reasoning fields when declared supported.", "security": [{"bearerAuth": []}], "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ChatRequest"}}}}, "responses": {"200": {"description": "Compatible completion or SSE stream"}, "400": {"$ref": "#/components/responses/APIError"}, "401": {"$ref": "#/components/responses/APIError"}, "429": {"$ref": "#/components/responses/APIError"}, "502": {"$ref": "#/components/responses/APIError"}}}},
    "/v1/responses": {"post": {"summary": "Create a response", "description": "Forwards an OpenAI-compatible Responses API request to a capable connection.", "security": [{"bearerAuth": []}], "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ResponseRequest"}}}}, "responses": {"200": {"description": "Compatible response or SSE stream"}, "400": {"$ref": "#/components/responses/APIError"}, "401": {"$ref": "#/components/responses/APIError"}, "429": {"$ref": "#/components/responses/APIError"}, "502": {"$ref": "#/components/responses/APIError"}}}},
    "/v1/embeddings": {"post": {"summary": "Create embeddings", "description": "Forwards an OpenAI-compatible embeddings request to a capable connection.", "security": [{"bearerAuth": []}], "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EmbeddingRequest"}}}}, "responses": {"200": {"description": "Compatible embedding response"}, "400": {"$ref": "#/components/responses/APIError"}, "401": {"$ref": "#/components/responses/APIError"}, "429": {"$ref": "#/components/responses/APIError"}, "502": {"$ref": "#/components/responses/APIError"}}}}
  },
  "components": {
    "securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "API key"}},
    "responses": {"APIError": {"description": "OpenAI-compatible normalized error", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}}},
    "schemas": {
      "ChatRequest": {"type": "object", "required": ["model", "messages"], "additionalProperties": true, "properties": {"model": {"type": "string"}, "messages": {"type": "array", "minItems": 1, "items": {"type": "object"}}, "stream": {"type": "boolean"}, "max_tokens": {"type": "integer", "minimum": 1}, "max_completion_tokens": {"type": "integer", "minimum": 1}}},
      "ResponseRequest": {"type": "object", "required": ["model", "input"], "additionalProperties": true, "properties": {"model": {"type": "string"}, "input": {}, "stream": {"type": "boolean"}, "max_output_tokens": {"type": "integer", "minimum": 1}}},
      "EmbeddingRequest": {"type": "object", "required": ["model", "input"], "additionalProperties": true, "properties": {"model": {"type": "string"}, "input": {}, "encoding_format": {"type": "string"}, "dimensions": {"type": "integer", "minimum": 1}}},
      "ErrorEnvelope": {"type": "object", "required": ["error"], "properties": {"error": {"type": "object", "required": ["message", "type"], "properties": {"message": {"type": "string"}, "type": {"type": "string"}, "param": {"type": "string"}, "code": {"type": "string"}}}}}
    }
  }
}`
