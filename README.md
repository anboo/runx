# RunX

**AI-first Process Runtime for Local Development**

RunX replaces `nohup`, `tmux`, `screen`, `ps`, `kill`, `tail -f`, `grep`, and `sleep 5` with a single binary. One runtime for humans and AI agents.

```
  runx start backend --cwd ./backend -- go run .
  runx start frontend --cwd ./frontend -- npm run dev
  runx ps --json
  runx dashboard
```

## Why RunX?

AI agents need to test endpoints, run migrations, execute integration tests -- but first they need the application running.

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

Every command outputs JSON. AI agents can parse everything without parsing terminal scrollback.

## Features

- **Single binary** -- no daemon installation, no config files, no YAML/TOML/JSON
- **Process lifecycle** -- start, stop, restart, kill
- **Unix Socket API** -- connect from any tool, including AI agents
- **JSON mode** -- `--json` on every command for machine consumption
- **Terminal Dashboard** -- Bubble Tea TUI with real-time metrics
- **Metrics** -- CPU, Memory, RSS, Threads, FD, Network, Disk IO via gopsutil
- **Logs** -- per-process ring buffer (10k entries), follow mode
- **Events** -- started, stopped, restarted, exited, killed timeline
- **Exec/Shell** -- run commands or open shell in process `cwd`
- **Wait** -- block until process exits (for automation scripts)
- **Attach** -- follow process output in real-time
- **Temporary mode** -- `--ttl`, `--ephemeral`, `--idle` for disposable processes

## Install

```bash
go build -o runx .
```

Place the binary in your `$PATH`:

```bash
mv runx /usr/local/bin/
```

No dependencies. No services to install. No config files.

## Workflow for AI Agents

1. **Start the app** as a managed background process
2. **Wait** for it to be ready
3. **Test** endpoints with curl/gcurl
4. **Read logs** if something fails (--json for structured output)
5. **Restart** with changes, **kill** when done

All commands are non-blocking (except `wait`). The daemon keeps processes alive in background.

```bash
# Typical agent session:
runx start api --cwd ./api -- sh -c 'go run .'
runx wait api --timeout 30s
curl -s http://localhost:8080/health | jq .
runx logs api --json | jq -r '.[] | select(.stream == "stderr") | .line'
runx exec api -- go test ./...
runx kill api
```

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

# Open dashboard
runx dashboard
```

## Commands

### `runx start <name> [flags] -- <command>...`

Start a new managed process.

| Flag | Description |
|------|-------------|
| `--cwd <dir>` | Working directory (default: `.`) |
| `--ttl <duration>` | Auto-stop after duration (e.g. `30m`, `2h`) |
| `--ephemeral` | Remove process when runx exits |
| `--idle <duration>` | Stop if idle for duration |

```bash
runx start api --cwd ./api -- go run ./cmd/server
runx start worker --ttl 1h -- node worker.js
runx start temp --ephemeral -- python script.py
```

### `runx stop <name>`

Send SIGTERM to a process. Finds by name or ID.

### `runx restart <name>`

Stop and restart a process. Assigns new ID, preserves metadata.

### `runx kill <name>`

Send SIGKILL to a process.

### `runx ps [--json] [--wide]`

List all managed processes.

```
PID      Name         Status   CPU%     Command
----------------------------------------------------------------------
38452    backend      running  0.0      go run .
39201    frontend     running  0.0      npm run dev
```

With `--json`:

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

View process logs. Uses ring buffer (last 10k lines).

### `runx metrics <name> [--json]`

Real-time process metrics.

```
CPU:     2.1%
Memory:  40MB
RSS:     38MB
VMEM:    120MB
Threads: 8
FD:      12
Network: 0.0KB RX / 0.0KB TX
Disk:    0.0MB R / 0.0MB W
IO Wait: 1.4%
```

With `--json`:

```json
{
  "cpu": 2.1,
  "memory": 41943040,
  "rss": 39845888,
  "threads": 8,
  "fd_count": 12
}
```

### `runx events [--json]`

Process lifecycle event log.

```
09:18:57 started     started go (pid 38452) in ./backend
09:19:00 stopped     stopped via SIGTERM
09:19:21 restarted   restarted backend-a1b2c3 -> backend-d4e5f6 (attempt 1)
```

### `runx exec <name> -- <command>...`

Execute a command in the process's working directory.

```bash
runx exec backend -- pwd
# /home/user/project/backend
```

### `runx shell <name>`

Open an interactive shell in the process's working directory.

```bash
runx shell backend
# bash: /home/user/project/backend$
```

### `runx attach <name>`

Follow process output in real-time (like `docker attach`).

### `runx wait <name> [--timeout <duration>]`

Block until process exits. Returns non-zero if timeout.

```bash
runx wait backend --timeout 5m
```

### `runx gc`

Clean up finished processes and old data.

### `runx dashboard`

Open the terminal dashboard.

## AI Mode

Every command supports `--json` for machine-readable output.

```bash
runx ps --json        # process list as JSON array
runx logs api --json  # log entries as JSON array
runx metrics api --json  # metrics as JSON object
runx events --json    # events as JSON array
```

The Unix socket API provides direct access without CLI:

```bash
# From any tool or AI agent:
curl -s --unix-socket ~/.runx/runx.sock http://localhost/processes
curl -s --unix-socket ~/.runx/runx.sock http://localhost/logs/backend?n=50
curl -s --unix-socket ~/.runx/runx.sock -X POST http://localhost/start \
  -d '{"name":"worker","cwd":"./worker","command":"node","args":["index.js"]}'
```

## Dashboard

```
┌──────────────────────────────────────────────────────────────┐
│ runx                                     CPU 2.1%  MEM 40MB  │
├─────────────┬────────────────────────────────────────────────┤
│ Dashboard    Logs    Metrics    Events                       │
├─────────────┴────────────────────────────────────────────────┤
│ Processes                                                    │
│                                                              │
│ > backend    38452  running  0.0%   40MB   8   1m32s  go     │
│   frontend   39201  running  0.0%   32MB   6   1m30s  npm    │
│                                                              │
│ Total: 2 | Running: 2 | Stopped: 0                           │
├──────────────────────────────────────────────────────────────┤
│ Tab:next  Enter:logs  R:restart  S:stop  K:kill  Q:quit     │
└──────────────────────────────────────────────────────────────┘
```

### Key Bindings

| Key | Action |
|-----|--------|
| `Tab` | Next tab |
| `Enter` | Open logs for selected process |
| `R` | Restart selected process |
| `S` | Stop selected process |
| `K` | Kill selected process |
| `M` | Show metrics for selected process |
| `E` | Events tab |
| `D` | Dashboard tab |
| `Space` | Toggle log follow mode |
| `Up/Down` or `j/k` | Navigate process list |
| `Q` or `Ctrl+C` | Quit |

## Architecture

```
             ┌─────────────────┐
             │     runx CLI     │
             └────────┬────────┘
                      │ Unix Socket
                      ▼
    ┌──────────────────────────────────┐
    │         Daemon (background)      │
    │                                  │
    │  ┌────────────────────────────┐  │
    │  │     ProcessManager         │  │
    │  │  ┌──────┐ ┌──────┐ ┌────┐ │  │
    │  │  │Proc 1│ │Proc 2│ │... │ │  │
    │  │  │      │ │      │ │    │ │  │
    │  │  │ √Logs│ │ √Logs│ │    │ │  │
    │  │  │ √Met │ │ √Met │ │    │ │  │
    │  │  └──────┘ └──────┘ └────┘ │  │
    │  └────────────────────────────┘  │
    │                                  │
    │  ┌──────┐  ┌──────┐  ┌───────┐  │
    │  │Events│  │Metric│  │Sess.  │  │
    │  │Buffer│  │Poll  │  │Mgr    │  │
    │  └──────┘  └──────┘  └───────┘  │
    └──────────────────────────────────┘
```

## Library Stack

| Library | Purpose |
|---------|---------|
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | TUI framework |
| [Lip Gloss](https://github.com/charmbracelet/lipgloss) | Terminal styling |
| [gopsutil](https://github.com/shirou/gopsutil) | Process metrics |
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
- [x] Metrics collection (gopsutil)
- [x] Log ring buffer
- [x] Event system
- [x] Terminal dashboard
- [x] Exec/Shell in process context
- [x] Wait/Attach
- [x] Temporary mode (TTL, ephemeral, idle)
- [ ] PTY-based attach
- [ ] Health checks
- [ ] Config file (optional, YAML/TOML)
- [ ] Remote daemon support
- [ ] Web dashboard
