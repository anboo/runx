package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"runx/internal/process"
)

// processSummary is the compact view returned by process_list. Keeping it
// small saves tokens when the agent polls the whole process table.
type processSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PID          int    `json:"pid"`
	Status       string `json:"status"`
	Uptime       string `json:"uptime,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Command      string `json:"command"`
	CWD          string `json:"cwd"`
	RestartCount int    `json:"restart_count"`
}

// mutations serializes mutating calls in the MCP server (see NewServer).
func registerProcessTools(s *server.MCPServer, rc *Client, mutations *sync.Mutex) {
	s.AddTool(mcp.NewTool("process_start",
		mcp.WithDescription(`Start a managed background process and return immediately.

The process is spawned by the runx daemon, not by this call, so nothing blocks
and no nohup/setsid/disown/redirect tricks are needed. It keeps running after
the MCP server exits.

Starting a process with a name that already exists stops the old instance
first (the name always maps to one live process).

For shell pipelines or chained commands use sh -c as the command, e.g.
command="sh" args=["-c", "go run . && migrate"].

Returns the full process record: id, pid, status, cwd, command.`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Unique name used to reference the process later")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Executable to run (use sh -c for pipelines)")),
		mcp.WithArray("args", mcp.Description("Arguments passed to the executable")),
		mcp.WithString("cwd", mcp.Description("Working directory (default: current directory)")),
		mcp.WithArray("env", mcp.Description("Extra environment variables as KEY=VALUE strings")),
		mcp.WithString("ttl", mcp.Description("Auto-stop after this duration, e.g. 30m or 30 (seconds)")),
		mcp.WithString("idle_timeout", mcp.Description("Stop if the process produces no output for this duration")),
		mcp.WithBoolean("ephemeral", mcp.Description("Forget the process record when the daemon exits")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		args := req.GetArguments()
		name := strArg(args, "name")
		command := strArg(args, "command")
		if name == "" {
			return errResult(errors.New("name is required")), nil
		}
		if command == "" {
			return errResult(errors.New("command is required")), nil
		}
		cwd := strArg(args, "cwd")
		if cwd == "" {
			cwd = "."
		}
		p, err := rc.Start(name, cwd, command,
			strSliceArg(args, "args"), strSliceArg(args, "env"),
			durationArg(args, "ttl"), durationArg(args, "idle_timeout"), boolArg(args, "ephemeral"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(p)
	})

	s.AddTool(mcp.NewTool("process_stop",
		mcp.WithDescription(`Stop a managed process gracefully (SIGTERM to the whole
process tree). Finds the process by id, id prefix or name. Returns the process
id once stopped.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if err := rc.Stop(id); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "stopped", "process": id})
	})

	s.AddTool(mcp.NewTool("process_kill",
		mcp.WithDescription(`Force-kill a managed process (SIGKILL to the whole
process tree). Use when process_stop did not help. Finds the process by id, id
prefix or name.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if err := rc.Kill(id); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "killed", "process": id})
	})

	s.AddTool(mcp.NewTool("process_restart",
		mcp.WithDescription(`Restart a managed process: stop the current instance and
start a fresh one with the same name, cwd, command, args, env and ttl. The new
instance gets a new id. Returns the new process record.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		p, err := rc.Get(id)
		if err != nil {
			return errResult(err), nil
		}
		if err := rc.Restart(id); err != nil {
			return errResult(err), nil
		}
		restarted, err := rc.Get(p.Name)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]any{
			"status":        "restarted",
			"old_id":        p.ID,
			"new_id":        restarted.ID,
			"new_pid":       restarted.PID,
			"restart_count": restarted.RestartCount,
		})
	})

	s.AddTool(mcp.NewTool("process_list",
		mcp.WithDescription(`List managed processes. By default only live processes
(running/starting) are returned so finished ones do not pile up; pass
all=true to include exited/killed/stopped records. Each entry carries id,
name, pid, status, uptime, exit code, command, cwd.`),
		mcp.WithBoolean("all", mcp.Description("Include finished processes (default false)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		procs, err := rc.List()
		if err != nil {
			return errResult(err), nil
		}
		all := boolArg(req.GetArguments(), "all")
		out := make([]processSummary, 0, len(procs))
		for _, p := range procs {
			if !all && p.Status != process.StatusRunning && p.Status != process.StatusStarting {
				continue
			}
			out = append(out, processSummary{
				ID:           p.ID,
				Name:         p.Name,
				PID:          p.PID,
				Status:       string(p.Status),
				Uptime:       p.Uptime,
				ExitCode:     p.ExitCode,
				Command:      p.Command,
				CWD:          p.CWD,
				RestartCount: p.RestartCount,
			})
		}
		return textResult(out)
	})

	s.AddTool(mcp.NewTool("process_inspect",
		mcp.WithDescription(`Full detail about one managed process: id, name, pid,
status, uptime, exit code, command, args, cwd, env, restart count, ttl,
ephemeral flag, recent lifecycle events and the latest metrics snapshot.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := rc.Get(strArg(req.GetArguments(), "process"))
		if err != nil {
			return errResult(err), nil
		}
		return textResult(p)
	})

	s.AddTool(mcp.NewTool("process_exec",
		mcp.WithDescription(`Run a one-shot command in the working directory of a
managed process with its environment, and return the combined output. Blocks
until the command finishes. Useful for migrations, tests, or inspecting the
process workspace.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name whose cwd/env to use")),
		mcp.WithArray("args", mcp.Required(), mcp.Description("Command and its arguments, e.g. [\"go\", \"test\", \"./...\"]")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id := strArg(args, "process")
		cmd := strSliceArg(args, "args")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if len(cmd) == 0 {
			return errResult(errors.New("args are required")), nil
		}
		out, err := rc.Exec(id, cmd)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"output": out})
	})

	s.AddTool(mcp.NewTool("process_gc",
		mcp.WithDescription(`Clean up finished process records. By default removes
only records older than 5 minutes (or ephemeral ones); pass force=true to
remove every exited/killed process immediately. Returns the removed ids.`),
		mcp.WithBoolean("force", mcp.Description("Remove every finished record regardless of age (default false)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		var removed []string
		var err error
		if boolArg(req.GetArguments(), "force") {
			removed, err = rc.GCForce()
		} else {
			removed, err = rc.GC()
		}
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]any{"removed": removed, "count": len(removed)})
	})
}
