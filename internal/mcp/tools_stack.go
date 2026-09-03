package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"runx/internal/launch"
	"runx/internal/socket"
)

func registerStackTools(s *server.MCPServer, rc *Client, mutations *sync.Mutex) {
	s.AddTool(mcp.NewTool("stack_up",
		mcp.WithDescription(`Launch a whole stack from a YAML config file: run
pre_steps sequentially first (docker compose up, migrations, codegen), then
start every process in parallel through the daemon. Process names are prefixed
with the config name (<config>.<process>). Returns the started processes and
pre_step results.`),
		mcp.WithString("config", mcp.Required(), mcp.Description("Path to the YAML launch config")),
		mcp.WithBoolean("wait_healthy", mcp.Description("Wait for each process health check before returning (default true)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		args := req.GetArguments()
		path := strArg(args, "config")
		if path == "" {
			return errResult(errors.New("config is required")), nil
		}

		cfg, err := launch.Load(path)
		if err != nil {
			return errResult(err), nil
		}

		var stepResults []launch.StepEvent
		if len(cfg.PreSteps) > 0 {
			if err := launch.RunPreSteps(cfg, launch.RunOptions{
				OnStep: func(e launch.StepEvent) {
					stepResults = append(stepResults, e)
				},
			}); err != nil {
				return errResult(err), nil
			}
		}

		started, err := launch.StartProcesses(cfg, socket.Starter{Client: rc.sock})
		if err != nil {
			return errResult(err), nil
		}

		waitHealthy := true
		if v, ok := args["wait_healthy"].(bool); ok {
			waitHealthy = v
		}

		var health []map[string]any
		if waitHealthy {
			for _, p := range cfg.Processes {
				if p.Health == nil {
					continue
				}
				name := cfg.ProcessName(p)
				if err := launch.WaitHealthy(p.Health); err != nil {
					health = append(health, map[string]any{"name": name, "healthy": false, "error": err.Error()})
				} else {
					health = append(health, map[string]any{"name": name, "healthy": true})
				}
				// Keep monitoring in the daemon after the launch returns.
				if _, err := rc.sock.SetHealth(name, *socket.HealthFromLaunch(p.Health)); err != nil {
					health = append(health, map[string]any{"name": name, "monitor_error": err.Error()})
				}
			}
		}

		return textResult(map[string]any{
			"config":    cfg.Name,
			"started":   started,
			"pre_steps": stepResults,
			"health":    health,
			"processes": len(started),
		})
	})

	s.AddTool(mcp.NewTool("stack_down",
		mcp.WithDescription(`Stop every process belonging to a launch config: the
exact config name and the <config>.<process> prefixed names. Returns how many
processes were stopped.`),
		mcp.WithString("name", mcp.Required(), mcp.Description("Config name from stack_up")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mutations.Lock()
		defer mutations.Unlock()
		name := strArg(req.GetArguments(), "name")
		if name == "" {
			return errResult(errors.New("name is required")), nil
		}

		procs, err := rc.List()
		if err != nil {
			return errResult(err), nil
		}

		prefix := name + "."
		stopped := 0
		var stoppedNames []string
		for _, p := range procs {
			if p.Name != name && !hasStringPrefix(p.Name, prefix) {
				continue
			}
			if p.Status != "running" && p.Status != "starting" {
				continue
			}
			if err := rc.Stop(p.ID); err == nil {
				stopped++
				stoppedNames = append(stoppedNames, p.Name)
			}
		}
		return textResult(map[string]any{
			"config":  name,
			"stopped": stopped,
			"names":   stoppedNames,
		})
	})
}

func hasStringPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
