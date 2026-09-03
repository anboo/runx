package mcp

import (
	"sync"

	"github.com/mark3labs/mcp-go/server"
)

const (
	// ServerName is what MCP clients see in server info.
	ServerName = "runx"
	// ServerVersion tracks the MCP tool surface.
	ServerVersion = "0.1.0"
)

// NewServer builds the MCP server with all runx tools registered. The tools
// talk to the runx daemon over its unix socket, so processes survive MCP
// server restarts and multiple agents can share one daemon.
//
// MCP clients may fire tool calls concurrently (the SDK runs each call in its
// own goroutine). Mutating calls are serialized through a mutex so operations
// like start-vs-stop on the same process name cannot race in the daemon;
// reads and blocking waits stay concurrent.
func NewServer() *server.MCPServer {
	s := server.NewMCPServer(ServerName, ServerVersion)
	rc := NewClient()

	mutations := &sync.Mutex{}
	registerProcessTools(s, rc, mutations)
	registerObserveTools(s, rc)
	registerDetailTools(s, rc)
	registerWaitTools(s, rc)
	registerStackTools(s, rc, mutations)

	return s
}

// Serve runs the MCP server over stdio, the transport AI clients use for
// local servers.
func Serve() error {
	return server.ServeStdio(NewServer())
}
