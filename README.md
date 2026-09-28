# ReadGate — AI-safe DB Gateway

```
AI ──MCP──▶ Gateway ──SSH tunnel──▶ Server ──localhost──▶ PostgreSQL
```

The AI only talks to the gateway. The gateway owns SSH + credentials.
A source enables **only after the gateway proves the identity is read-only**.

## Fleet model (what the AI sees)

```
Production (cluster)
  PostgreSQL · SSH → server-1 · DB connect_local · user ai_readonly
Analytics (cluster)
  PostgreSQL · SSH → analytics-1 · DB analytics
```

MCP tools: `fleet_overview · list_sources · list_clusters · schema(source) · query(source, sql) · explain(source, sql) · sample(source, table) · doctor(source)`

## Onboarding

1. Add database → 2. SSH → 3. Detect PG → 4. Verify admin (temp, never stored)
5. CREATE ROLE ai_readonly … → 6. Verify SELECT ✓ / write denied ✓ / ro=on ✓
7. Save → 8. MCP enabled

Mode B (manual): gateway generates the SQL script, you run it, gateway only receives the ai user + password.

## Run

```bash
cd ReadGate
go mod tidy
wails dev            # desktop + hot reload
# frontend only:
cd frontend && npm install && npm run dev
```

MCP endpoint: `ReadGate.exe mcp` speaks **stdio** (goals parity, protocol 2024-11-05) —
no window, no port. opencode launches it as a local server:

```json
{ "mcp": { "readgate": {
  "type": "local",
  "command": ["E:\\laragon\\www\\go\\ReadGate\\build\\bin\\ReadGate.exe", "mcp"],
  "enabled": true
} } }
```

Copy it from the app: **MCP tab → opencode · local → Copy config**, then restart
the opencode session. A legacy HTTP endpoint (`http://127.0.0.1:9413/mcp`,
`GET /mcp/tools`, `GET /api/fleet`) is kept for Claude/Cursor-style clients —
see the other tabs. The in-app **Test MCP** button runs the exact client
handshake (stdio + http) and shows every leg.

## Security notes

- `internal/guard` rejects anything but single-statement SELECT/WITH/EXPLAIN/SHOW.
- Every session sets `default_transaction_read_only=on` + `statement_timeout=15s` + row cap 200–500.
- Store: `~/.readgate/readgate.db` — SQLite (WAL, pure-Go `modernc.org/sqlite`, no CGO).
  Password columns are AES-GCM encrypted with `~/.readgate/key.bin` (0600 each).
  A legacy `store.json` is imported once, then archived to `store.json.bak`.
- SSH: key file or agent; PG stays on `127.0.0.1:5432` remotely; `known_hosts` pinning is TODO for 0.2.
