# RunX

**AI-first Process Runtime for Local Development**

RunX replaces `nohup`, `tmux`, `screen`, `ps`, `kill`, `tail -f`, `grep`, and
`sleep 5` with a single binary and one daemon. One runtime for humans and AI
agents: CLI, terminal dashboard, desktop GUI and an MCP toolset for agents.

```
runx start backend --cwd ./backend -- go run .
runx start frontend --cwd ./frontend -- npm run dev
runx ps --json
runx dashboard          # terminal TUI
runx-gui                # desktop app (Wails)
```

## Why RunX?

AI agents need to test endpoints, run migrations, execute integration tests -
but first they need the application running.

Without RunX:
```
1. Open terminal, cd backend, go run .         # starts the app
2. Open another terminal, curl /api/health     # test endpoint
3. App crashes -> scroll up to find the error  # waste time
4. Ctrl+C, fix, re-run                          # repeat
5. Agent can't see anything                    # blind
```

With RunX:
```
runx start backend --cwd ./backend -- go run .   # start once
curl http://localhost:8080/health                  # test immediately
runx logs backend                                  # read errors as JSON
runx restart backend                               # restart with fix
runx kill backend                                  # cleanup
```

Every command outputs JSON. AI agents can parse everything without parsing
terminal scrollback.

## Features

- **Single daemon** - one background process owns all managed processes; they
  survive CLI, MCP and GUI restarts
- **Process lifecycle** - start, stop (SIGTERM), restart, kill (SIGKILL) by
  process-tree (process group), by name or id
- **Name-based identity** - a name maps to exactly one live process; starting
  again with the same name replaces the old instance
- **Unix Socket API** - HTTP over `~/.runx/runx.sock`, JSON everywhere
- **Terminal Dashboard** - Bubble Tea TUI with real-time metrics
- **Desktop GUI** - Wails app: stack launcher, config editor, live process
  dashboard with logs, events and network panels
- **MCP server** - 21 process-manager tools for AI agents (`runx mcp`)
- **Logs** - per-process ring buffer (10k lines), `since` cursor for
  incremental reads, `stream` and `grep` filters
- **Events** - started/stopped/restarted/exited/killed timeline with cursor
  and process filter
- **Tree-aggregated metrics** - CPU, RSS, VMEM, swap, threads, fds, read/write
  bytes collected over the whole process tree, so `go run .` reports the load
  of its real child binary, not the idle wrapper
- **Process detail** - on-demand snapshot: process tree, listening ports,
  grouped connections, open fd paths, real exe/cwd (`runx ports`,
  `process_ports`, `process_detail`)
- **Server-side waits** - block until status, exit (with code), log pattern,
  TCP port (by PID), HTTP URL or a fixed interval - no client polling
- **Health monitoring** - periodic HTTP probes; `healthy: false`, `last_error`
  and `health_failed`/`health_recovered` events when a probe fails. Local-port
  probes verify the PID actually listens, so a foreign service squatting on the
  port is reported instead of being mistaken for health
- **Exit details** - `exit_code`, `exit_signal` and `last_error` on every
  finished process, plus descriptive event messages
- **Temporary mode** - `--ttl` (auto-stop), `--idle` (stop when silent),
  `--ephemeral` (forget on daemon exit)
- **Stack orchestration** - launch a whole stack from YAML: pre_steps then
  processes in parallel, with health waits
- **Exec/Shell/Attach** - run commands or open a shell in the process cwd

## Architecture

```
 +------------+   +----------+   +-------------------+   +----------+
 | runx CLI   |   | runx mcp |   | runx-gui (Wails)  |   |   TUI    |
 | runx start |   |  (MCP)   |   |  desktop app      |   | dashboard|
 +-----+------+   +----+-----+   +---------+---------+   +----+-----+
       |               |                   |                   |
       +---------------+---- Unix socket ---+-------------------+
                          (HTTP/JSON)
                               |
                               v
               +------------------------------+
               |         runx daemon          |
               |  ProcessManager              |
               |  +------+ +------+           |
               |  |Proc 1| |Proc 2|  ...      |
               |  |Logs  | |Logs  |           |
               |  |Met   | |Met   |           |
               |  |Ports | |Ports |           |
               |  +------+ +------+           |
               |  Events  Metrics  Health     |
               +------------------------------+
```

The daemon is the single source of truth. Every client (CLI, MCP, GUI, TUI)
talks to it over the unix socket, so processes survive any client restart and
multiple agents can share one daemon.

## Install

```bash
go build -o runx .
mv runx /usr/local/bin/
```

Desktop GUI (optional):

```bash
cd cmd/runx-gui/frontend && npm install && npm run build
cd .. && go build -tags "webkit2_41 production" -o build/bin/runx-gui .
```

Build tags matter: `production` is required by Wails (without it the app
refuses to start), `webkit2_41` selects WebKitGTK 4.1 (use `webkit2_40` for
4.0, or drop it if you only have 4.0).

## Quick Start

```bash
# Start a web server
runx start backend --cwd ./backend -- go run .

# Start frontend build watcher
runx start frontend --cwd ./frontend -- npm run dev

# Start a database
runx start postgres --cwd /var/lib/postgres -- postgres -D data

# See what's running
runx ps

# Open the terminal dashboard
runx dashboard
```

The daemon auto-starts on the first command (socket at `~/.runx/runx.sock`,
pid at `~/.runx/daemon.pid`).

## Commands

### `runx start <name> [flags] -- <command>...`

Start a new managed process.

| Flag | Description |
|------|-------------|
| `--cwd <dir>` | Working directory (default: `.`) |
| `--ttl <duration>` | Auto-stop after duration (e.g. `30m`, `2h`) |
| `--ephemeral` | Remove process when runx exits |
| `--idle <duration>` | Stop if no output for the duration |

```bash
runx start api --cwd ./api -- go run ./cmd/server
runx start worker --ttl 1h -- node worker.js
runx start temp --ephemeral -- python script.py
```

### `runx stop|restart|kill <name>`

- `stop` - SIGTERM to the process tree (graceful)
- `restart` - stop, then start a fresh instance with the same config (new id)
- `kill` - SIGKILL to the process tree

### `runx ps [--json] [--wide]`

List managed processes. By default `ps` shows all; JSON mode is machine
readable.

```json
[
  {
    "id": "backend-a1b2c3",
    "name": "backend",
    "pid": 38452,
    "status": "running",
    "cpu": 0.5,
    "memory": 41943040,
    "command": "go"
  }
]
```

### `runx logs <name> [-n <lines>] [--json]`

Process output from the ring buffer (last 10k lines per stream, merged and
sorted by time).

```bash
runx logs backend --json | jq -r '.[] | select(.stream == "stderr") | .line'
```

### `runx metrics <name> [--json]`

Tree-aggregated resource usage: CPU, RSS, VMEM, swap, threads, fds,
read/write bytes, cpu times, context switches.

### `runx events [--json]`

Lifecycle event timeline (started, stopped, restarted, exited, killed,
health_failed, health_recovered).

### `runx ports <name>`

Listening ports and network connections of the whole process tree:

```
Process tree:
  pid 38452   ppid 37101   [sleep] go run .
  pid 38520   ppid 38452   [sleep] /home/user/.cache/go-build/.../app

Listening ports:
  tcp://*:8000

Connections:
  ESTABLISHED  127.0.0.1:4222  -> 127.0.0.1:5439      x14
  ESTABLISHED  192.168.1.5:90  -> 151.241.221.236:8080 x1
```

### `runx exec <name> -- <command>...`

Run a one-shot command in the process working directory with its environment.

### `runx shell <name>`

Open an interactive shell in the process working directory.

### `runx attach <name>`

Follow process output in real-time (like `docker attach`).

### `runx wait <name> [--timeout <duration>]`

Block until the process exits. Returns non-zero on timeout.

### `runx gc`

Clean up finished process records (ephemeral or older than 5 minutes).

### `runx up|down`

Launch/stop a stack from a YAML config (see below).

### `runx dashboard`

Open the terminal dashboard (Bubble Tea TUI).

### `runx daemon`

Start the background daemon in the foreground (normally auto-started).

### `runx mcp`

Serve the MCP toolset over stdio (see MCP Server).

## Launch a whole stack from YAML

`runx up` runs pre_steps first (docker compose up, migrations, codegen), then
starts every process in the config. `runx down` stops everything from a
config. See `examples/webapp.yaml` for a full example.

```yaml
name: webapp

env:
  NODE_ENV: development

pre_steps:
  - name: postgres
    command: docker compose up -d
    timeout: 120s

  - name: migrate
    command: make migrate
    ignore_errors: true

processes:
  - name: backend
    cwd: ./backend
    command: go run .
    health:
      url: http://localhost:8080/health
      interval: 2s
  - name: frontend
    cwd: ./frontend
    command: npm run dev
```

```bash
runx up dev.yaml      # pre_steps, then start all processes
runx down dev         # stop every process of the dev config
```

| Field | Description |
|-------|-------------|
| `name` | Required, unique id. Managed processes are named `<name>.<process>`. |
| `root` | Base dir for relative `cwd`. Defaults to the config file's dir. |
| `env` | Env applied to every pre_step and process. |
| `pre_steps` | One-shot commands run sequentially before processes. |
| `pre_steps[].command` | Full command line, or executable when `args` is set. |
| `pre_steps[].cwd` | Working dir, relative to `root`. |
| `pre_steps[].ignore_errors` | Keep going even if the step exits non-zero. |
| `pre_steps[].timeout` | Duration string; fail the step if it exceeds this. |
| `processes` | Long-running processes, all started in parallel. |
| `processes[].command` | Full command line, or executable when `args` is set. |
| `processes[].cwd` | Working dir, relative to `root`. |
| `processes[].env` | Env merged on top of the config-level `env`. |
| `processes[].health` | Readiness probe; `up` waits for it and the daemon keeps monitoring afterwards. |

## Health monitoring

Attach a periodic HTTP probe to a process. The daemon polls the URL and
tracks the result:

- `healthy: false` on the process record while the probe fails
- `last_error` with the exact reason (`connection refused`, `process does not
  listen on port 8000 (occupied by another process?)`, ...)
- `health_failed` / `health_recovered` events on state transitions

Local-port probes (any `http://host:port/...` URL) also verify that the
**process PID itself** listens on that port - a foreign service squatting on
the same port is reported, not mistaken for health.

Configure it in the stack config (`processes[].health`) or attach at runtime:

```bash
curl --unix-socket ~/.runx/runx.sock -X POST http://localhost/health/backend \
  -d '{"url":"http://localhost:8000/health","interval":"2s"}'
```

This catches the classic "process is alive but not serving" case (e.g. a port
collision swallowed by the app).

## Process detail

On-demand snapshot of a process and its whole tree:

- **tree** - pid/ppid/state/cmd for the wrapper and every child
- **ports** - listening sockets owned by the tree
- **connections** - grouped by remote/state (`12 ESTABLISHED -> 1.2.3.4:443`)
- **fd_paths** - open file descriptors (Linux, via `/proc`)
- **exe / cwd** - the real executable and working directory
- **metrics** - resources aggregated over the tree

```bash
runx ports backend
curl --unix-socket ~/.runx/runx.sock http://localhost/process/backend/detail
```

## MCP Server

`runx mcp` serves the process-manager toolset over the Model Context Protocol
(stdio transport). AI agents get first-class process management tools and stop
needing `nohup`, `setsid`, `disown`, `< /dev/null > log 2>&1 &`, manual PID
hunting or `sleep`+`grep` polling loops.

The MCP server is a thin client of the runx daemon: processes are owned by the
daemon, so they survive MCP server restarts and multiple agents can share one
daemon. Processes are killed by pid from the daemon state - no `pkill -f`
pattern matching.

```jsonc
// opencode.json / claude_desktop_config.json
{
  "mcpServers": {
    "runx": {
      "command": "runx",
      "args": ["mcp"]
    }
  }
}
```

### Tools

| Tool | What it does |
|------|--------------|
| `process_start` | Start a managed process, return immediately (name, cwd, command, args, env, ttl, idle_timeout, ephemeral) |
| `process_stop` | SIGTERM to the process tree, by id/name |
| `process_kill` | SIGKILL to the process tree, by id/name |
| `process_restart` | Stop and start a fresh instance with the same config |
| `process_list` | Live processes (id, pid, status, uptime, exit code); `all=true` includes finished |
| `process_inspect` | Full record: args, cwd, env, events, latest metrics, exit details |
| `process_logs` | Ring-buffer output; cursor `since`, `stream`, `grep` filters |
| `process_events` | Lifecycle timeline; cursor `since`, `process` filter |
| `process_metrics` | Tree-aggregated CPU, memory, RSS, threads, fd, IO snapshot |
| `process_exec` | One-shot command in the process cwd/env, returns output |
| `process_wait_status` | Block until status matches (e.g. `running`) or timeout |
| `process_wait_exit` | Block until exit, returns exit code |
| `process_wait_log` | Block until a line matches a regex; atomic with `since` cursor |
| `process_wait_port` | Block until the process (by pid) listens on a TCP port |
| `process_wait_url` | Block until an HTTP endpoint responds (readiness probe) |
| `process_ports` | Listening ports + grouped connections of the process tree |
| `process_detail` | Full snapshot: tree, ports, connections, fd paths, exe/cwd, tree metrics |
| `sleep` | Wait a fixed interval |
| `stack_up` | Launch a YAML stack: pre_steps, then all processes (with health waits) |
| `stack_down` | Stop every process of a launched config |
| `process_gc` | Clean up finished records; `force=true` removes them immediately |

### Agent workflow

```text
process_start mockserver -- bin/mockserver-bin
process_wait_port mockserver 8090
process_start app --env KEY=VALUE -- bin/cli-executor-bin
process_wait_url app http://localhost:8000/
process_ports app          # check what the app actually listens on
process_stop mockserver && process_restart app   # after killing the mock
```

All responses are JSON (text + structured content). Timeouts return `isError`
with the reason, so the agent can retry or inspect logs.

## Desktop GUI (runx-gui)

Wails desktop app wired to the same daemon - it sees every process (CLI, MCP,
GUI-launched) in one place.

- **Launch** - pick a saved config, run pre_steps (streamed output) and
  processes, stop all
- **Configs** - visual YAML editor (name, root, env, pre_steps, processes with
  args/env/health); saved to `~/.runx/configs/*.yaml`
- **Processes** - live dashboard: summary strip (total/running/unhealthy/
  failed), process cards with status badges, CPU/MEM bars, failure reasons,
  and a detail pane with Logs / Events / Ports tabs (process tree, listening
  ports, connections, paths, tree resources)

```bash
./build/bin/runx-gui
```

The GUI auto-starts the daemon on launch (it never spawns copies of itself -
it starts a real `runx` from PATH).

## Terminal Dashboard

```
+--------------------------------------------------------------+
| runx                                     CPU 2.1%  MEM 40MB  |
+-------------+------------------------------------------------+
| Dashboard    Logs    Metrics    Events                       |
+-------------+------------------------------------------------+
| Processes                                                    |
|                                                              |
| > backend    38452  running  0.0%   40MB   8   1m32s  go     |
|   frontend   39201  running  0.0%   32MB   6   1m30s  npm    |
|                                                              |
| Total: 2 | Running: 2 | Stopped: 0                           |
+--------------------------------------------------------------+
| Tab:next  Enter:logs  R:restart  S:stop  K:kill  Q:quit     |
+--------------------------------------------------------------+
```

Key bindings: `Tab` next tab, `Enter` logs, `R` restart, `S` stop, `K` kill,
`M` metrics, `E` events, `D` dashboard, `Space` log follow, `Q` quit.

## AI Mode

Every CLI command supports `--json` for machine-readable output. The unix
socket API provides direct access without the CLI:

```bash
curl -s --unix-socket ~/.runx/runx.sock http://localhost/processes
curl -s --unix-socket ~/.runx/runx.sock http://localhost/logs/backend?n=50
curl -s --unix-socket ~/.runx/runx.sock "http://localhost/events?since=1788460000000"
curl -s --unix-socket ~/.runx/runx.sock -X POST http://localhost/start \
  -d '{"name":"worker","cwd":"./worker","command":"node","args":["index.js"]}'
curl -s --unix-socket ~/.runx/runx.sock -X POST http://localhost/wait/backend \
  -d '{"condition":"log","pattern":"Listening","timeout":"30s"}'
```

## Platform support

- **Linux** - full functionality (process groups, signals, `/proc` fd paths)
- **macOS** - full functionality (process groups, signals; fd paths disabled)
- **Windows** - builds and works, with caveats: no graceful SIGTERM (stop is
  an immediate kill), no process-group signals (children may survive), fd
  paths disabled

## Library Stack

| Library | Purpose |
|---------|---------|
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | TUI framework |
| [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Terminal styling |
| [gopsutil](https://github.com/shirou/gopsutil) | Process/network metrics |
| [mcp-go](https://github.com/mark3labs/mcp-go) | MCP server |
| [Wails](https://wails.io) | Desktop GUI |
| Go `net/http` | Unix socket API |

## Development

```bash
git clone <repo>
cd runx
go build -o runx .
./runx daemon &       # start background server
./runx start myapp -- mycommand
./runx dashboard      # open TUI
```

### Testing

```bash
# Start daemon
./runx daemon &
# Run processes
./runx start test --cwd /tmp -- sleep 30
# Verify
./runx ps
./runx kill test
# Cleanup
kill %1
```

## Roadmap

- [x] Process lifecycle management
- [x] Unix socket API
- [x] JSON output for AI
- [x] Tree-aggregated metrics (gopsutil)
- [x] Log ring buffer with cursors
- [x] Event system with cursors
- [x] Terminal dashboard
- [x] Desktop GUI (Wails)
- [x] Exec/Shell in process context
- [x] Server-side waits (status/exit/log/port/url/interval)
- [x] Temporary mode (TTL, idle, ephemeral)
- [x] Health monitoring in the daemon
- [x] Process detail (ports, connections, fd paths, tree)
- [x] MCP server (`runx mcp`) with process-manager toolset
- [x] Stack orchestration from YAML
- [ ] PTY-based attach
- [ ] Remote daemon support
- [ ] Web dashboard
- [ ] MCP: optional file logging (`log_path`) with rotation