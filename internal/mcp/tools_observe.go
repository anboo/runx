package mcp

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"runx/internal/socket"
)

func registerObserveTools(s *server.MCPServer, rc *Client) {
	s.AddTool(mcp.NewTool("process_logs",
		mcp.WithDescription(`Read output of a managed process (stdout and stderr,
merged and sorted by time). Supports incremental reads: pass since (a Unix
millisecond cursor from the previous call) to get only new lines. grep is a
regular expression matched against the line text.

The daemon keeps the last 10k lines per stream in memory, so no log files are
involved.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
		mcp.WithInteger("lines", mcp.Description("Max lines to return (default 100, capped by the ring buffer)")),
		mcp.WithInteger("since", mcp.Description("Only lines with timestamp greater than this Unix ms cursor")),
		mcp.WithString("stream", mcp.Description("Filter by stream: stdout or stderr (default: both)")),
		mcp.WithString("grep", mcp.Description("Regular expression to match against line text")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		id := strArg(args, "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		entries, err := rc.Logs(id, socket.LogFilter{
			Since:  int64(intArg(args, "since")),
			Stream: strArg(args, "stream"),
			Grep:   strArg(args, "grep"),
			N:      intArg(args, "lines"),
		})
		if err != nil {
			return errResult(err), nil
		}
		if len(entries) == 0 {
			return textResult([]any{})
		}
		return textResult(entries)
	})

	s.AddTool(mcp.NewTool("process_events",
		mcp.WithDescription(`Read the lifecycle event timeline (started, stopped,
restarted, exited, killed). Supports incremental reads: pass since (a Unix
millisecond cursor) to get only events newer than the last check. Optionally
filter by process. Events never rotate away faster than 5k entries.`),
		mcp.WithInteger("since", mcp.Description("Only events with timestamp greater than this Unix ms cursor")),
		mcp.WithString("process", mcp.Description("Filter by process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		events, err := rc.Events(int64(intArg(args, "since")), strArg(args, "process"))
		if err != nil {
			return errResult(err), nil
		}
		if len(events) == 0 {
			return textResult([]any{})
		}
		return textResult(events)
	})

	s.AddTool(mcp.NewTool("process_metrics",
		mcp.WithDescription(`Latest resource snapshot of a managed process: cpu
percent, memory, rss, vmem, threads, fd count, network rx/tx, disk read/write,
io wait. Polled by the daemon every 2 seconds.`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		m, err := rc.Metrics(id)
		if err != nil {
			return errResult(err), nil
		}
		if m.Timestamp == 0 {
			return textResult(map[string]string{"process": id, "note": "no metrics yet (process may not be running)"})
		}
		return textResult(m)
	})
}
