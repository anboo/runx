package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"runx/internal/daemon"
	"runx/internal/socket"
	"golang.org/x/term"
)

type CLI struct{}

func New() *CLI {
	return &CLI{}
}

func (c *CLI) ensureDaemon() error {
	if !daemon.IsRunning() {
		return daemon.Spawn()
	}
	return nil
}

func (c *CLI) Start(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	name := args[0]
	cwd := "."
	ttl := time.Duration(0)
	ephemeral := false
	idle := time.Duration(0)
	var cmdArgs []string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			if i+1 < len(args) {
				cwd = args[i+1]
				i++
			}
		case "--ttl":
			if i+1 < len(args) {
				d, err := time.ParseDuration(args[i+1])
				if err == nil {
					ttl = d
				}
				i++
			}
		case "--ephemeral":
			ephemeral = true
		case "--idle":
			if i+1 < len(args) {
				d, err := time.ParseDuration(args[i+1])
				if err == nil {
					idle = d
				}
				i++
			}
		case "--":
			cmdArgs = args[i+1:]
			goto done
		}
	}
done:

	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: command required after --")
		os.Exit(1)
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	client := socket.NewClient()

	procsRaw, _ := client.GetProcesses()
	var procs []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if json.Unmarshal(procsRaw, &procs) == nil {
		for _, p := range procs {
			if p.Name == name && p.Status == "running" {
				fmt.Fprintf(os.Stderr, "killing existing %s (%s)\n", p.ID, p.Status)
				client.KillProcess(p.ID)
			}
		}
	}

	resp, err := client.StartProcess(name, absCwd, cmdArgs[0], cmdArgs[1:], nil, ttl, ephemeral, idle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(resp))
}

func (c *CLI) Stop(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}
	client := socket.NewClient()
	resp, err := client.StopProcess(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(resp))
}

func (c *CLI) Restart(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}
	client := socket.NewClient()
	resp, err := client.RestartProcess(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(resp))
}

func (c *CLI) Kill(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}
	client := socket.NewClient()
	resp, err := client.KillProcess(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(resp))
}

func (c *CLI) PS(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	jsonFlag := false
	wideFlag := false
	for _, a := range args {
		if a == "--json" {
			jsonFlag = true
		}
		if a == "-w" || a == "--wide" {
			wideFlag = true
		}
	}

	client := socket.NewClient()
	resp, err := client.GetProcesses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonFlag {
		var pretty bytes.Buffer
		json.Indent(&pretty, resp, "", "  ")
		fmt.Println(pretty.String())
		return
	}

	var procs []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		PID     int    `json:"pid"`
		Status  string `json:"status"`
		CPU     float64 `json:"cpu"`
		Memory  uint64 `json:"memory"`
		Uptime  string `json:"uptime"`
		Command string `json:"command"`
	}
	json.Unmarshal(resp, &procs)

	if len(procs) == 0 {
		fmt.Println("No processes")
		return
	}

	if wideFlag {
		fmt.Printf("%-20s %-20s %-8s %-10s %-8s %-12s %s\n", "ID", "Name", "PID", "Status", "CPU%", "Memory", "Command")
		fmt.Println(strings.Repeat("-", 100))
		for _, p := range procs {
			memStr := fmt.Sprintf("%.1fMB", float64(p.Memory)/1024/1024)
			fmt.Printf("%-20s %-20s %-8d %-10s %-8.1f %-12s %s\n", p.ID, p.Name, p.PID, p.Status, p.CPU, memStr, p.Command)
		}
	} else {
		fmt.Printf("%-8s %-20s %-8s %-10s %s\n", "PID", "Name", "Status", "CPU%", "Command")
		fmt.Println(strings.Repeat("-", 70))
		for _, p := range procs {
			fmt.Printf("%-8d %-20s %-8s %-10.1f %s\n", p.PID, p.Name, p.Status, p.CPU, truncate(p.Command, 30))
		}
	}
}

func (c *CLI) Logs(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	jsonFlag := false
	n := 50
	var id string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonFlag = true
		case "-n":
			if i+1 < len(args) {
				v, _ := strconv.Atoi(args[i+1])
				if v > 0 {
					n = v
				}
				i++
			}
		default:
			if id == "" {
				id = args[i]
			}
		}
	}

	if id == "" {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}

	client := socket.NewClient()
	resp, err := client.GetLogs(id, n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonFlag {
		var pretty bytes.Buffer
		json.Indent(&pretty, resp, "", "  ")
		fmt.Println(pretty.String())
		return
	}

	var logEntries []struct {
		Stream string `json:"stream"`
		Line   string `json:"line"`
	}
	json.Unmarshal(resp, &logEntries)

	for _, entry := range logEntries {
		prefix := ""
		if entry.Stream == "stderr" {
			prefix = "[ERR] "
		}
		fmt.Printf("%s%s\n", prefix, entry.Line)
	}
}

func (c *CLI) Metrics(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	jsonFlag := false
	var id string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonFlag = true
		default:
			if id == "" {
				id = args[i]
			}
		}
	}

	if id == "" {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}

	client := socket.NewClient()
	resp, err := client.GetMetrics(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if string(resp) == "null" {
		fmt.Println("No metrics yet (process may not be running)")
		return
	}

	if jsonFlag {
		var pretty bytes.Buffer
		json.Indent(&pretty, resp, "", "  ")
		fmt.Println(pretty.String())
		return
	}

	var m struct {
		CPU      float64 `json:"cpu"`
		Memory   uint64  `json:"memory"`
		RSS      uint64  `json:"rss"`
		VMem     uint64  `json:"virtual_memory"`
		Threads  int32   `json:"threads"`
		FDCount  int     `json:"fd_count"`
		NetRX    uint64  `json:"network_rx"`
		NetTX    uint64  `json:"network_tx"`
		DiskRead uint64  `json:"disk_read"`
		DiskWrite uint64 `json:"disk_write"`
		IOWait   float64 `json:"io_wait"`
	}
	if err := json.Unmarshal(resp, &m); err != nil {
		fmt.Println(string(resp))
		return
	}

	fmt.Printf("CPU:     %.1f%%\n", m.CPU)
	fmt.Printf("Memory:  %.0fMB\n", float64(m.Memory)/1024/1024)
	fmt.Printf("RSS:     %.0fMB\n", float64(m.RSS)/1024/1024)
	fmt.Printf("VMEM:    %.0fMB\n", float64(m.VMem)/1024/1024)
	fmt.Printf("Threads: %d\n", m.Threads)
	fmt.Printf("FD:      %d\n", m.FDCount)
	fmt.Printf("Network: %.1fKB RX / %.1fKB TX\n", float64(m.NetRX)/1024, float64(m.NetTX)/1024)
	fmt.Printf("Disk:    %.1fMB R / %.1fMB W\n", float64(m.DiskRead)/1024/1024, float64(m.DiskWrite)/1024/1024)
	fmt.Printf("IO Wait: %.1f%%\n", m.IOWait)
}

func (c *CLI) Attach(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}

	client := socket.NewClient()
	resp, err := client.GetProcess(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var info struct {
		Status string `json:"status"`
	}
	json.Unmarshal(resp, &info)

	if info.Status != "running" {
		fmt.Fprintf(os.Stderr, "Process %s is not running (status: %s)\n", args[0], info.Status)
		os.Exit(1)
	}

	logResp, _ := client.GetLogs(args[0], 20)
	var logLines []struct {
		Stream string `json:"stream"`
		Line   string `json:"line"`
	}
	if json.Unmarshal(logResp, &logLines) == nil {
		for _, l := range logLines {
			if l.Stream == "stdout" {
				fmt.Println(l.Line)
			} else {
				fmt.Fprintln(os.Stderr, l.Line)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\nAttached to %s (follow mode). Press Ctrl+C to detach.\n", args[0])

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	var lastIdx int

	for {
		select {
		case <-done:
			fmt.Fprintln(os.Stderr, "\nDetached")
			return
		case <-ticker.C:
			resp, err := client.GetLogs(args[0], 100)
			if err != nil {
				continue
			}
			var lines []struct {
				Stream string `json:"stream"`
				Line   string `json:"line"`
			}
			if json.Unmarshal(resp, &lines) != nil {
				continue
			}
			for i, l := range lines {
				if i < lastIdx {
					continue
				}
				if l.Stream == "stdout" {
					fmt.Println(l.Line)
				} else {
					fmt.Fprintln(os.Stderr, l.Line)
				}
			}
			if len(lines) > 0 {
				lastIdx = len(lines)
			}
		}
	}
}

func (c *CLI) Exec(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: runx exec <id> -- <command>...")
		os.Exit(1)
	}

	id := args[0]
	var cmdArgs []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			cmdArgs = args[i+1:]
			break
		}
	}
	if len(cmdArgs) == 0 {
		cmdArgs = args[1:]
	}

	client := socket.NewClient()
	resp, err := client.ExecProcess(id, cmdArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(resp))
}

func (c *CLI) Shell(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}

	client := socket.NewClient()
	resp, err := client.GetProcess(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var procInfo struct {
		CWD  string   `json:"cwd"`
		Env  []string `json:"env"`
	}
	if err := json.Unmarshal(resp, &procInfo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command("/bin/bash")
	cmd.Dir = procInfo.CWD
	cmd.Env = append(os.Environ(), procInfo.Env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, _ := term.MakeRaw(int(os.Stdin.Fd()))
		if oldState != nil {
			defer term.Restore(int(os.Stdin.Fd()), oldState)
		}
	}

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (c *CLI) Wait(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: process id required")
		os.Exit(1)
	}

	id := args[0]
	timeout := 5 * time.Minute
	for i := 1; i < len(args); i++ {
		if args[i] == "--timeout" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err == nil {
				timeout = d
			}
			i++
		}
	}

	client := socket.NewClient()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.GetProcess(id)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var p procStatus
		if err := json.Unmarshal(resp, &p); err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if p.Status == "exited" || p.Status == "killed" || p.Status == "stopped" {
			fmt.Println("done")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "Timeout waiting for process %s\n", id)
	os.Exit(1)
}

type procStatus struct {
	Status string `json:"status"`
}

func (c *CLI) GC(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	client := socket.NewClient()
	if err := client.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("GC request sent (daemon cleans up periodically)")
}

func (c *CLI) Events(args []string) {
	if err := c.ensureDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	jsonFlag := false
	for _, a := range args {
		if a == "--json" {
			jsonFlag = true
		}
	}

	client := socket.NewClient()
	resp, err := client.GetEvents()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonFlag {
		var pretty bytes.Buffer
		json.Indent(&pretty, resp, "", "  ")
		fmt.Println(pretty.String())
		return
	}

	var events []struct {
		Type      string `json:"type"`
		ProcessID string `json:"process_id"`
		Message   string `json:"message"`
		Timestamp int64  `json:"timestamp"`
	}
	json.Unmarshal(resp, &events)

	for _, e := range events {
		t := time.UnixMilli(e.Timestamp)
		fmt.Printf("%s %-15s %s\n", t.Format("15:04:05"), e.Type, e.Message)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
