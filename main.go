package main

import (
	"fmt"
	"os"

	"runx/internal/cli"
	"runx/internal/daemon"
	"runx/internal/mcp"
	"runx/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	app := cli.New()

	switch cmd {
	case "start":
		app.Start(args)

	case "stop":
		app.Stop(args)

	case "restart":
		app.Restart(args)

	case "kill":
		app.Kill(args)

	case "ps":
		app.PS(args)

	case "logs":
		app.Logs(args)

	case "metrics":
		app.Metrics(args)

	case "ports":
		app.Ports(args)

	case "events":
		app.Events(args)

	case "attach":
		app.Attach(args)

	case "exec":
		app.Exec(args)

	case "shell":
		app.Shell(args)

	case "wait":
		app.Wait(args)

	case "gc":
		app.GC(args)

	case "up":
		app.Up(args)

	case "down":
		app.Down(args)

	case "daemon":
		d := daemon.New()
		if err := d.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
			os.Exit(1)
		}

	case "mcp":
		if err := mcp.Serve(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP error: %v\n", err)
			os.Exit(1)
		}

	case "dashboard":
		if !daemon.IsRunning() {
			if err := daemon.Spawn(); err != nil {
				fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
				os.Exit(1)
			}
		}
		p := tea.NewProgram(tui.NewModel(), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`RunX - AI-first Process Runtime

Usage:
  runx <command> [options]

Commands:
  start     Start a process
  stop      Stop a process
  restart   Restart a process
  kill      Kill a process
  ps        List processes
  logs      View process logs
  metrics   View process metrics
  ports     List listening ports and connections of a process tree
  events    View process events
  attach    Attach to a process
  exec      Execute a command in process context
  shell     Open a shell in process directory
  wait      Wait for a process to finish
  gc        Clean up dead processes and old data
  up        Launch a stack from a YAML config (pre_steps + processes)
  down      Stop all processes of a launched config
  dashboard Open the terminal dashboard
  daemon    Start the background daemon
  mcp       Serve the MCP process-manager toolset over stdio

Examples:
  runx start backend --cwd ./backend -- go run .
  runx start frontend --cwd ./frontend -- npm run dev
  runx up dev.yaml
  runx down dev
  runx ps --json
  runx logs backend --json
  runx metrics backend
  runx wait backend --timeout 5m
  runx exec backend -- pwd
  runx shell backend
  runx dashboard`)
}
