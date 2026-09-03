# RunX Skill for AI Agents

## MCP Toolset (preferred)

If your client has the runx MCP server configured (`runx mcp` over stdio),
use the `process_*` tools instead of shell backgrounding:

- `process_start` - start a managed process (daemon-owned, non-blocking)
- `process_wait_port` / `process_wait_url` / `process_wait_status` - wait for readiness instead of `sleep`+curl loops
- `process_wait_log` - wait for a specific line in the output (regex)
- `process_wait_exit` - wait for a job to finish, returns exit code
- `process_logs` / `process_events` - read output and lifecycle timeline with `since` cursors
- `process_stop` / `process_kill` / `process_restart` / `process_gc` - control and cleanup

Never use `nohup`, `setsid`, `disown` or `< /dev/null > log 2>&1 &`: the daemon
already detaches processes and kills them by pid from its own state (no
`pkill -f` pattern matching).

## Primary Use Case: Start App -> Test Endpoints

Before an AI agent can test endpoints with curl/gcurl, it needs the application running. RunX starts the app as a managed background process, so the agent can:

```bash
# 1. Start the app
runx start backend --cwd ./backend -- sh -c 'go run .'

# 2. Wait for it to be ready
runx wait backend --timeout 30s

# 3. Now test endpoints
curl http://localhost:8080/health

# 4. Check logs if something fails
runx logs backend

# 5. Restart with different config if needed
runx restart backend

# 6. Kill when done
runx kill backend
```

This replaces the manual workflow of: open terminal -> `cd backend` -> `go run .` -> open another terminal -> `curl ...` -> read scrollback -> Ctrl+C -> re-run.

## How to start a process

```bash
runx start <name> --cwd <dir> -- <command>
```

Name is used to reference the process later. If a process with the same name is already running, it gets killed first.

For chained commands, always use `sh -c`:

```bash
runx start backend --cwd ./backend -- sh -c 'go run .'
runx start db --cwd . -- sh -c 'docker compose up -d && sleep infinity'
```

## How to check what is running

```bash
runx ps                 # table view
runx ps --json          # machine-readable
```

Returns: name, pid, status (running/exited/killed), cpu, memory, uptime.

## How to read logs

```bash
runx logs <name>                # last 50 lines (stdout + stderr, merged by time)
runx logs <name> -n 200         # last 200 lines
runx logs <name> --json         # JSON array with stream/line/timestamp
```

## How to check metrics

```bash
runx metrics <name>             # formatted: cpu, memory, rss, threads, fd
runx metrics <name> --json      # machine-readable
```

## How to get process events

```bash
runx events                     # all events (started, stopped, exited, killed, restarted)
runx events --json
```

## How to wait for a process

```bash
runx wait <name>                # blocks until process exits (default timeout 5m)
runx wait <name> --timeout 30s
```

Useful for: waiting for a task to finish before proceeding.

## How to restart a process

```bash
runx restart <name>
```

## How to stop or kill a process

```bash
runx stop <name>                # SIGTERM
runx kill <name>                # SIGKILL
```

## How to exec a command in process directory

```bash
runx exec <name> -- <command>
```

Executes in the same working directory as the process. Example:

```bash
runx exec backend -- pwd        # returns the backend directory
runx exec backend -- go test ./...
```

## How to open a shell in process directory

```bash
runx shell <name>
```

Opens interactive bash in the process working directory.

## How to start disposable processes

```bash
runx start worker --ttl 30m -- python worker.py     # auto-stop after 30m
runx start temp --ephemeral -- python script.py     # removed on daemon exit
```

## How to attach to a running process

```bash
runx attach <name>
```

Follows process output (like `tail -f`). Press Ctrl+C to detach.

## How to use JSON output in scripts

Every command supports `--json`. Use with `jq` for scripting:

```bash
runx ps --json | jq -r '.[] | select(.status == "running") | .name'
runx metrics backend --json | jq '.cpu'
runx logs backend --json | jq -r '.[] | select(.stream == "stderr") | .line'
```

## Unix socket API (direct access, no CLI)

Socket: `~/.runx/runx.sock`

```bash
curl -s --unix-socket ~/.runx/runx.sock http://localhost/processes
curl -s --unix-socket ~/.runx/runx.sock http://localhost/logs/backend?n=100
curl -s --unix-socket ~/.runx/runx.sock -X POST http://localhost/start \
  -H 'Content-Type: application/json' \
  -d '{"name":"worker","cwd":"/tmp","command":"sleep","args":["30"]}'
curl -s --unix-socket ~/.runx/runx.sock -X POST http://localhost/kill/backend
```

## Typical AI workflow

1. Start services:

```bash
runx start db --cwd . -- sh -c 'docker compose up -d'
runx start backend --cwd ./backend -- sh -c 'go run .'
runx start frontend --cwd ./frontend -- sh -c 'npm run dev'
```

2. Wait for readiness:

```bash
runx wait backend --timeout 30s
```

3. Check if everything is running:

```bash
runx ps --json
```

4. Read logs when something fails:

```bash
runx logs backend --json | jq -r '.[] | select(.stream == "stderr") | .line'
```

5. Run tests in process context:

```bash
runx exec backend -- go test ./...
```

6. Clean up:

```bash
runx kill backend
runx kill frontend
```

## Notes

- Process lookup works by: exact ID -> ID prefix -> name. Prefers running processes.
- `runx logs` returns merged stdout+stderr, sorted by timestamp.
- Daemon auto-starts on first command. PID in `~/.runx/daemon.pid`.
- Socket at `~/.runx/runx.sock`.
- All timestamps are Unix milliseconds (int64) in JSON.
