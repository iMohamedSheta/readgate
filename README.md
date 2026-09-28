# ReadGate

[![CI](https://github.com/iMohamedSheta/ReadGate/actions/workflows/ci.yml/badge.svg)](https://github.com/iMohamedSheta/ReadGate/actions/workflows/ci.yml)
[![Release](https://github.com/iMohamedSheta/ReadGate/actions/workflows/release.yml/badge.svg)](https://github.com/iMohamedSheta/ReadGate/releases/latest)

One binary, no installer. Your database fleet, AI-readable —
credentials never leave this box.

```
AI ──MCP──▶ Gateway ──SSH tunnel──▶ Server ──localhost──▶ Database
```

A source enables **only after the gateway proves the identity is read-only**
(connectivity ✓, authentication ✓, read access ✓, write denied ✓).

![ReadGate app screenshot](docs/screenshot.png)
_Screenshot uses synthetic demo data (2 clusters, 3 local SQLite sources) — never a real database._

## Download

Open the [**latest release**](https://github.com/iMohamedSheta/ReadGate/releases/latest)
and pick your OS. Per-OS screenshots (`screenshot-windows/macos/linux.png`) ship next
to the binaries — all seeded demo data.

| OS | File | Run |
|---|---|---|
| Windows 10/11 (x64) | `ReadGate-Windows-amd64.exe` (`ReadGate.exe` is the same file) | Double-click. If SmartScreen warns (unsigned): **More info → Run anyway** |
| macOS (Universal: Intel + Apple Silicon) | `ReadGate-macOS-universal.zip` | Unzip, drag `ReadGate.app` to Applications, right-click → Open on first launch (unsigned) |
| Linux (x64) | `ReadGate-Linux-amd64.tar.gz` | `tar xzf … && ./ReadGate` (needs WebKitGTK on minimal distros: `libwebkit2gtk-4.1`) |

Data lives in `~/.readgate/readgate.db` on every OS (`%USERPROFILE%\.readgate\readgate.db`
on Windows). Override the folder with `READGATE_HOME` (also used to run an isolated copy).

`ReadGate --version` prints the embedded release tag (`dev` for local builds;
Settings → AI access shows it too). `ReadGate mcp` runs the MCP server on stdio.

## Contents

- [Engines](#engines)
- [Onboarding](#onboarding)
- [Browse, Query, Doctor](#browse-query-doctor)
- [Table editing (app writes)](#table-editing-app-writes)
- [MCP — let the AI read your fleet](#mcp--let-the-ai-read-your-fleet)
- [Data, troubleshooting](#data-troubleshooting)
- [Build from source](#build-from-source)
- [Releasing](#releasing)
- [Project layout](#project-layout)

## Engines

Group sources into **clusters** (Production, Analytics, Billing…) so the agent
reasons about the right database. Same workflow for every engine; only the
login details differ.

| Engine | Status | Notes |
|---|---|---|
| PostgreSQL | ✅ Ready | Full proof + auto-provision of `ai_readonly` |
| MySQL / MariaDB / TiDB | ✅ Ready | `CREATE USER` + `SELECT, SHOW VIEW` grants, `SHOW GRANTS` audit |
| SQLite | ✅ Ready | Local `.db` file — no server, no login, always opened read-only |
| Turso | ✅ Ready | `libsql://` URL + token; gateway-enforced read-only (tokens are full-access) |
| SQL Server | ✅ Ready | `db_datareader`-only proof, `TOP`/`OFFSET-FETCH` paging, `SHOWPLAN_ALL` |
| CockroachDB / Redshift | 🧪 Beta | Postgres wire — best effort |
| ClickHouse | ⏳ Soon | Needs driver + dialect |
| Oracle | 📋 Planned | Needs Oracle client libraries |

Connection modes: **SSH tunnel** (Postgres/MySQL/SQL Server families),
**direct** host:port, **file** (SQLite), **URL + token** (Turso).

## Onboarding

Add Database → pick engine → connect → temporary admin credentials
(**never stored**) → gateway creates the dedicated read-only identity →
proves `SELECT ✓ / write denied ✓ / read-only ✓` → source enables → live on MCP.

Manual mode: the gateway generates the SQL script, you run it as admin, and
only the read-only username + password come back.

## Browse, Query, Doctor

- **Browse** — Beekeeper-style table list with row estimates, filter builder
  (`contains, =, ≠, >, starts with, IN, is null…`) + raw `WHERE` mode, paging,
  CSV export, per-column filters, sortable columns, click-a-row detail view.
- **Query** — guarded editor (`SELECT / WITH / EXPLAIN / SHOW` only, no stacked
  statements, auto-`LIMIT`, 500-row cap, 15s timeout) with tabs + history, runs
  as the read-only identity in a `READ ONLY` transaction.
- **Doctor** — the same health probes the AI uses: seq scans, missing PKs,
  connection pressure, long queries, biggest tables.

## Table editing (app writes)

The app is read-only by default. Flip **Settings → General → Allow
modifications**, and Browse grows an **Edit rows** mode: edit cells, add rows,
delete rows, then **Apply** — every batch shows its exact SQL and asks first.

- Row DML only (`INSERT / UPDATE / DELETE`) — DDL is blocked, full-table
  `UPDATE`/`DELETE` needs explicit confirmation.
- Server databases need a saved **write login** (`postgres`, `root`, `sa`…),
  AES-GCM encrypted in SQLite, used only for live edit connections.
- SQLite needs no login; Turso reuses its token.
- **The AI stays read-only**: no MCP tool can write, ever.

## MCP — let the AI read your fleet

The same binary is an **MCP server** (protocol 2024-11-05): `ReadGate mcp`
speaks stdio, so opencode launches it directly — no window, no port. The AI
sees **names only** (clusters, sources, tables, columns) — never hosts, ports,
users, or passwords. The MCP tab has per-client setup guides (opencode first),
a 14-tool reference with examples, the recommended workflow, and a self-test.

| Tool | What it does |
|---|---|---|
| `fleet_overview` | Clusters + source names — the AI starts here |
| `list_sources` / `list_clusters` | Enabled sources / cluster list |
| `search_tables` | Fuzzy-find tables by name fragment |
| `schema` / `table_stats` | Table inventory / sizes, scans, bloat |
| `columns` / `indexes` / `relationships` | Shape, indexes, FK graph for JOINs |
| `query` / `sample` | Guarded SELECT (500-row cap, 15s timeout) / first N rows |
| `explain` / `slow_queries` | Plan JSON / heaviest statements |
| `doctor` | Seq scans, missing PKs, pressure, long queries |

## Data, troubleshooting

- Store: `~/.readgate/readgate.db` (SQLite WAL) + `key.bin` (`0600`).
  Override the folder with `READGATE_HOME` (also used to run an isolated copy).
- Passwords are AES-GCM encrypted; backend errors (never secrets) land in
  Settings → Logs — reproduce the error, hit reload, paste the red lines.
- If the app ever shows a blank page: quit **every** ReadGate process, then launch fresh.
  (Windows: end them in Task Manager. macOS: Cmd+Q. Linux: `pkill ReadGate`.)

## Build from source

```bash
go mod tidy
# Windows:
wails build -platform windows/amd64 -o ReadGate.exe   # build/bin/ReadGate.exe
# macOS (Universal: Intel + Apple Silicon):
wails build -platform darwin/universal -o ReadGate    # build/bin/ReadGate.app
# Linux:
wails build -platform linux/amd64 -o ReadGate         # build/bin/ReadGate
wails dev            # desktop + hot reload
cd frontend && npm install && npm run dev   # frontend only
```

Linux needs WebKitGTK dev packages once:
`sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev`.

`ReadGate mcp` runs the MCP server on stdio (used by opencode `type: local`).
`ReadGate --shot-seed` seeds a synthetic demo profile (used for screenshots).

## Releasing

Every push to `main` publishes a new GitHub Release automatically
(`.github/workflows/release.yml`): the same version is built on Windows, macOS,
and Linux with the version baked in
(`-ldflags "-X readgate/internal/version.Version=v…"`), each seeded
(`--shot-seed`) and screenshotted while running, and uploaded as
`ReadGate-Windows-amd64.exe` (+ `ReadGate.exe` alias),
`ReadGate-macOS-universal.zip`, `ReadGate-Linux-amd64.tar.gz`,
plus `screenshot.png` (Windows hero) and per-OS shots.

The version bump follows [Conventional Commits](https://www.conventionalcommits.org/):

| Commit message | Bump |
|---|---|
| `feat: ...` / `feat(scope): ...` | minor (`x.Y.0`) |
| `fix: ...` / `fix(scope): ...` | patch (`x.y.Z`) |
| `...!:` / `BREAKING CHANGE` in body | major (`X.0.0`) |
| anything else (`docs:`, `chore:`, …) | patch |

Add `[skip release]` to the HEAD commit message to skip publishing.
Pushes that don't touch the shipped app (docs, `docs/`, `.github/`, `scripts/`)
are skipped automatically — they batch up into the next app release instead.
Pull requests and pushes run `CI` instead: `go build`, `go test`, `go vet`,
`staticcheck` (bug detection) plus `staticcheck -checks "all"` (style lint),
and the frontend `vite build`.

Refresh the docs screenshot any time (seeded demo profile, never your live data):

```powershell
# Windows:
./scripts/screenshot.ps1 -OutFile "docs/screenshot.png"
```

```bash
# macOS / Linux (Linux CI runs under xvfb-run):
./scripts/screenshot.sh --out "docs/screenshot.png"
# Linux headless:
xvfb-run -a ./scripts/screenshot.sh --exe build/bin/ReadGate --out docs/screenshot.png
```

## Project layout

```
ReadGate/
  main.go / app.go            # Wails entry, backend bindings (fleet, browse, MCP, writes)
  internal/
    pg/ my/ mssql/            # server engines (Postgres / MySQL / SQL Server families)
    lite/ turso/              # SQLite file / Turso libsql
    dbops/ engines/           # engine dispatcher + registry (ports, status)
    guard/                    # read-only + write SQL validation
    store/                    # SQLite + AES-GCM secrets + settings + write logins
    mcpserver/                # MCP over stdio + HTTP (14 tools, names only)
    shotseed/                 # synthetic demo profile for good screenshots
    version/                  # release tag baked via ldflags
  frontend/src/               # React UI (fleet, browse, query, doctor, MCP, settings)
  scripts/                    # next-version.ps1, screenshot.ps1 (Windows), screenshot.sh (mac/Linux)
  docs/screenshot.png         # app screenshot, seeded demo data (README + releases)
```
