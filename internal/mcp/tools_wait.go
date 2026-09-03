package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"runx/internal/socket"
)

// waitResult converts a daemon WaitResponse into a tool result. A failed wait
// (timeout, process died) is reported as an error so the LLM sees why and can
// self-correct.
func waitResult(resp *socket.WaitResponse, extra map[string]any) (*mcp.CallToolResult, error) {
	if !resp.OK {
		reason := resp.Reason
		if reason == "" {
			reason = "condition not met"
		}
		return errResult(errors.New(reason)), nil
	}
	out := map[string]any{"ok": true}
	if resp.Status != "" {
		out["status"] = resp.Status
	}
	if resp.ExitCode != nil {
		out["exit_code"] = *resp.ExitCode
	}
	if resp.Line != "" {
		out["line"] = resp.Line
		out["stream"] = resp.Stream
	}
	for k, v := range extra {
		out[k] = v
	}
	return textResult(out)
}

func registerWaitTools(s *server.MCPServer, rc *Client) {
	s.AddTool(mcp.NewTool("process_wait_status",
		mcp.WithDescription(`Block until a managed process reaches one of the given
statuses, or the timeout expires. Statuses: running, starting, exited, killed,
stopped. Use "running" to wait for a process to come up after start, or
"exited"/"killed" to wait for shutdown. Returns the status that matched.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
		mcp.WithArray("statuses", mcp.Required(), mcp.Description("Statuses to wait for, e.g. [\"running\"]")),
		mcp.WithString("timeout", mcp.Description("Max wait, e.g. 30s or 30 (seconds). Default 30s, capped 5m")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id := strArg(args, "process")
		statuses := strSliceArg(args, "statuses")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if len(statuses) == 0 {
			return errResult(errors.New("statuses are required")), nil
		}
		resp, err := rc.Wait(id, socket.WaitRequest{
			Condition: socket.WaitStatus,
			Statuses:  statuses,
			Timeout:   durationString(args),
		})
		if err != nil {
			return errResult(err), nil
		}
		return waitResult(resp, map[string]any{"condition": "status"})
	})

	s.AddTool(mcp.NewTool("process_wait_exit",
		mcp.WithDescription(`Block until a managed process exits, then return its
exit code (0 means success). Use this to wait for a one-shot job started with
process_start to finish. Times out with an error if the process stays alive.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
		mcp.WithString("timeout", mcp.Description("Max wait, e.g. 2m or 120 (seconds). Default 30s, capped 5m")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		resp, err := rc.Wait(id, socket.WaitRequest{
			Condition: socket.WaitExit,
			Timeout:   durationString(req.GetArguments()),
		})
		if err != nil {
			return errResult(err), nil
		}
		return waitResult(resp, map[string]any{"condition": "exit"})
	})

	s.AddTool(mcp.NewTool("process_wait_log",
		mcp.WithDescription(`Block until a line matching the pattern appears in the
process output, or the timeout expires. The pattern is a regular expression
(plain text also works). Pass since (a Unix ms cursor) to ignore lines already
consumed by a previous read - this makes the wait atomic. Returns the matching
line and its stream.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Regular expression to match in the output")),
		mcp.WithInteger("since", mcp.Description("Only match lines with timestamp greater than this Unix ms cursor")),
		mcp.WithString("timeout", mcp.Description("Max wait, e.g. 30s or 30 (seconds). Default 30s, capped 5m")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id := strArg(args, "process")
		pattern := strArg(args, "pattern")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if pattern == "" {
			return errResult(errors.New("pattern is required")), nil
		}
		resp, err := rc.Wait(id, socket.WaitRequest{
			Condition: socket.WaitLog,
			Pattern:   pattern,
			Since:     int64(intArg(args, "since")),
			Timeout:   durationString(args),
		})
		if err != nil {
			return errResult(err), nil
		}
		return waitResult(resp, map[string]any{"condition": "log", "pattern": pattern})
	})

	s.AddTool(mcp.NewTool("process_wait_port",
		mcp.WithDescription(`Block until the process is listening on the given TCP
port. The check is by process pid, so a port held by an unrelated process does
not count. Fails fast if the process exits while waiting.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
		mcp.WithInteger("port", mcp.Required(), mcp.Description("TCP port to wait for")),
		mcp.WithString("timeout", mcp.Description("Max wait, e.g. 30s or 30 (seconds). Default 30s, capped 5m")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id := strArg(args, "process")
		port := intArg(args, "port")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		if port <= 0 {
			return errResult(errors.New("port is required")), nil
		}
		resp, err := rc.Wait(id, socket.WaitRequest{
			Condition: socket.WaitPort,
			Port:      port,
			Timeout:   durationString(args),
		})
		if err != nil {
			return errResult(err), nil
		}
		return waitResult(resp, map[string]any{"condition": "port", "port": port})
	})

	s.AddTool(mcp.NewTool("process_wait_url",
		mcp.WithDescription(`Block until an HTTP endpoint responds (any status below
500, i.e. the server is up). Use as a readiness probe, e.g.
process_wait_url url="http://localhost:8080/health". Polls every second by
default.`),
		mcp.WithString("url", mcp.Required(), mcp.Description("HTTP URL to probe")),
		mcp.WithString("timeout", mcp.Description("Max wait, e.g. 30s or 30 (seconds). Default 30s, capped 5m")),
		mcp.WithString("interval", mcp.Description("Poll interval, e.g. 500ms (default 1s)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		url := strArg(args, "url")
		if url == "" {
			return errResult(errors.New("url is required")), nil
		}
		resp, err := rc.WaitHTTP(socket.WaitHTTPRequest{
			URL:      url,
			Timeout:  durationString(args),
			Interval: strArg(args, "interval"),
		})
		if err != nil {
			return errResult(err), nil
		}
		return waitResult(resp, map[string]any{"condition": "http", "url": url})
	})

	s.AddTool(mcp.NewTool("sleep",
		mcp.WithDescription(`Wait for a fixed interval. Use sparingly: prefer
process_wait_status / process_wait_log / process_wait_port / process_wait_url
which block until the actual condition instead of guessing a delay.`),
		mcp.WithString("duration", mcp.Required(), mcp.Description("Interval, e.g. 5s or 5 (seconds). Max 5m")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		d := durationArg(req.GetArguments(), "duration")
		if d <= 0 {
			return errResult(errors.New("duration is required")), nil
		}
		if d > 5*time.Minute {
			d = 5 * time.Minute
		}
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return errResult(ctx.Err()), nil
		}
		return textResult(map[string]any{"slept": d.String()})
	})
}

// durationString renders the timeout argument in the form the daemon expects
// ("30s"). Empty means "use the daemon default".
func durationString(args map[string]any) string {
	v := strArg(args, "timeout")
	if v == "" {
		return ""
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d.String()
	}
	return v + "s"
}
