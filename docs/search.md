# Web research

LocalM can answer `POST /v1/search` from a self-hosted search engine, so local
models can be grounded in current web results without a cloud search API. It is
a retrieval primitive, like embeddings: LocalM searches, optionally reads the
top pages as plain text, and returns sources. It never prompts a model with
them, follows instructions found in a page, or keeps what it read.

Search is off until `SEARCH_BASE_URL` points at a [SearXNG](https://docs.searxng.org/)
instance with the JSON format enabled. The [local stack](local-stack.md) runs one
for you, private to its network.

## Request

```bash
curl http://localhost:8080/v1/search \
  -H "Authorization: Bearer ${LOCALM_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"queries":["bella cucina austin reviews","bella cucina austin"],"max_results":6,"read_pages":3,"language":"en"}'
```

| Field | Default | Meaning |
| --- | --- | --- |
| `query` / `queries` | required | One query, or up to five; duplicates collapse |
| `max_results` | `SEARCH_MAX_RESULTS` | Results to return, at most `SEARCH_MAX_RESULTS` |
| `read_pages` | `2` | How many of the first results to read in full, at most `SEARCH_MAX_READ_PAGES` |
| `language` | engine default | A language tag such as `fr` or `es-MX` |

Unknown fields are rejected, so typos fail loudly.

## Response

```json
{
  "object": "search.result",
  "queries": ["bella cucina austin reviews", "bella cucina austin"],
  "data": [
    {
      "index": 1,
      "title": "Bella Cucina - Austin - Yelp",
      "url": "https://www.yelp.com/biz/bella-cucina-austin",
      "snippet": "Great pasta, friendly team…",
      "engine": "bing",
      "published_at": "2026-10-01",
      "content": "Bella Cucina\nLovely team. A little wait on Saturday…",
      "content_truncated": true
    }
  ]
}
```

Results from every query are merged round-robin, so each query contributes its
best hits, and the same page found twice (with or without `www.`, a query string
or a fragment) appears once. `content` is the page's readable text, without
scripts, styles, navigation or markup, or `null` when the page wasn't read, isn't
text, or couldn't be reached. Keep `index` and `url` with the text when you put
results in a prompt, so the model can cite what it was shown.

One failing query is tolerated. When every query fails, the response is `502`
with code `search_upstream_error`. Without `SEARCH_BASE_URL`, `/v1/search`
answers `404` with code `search_not_configured`.

## Safety

Reading a page means fetching a URL that came from the web, so LocalM treats
every one as hostile:

- Pages are fetched only over HTTP(S), on ports 80 and 443, without credentials
  in the URL.
- The address is checked **when connecting**, after DNS resolution: loopback,
  private, link-local, carrier-grade NAT, multicast, documentation and NAT64
  ranges are refused, so DNS rebinding can't reach the private network either.
- Every redirect is re-checked; at most four are followed.
- Only `text/html` and `text/plain` are read, up to `SEARCH_PAGE_MAX_BYTES` and
  `SEARCH_PAGE_TIMEOUT`; text is cut at `SEARCH_PAGE_MAX_CHARS`.
- The environment's HTTP proxy is not used for page reads.
- Queries, URLs and page content are never logged or stored.

Searches count against the same per-key and per-IP rate limits as inference and
have their own concurrency limit, `MAX_CONCURRENT_SEARCH`.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `SEARCH_BASE_URL` | empty (off) | The SearXNG root URL, e.g. `http://searxng:8080` |
| `SEARCH_PROVIDER` | `searxng` | The search engine type (only `searxng` today) |
| `SEARCH_API_KEY` | empty | Bearer key sent to the search engine, if it requires one |
| `SEARCH_MAX_RESULTS` | `8` | Results per request, 1–20 |
| `SEARCH_MAX_READ_PAGES` | `3` | Pages a request may read, 0–8 |
| `SEARCH_TIMEOUT` | `12s` | Per-query search engine timeout |
| `SEARCH_PAGE_TIMEOUT` | `8s` | Per-page read timeout |
| `SEARCH_PAGE_MAX_BYTES` | `1500000` | Largest page body read |
| `SEARCH_PAGE_MAX_CHARS` | `6000` | Characters of text returned per page |
| `SEARCH_SAFESEARCH` | `1` | SearXNG safe search: 0, 1 or 2 |
| `SEARCH_USER_AGENT` | `localm-research/1.0 (+https://github.com/usmhic/localm)` | User agent for page reads |
| `MAX_CONCURRENT_SEARCH` | `4` | Simultaneous searches per replica |

SearXNG must allow the JSON format (`search.formats: [html, json]` in its
`settings.yml`); otherwise it answers 403 and LocalM reports
`search_upstream_error`. Keep it on a private network, as the stack does: LocalM
is the authenticated door.
