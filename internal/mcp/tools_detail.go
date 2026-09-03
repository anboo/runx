package mcp

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerDetailTools(s *server.MCPServer, rc *Client) {
	s.AddTool(mcp.NewTool("process_ports",
		mcp.WithDescription(`List the TCP/UDP ports a process tree is listening on
and its active network connections (grouped by remote address and state).
The check covers the whole process tree, so a `+"`go run .`"+` wrapper reports
its real child binary. Useful to find "what port is my service on" or spot
stuck connections (many ESTABLISHED to one remote).`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		d, err := rc.Detail(id)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]any{
			"process":     d.ProcessID,
			"tree":        d.Tree,
			"ports":       d.Ports,
			"connections": d.Connections,
		})
	})

	s.AddTool(mcp.NewTool("process_detail",
		mcp.WithDescription(`Full on-demand snapshot of a process: process tree
(pid/ppid/state/cmd for the wrapper and every child), listening ports,
grouped network connections, open file descriptor paths, real executable and
working directory, and resource metrics aggregated over the whole tree
(cpu, rss, swap, threads, fds, read/write bytes, cpu times).`),
		mcp.WithString("process", mcp.Required(), mcp.Description("Process id or name")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := strArg(req.GetArguments(), "process")
		if id == "" {
			return errResult(errors.New("process is required")), nil
		}
		d, err := rc.Detail(id)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(d)
	})
}