# copilot-proxy

A minimal-dependency HTTP proxy that sits between a local coding agent and the
**GitHub Copilot** API. It handles Copilot authentication for you, so your agent
only needs to speak the OpenAI-compatible HTTP API and point at this proxy — no
knowledge of GitHub's device flow or token exchange required.

- **Zero external dependencies** — pure Go standard library.
- **Handles auth** — GitHub OAuth device-flow login, plus automatic exchange and
  refresh of the short-lived Copilot token.
- **Transparent proxy** — forwards requests to `https://api.githubcopilot.com`,
  injecting the required `Authorization` and editor headers.
- **Streaming-friendly** — server-sent event (SSE) responses stream through
  token by token.

> Note: This talks to GitHub Copilot's private editor API. Use it with your own
> Copilot subscription and in accordance with GitHub's terms of service.

## Requirements

- Go 1.23+
- An active GitHub Copilot subscription

## Build

```sh
go build -o copilot-proxy .
```

This produces a single self-contained binary (`copilot-proxy`, or
`copilot-proxy.exe` on Windows).

## Usage

### 1. Log in

```sh
copilot-proxy login
```

This starts the GitHub device flow: open the printed URL, enter the code, and
authorize. The resulting GitHub token is saved to your user config directory
(`%AppData%\copilot-proxy\config.json` on Windows,
`~/.config/copilot-proxy/config.json` on Linux/macOS) with owner-only
permissions.

### 2. Start the proxy

```sh
copilot-proxy                 # listens on 127.0.0.1:8080 by default
copilot-proxy --port 9000 -v  # custom port, verbose request logging
```

### 3. Point your agent at it

Configure your coding agent's OpenAI-compatible base URL:

```
http://127.0.0.1:8080        # Copilot-native paths, e.g. /chat/completions
http://127.0.0.1:8080/v1     # OpenAI-style /v1/... paths also work
```

Any dummy API key is fine — the proxy replaces it with a real Copilot token.

Example request:

```sh
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer unused" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

List available models:

```sh
curl http://127.0.0.1:8080/models
```

## Commands

| Command                | Description                                       |
| ---------------------- | ------------------------------------------------- |
| `copilot-proxy`        | Start the proxy (alias for `serve`)               |
| `copilot-proxy serve`  | Start the proxy                                   |
| `copilot-proxy login`  | Authenticate with GitHub via the device flow      |
| `copilot-proxy logout` | Remove the stored GitHub token                    |
| `copilot-proxy status` | Show authentication status and test Copilot access|

### Serve flags

| Flag             | Default     | Description                    |
| ---------------- | ----------- | ------------------------------ |
| `-p`, `--port`   | `8080`      | Port to listen on             |
| `--host`         | `127.0.0.1` | Interface to bind             |
| `-v`, `--verbose`| `false`     | Also log response status and duration per request |

## Logging

Every incoming request is logged to **stdout**, one line per request:

```
2026/07/08 15:44:02 127.0.0.1 POST /v1/chat/completions
```

With `-v`/`--verbose`, a completion line with the upstream status and duration
is added after each request:

```
2026/07/08 15:44:05 POST /chat/completions -> 200 (2.13s)
```

Proxy and token errors are written to **stderr**, so you can redirect access
logs and error logs independently.

## Configuration via environment

Set `COPILOT_PROXY_GITHUB_TOKEN` to supply a GitHub OAuth token directly and
bypass the on-disk config (useful for headless/CI environments):

```sh
COPILOT_PROXY_GITHUB_TOKEN=gho_xxx copilot-proxy
```

## How it works

1. **Login** — OAuth device flow against `github.com` using the public Copilot
   client id, yielding a long-lived GitHub OAuth token (stored on disk).
2. **Token exchange** — the GitHub token is exchanged at
   `api.github.com/copilot_internal/v2/token` for a short-lived Copilot token,
   which the proxy caches and refreshes automatically before it expires.
3. **Forwarding** — each incoming request is proxied to
   `api.githubcopilot.com` with `Authorization: Bearer <copilot-token>` plus the
   editor/integration headers Copilot expects. A leading `/v1` path segment is
   stripped so OpenAI-style clients work unchanged.

## Endpoints

| Path               | Behavior                                             |
| ------------------ | ---------------------------------------------------- |
| `/` or `/healthz`  | Local status/health JSON (not forwarded)             |
| everything else    | Proxied to the Copilot API                           |
