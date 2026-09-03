package mcp

import (
	"encoding/json"
	"time"

	"runx/internal/daemon"
	"runx/internal/process"
	"runx/internal/socket"
)

// Client is a typed wrapper over the runx daemon socket. Every call bootstraps
// the daemon on first use, so the MCP server works out of the box.
type Client struct {
	sock *socket.Client
}

// NewClient creates a client. The daemon is spawned lazily on the first call.
func NewClient() *Client {
	return &Client{sock: socket.NewClient()}
}

func (c *Client) ensureDaemon() error {
	if !daemon.IsRunning() {
		return daemon.Spawn()
	}
	return nil
}

// call runs fn against the socket and unmarshals the response into v.
func (c *Client) call(fn func() ([]byte, error), v any) error {
	if err := c.ensureDaemon(); err != nil {
		return err
	}
	b, err := fn()
	if err != nil {
		return err
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(b, v)
}

func (c *Client) Start(name, cwd, command string, args, env []string, ttl, idle time.Duration, ephemeral bool) (*process.ProcessInfo, error) {
	var p process.ProcessInfo
	err := c.call(func() ([]byte, error) {
		return c.sock.StartProcess(name, cwd, command, args, env, ttl, ephemeral, idle)
	}, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) List() ([]process.ProcessInfo, error) {
	var procs []process.ProcessInfo
	err := c.call(c.sock.GetProcesses, &procs)
	return procs, err
}

func (c *Client) Get(id string) (*process.ProcessInfo, error) {
	var p process.ProcessInfo
	err := c.call(func() ([]byte, error) { return c.sock.GetProcess(id) }, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) Stop(id string) error {
	return c.call(func() ([]byte, error) { return c.sock.StopProcess(id) }, nil)
}

func (c *Client) Kill(id string) error {
	return c.call(func() ([]byte, error) { return c.sock.KillProcess(id) }, nil)
}

func (c *Client) Restart(id string) error {
	return c.call(func() ([]byte, error) { return c.sock.RestartProcess(id) }, nil)
}

func (c *Client) Exec(id string, args []string) (string, error) {
	var out struct {
		Output string `json:"output"`
	}
	err := c.call(func() ([]byte, error) { return c.sock.ExecProcess(id, args) }, &out)
	if err != nil {
		return "", err
	}
	return out.Output, nil
}

func (c *Client) Logs(id string, f socket.LogFilter) ([]process.LogEntry, error) {
	var entries []process.LogEntry
	err := c.call(func() ([]byte, error) { return c.sock.LogsFiltered(id, f) }, &entries)
	return entries, err
}

func (c *Client) Events(since int64, proc string) ([]process.Event, error) {
	var events []process.Event
	err := c.call(func() ([]byte, error) { return c.sock.GetEventsSince(since, proc) }, &events)
	return events, err
}

func (c *Client) Metrics(id string) (*process.MetricsSnapshot, error) {
	var m process.MetricsSnapshot
	err := c.call(func() ([]byte, error) { return c.sock.GetMetrics(id) }, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Detail returns the full on-demand process snapshot (tree, ports,
// connections, fd paths, exe/cwd, tree metrics).
func (c *Client) Detail(id string) (*process.ProcessDetail, error) {
	var d process.ProcessDetail
	err := c.call(func() ([]byte, error) { return c.sock.GetProcessDetail(id) }, &d)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (c *Client) Wait(id string, req socket.WaitRequest) (*socket.WaitResponse, error) {
	var resp socket.WaitResponse
	err := c.call(func() ([]byte, error) { return c.sock.Wait(id, req) }, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) WaitHTTP(req socket.WaitHTTPRequest) (*socket.WaitResponse, error) {
	var resp socket.WaitResponse
	err := c.call(func() ([]byte, error) { return c.sock.WaitHTTP(req) }, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GC() ([]string, error) {
	return c.gc(false)
}

// GCForce removes every finished process record, used by the process_gc tool
// where the agent explicitly asks for a clean slate.
func (c *Client) GCForce() ([]string, error) {
	return c.gc(true)
}

func (c *Client) gc(force bool) ([]string, error) {
	var out struct {
		Removed []string `json:"removed"`
	}
	err := c.call(func() ([]byte, error) {
		if force {
			return c.sock.GCForce()
		}
		return c.sock.GC()
	}, &out)
	if err != nil {
		return nil, err
	}
	return out.Removed, nil
}
