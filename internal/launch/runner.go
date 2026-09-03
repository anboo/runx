package launch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Starter abstracts how a managed process gets created so the same launch
// logic works both through the CLI (which talks to the daemon over the unix
// socket) and inside the desktop GUI (which owns an in-process manager).
// Start must return the process ID assigned to the process.
type Starter interface {
	Start(name, cwd, command string, args, env []string) (id string, err error)
}

// StepEvent reports pre_step progress. Output carries incremental chunks
// while the step is running and the full captured output on completion.
type StepEvent struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Status string `json:"status"` // running | done | failed | skipped
	Output string `json:"output"`
}

// RunOptions controls pre_step execution.
type RunOptions struct {
	// Ctx can be used to cancel the pre_step sequence mid-run. Optional.
	Ctx context.Context
	// Out receives pre_step output. When nil, output is discarded.
	Out io.Writer
	// OnStep is called on every state change or output chunk. Optional.
	OnStep func(StepEvent)
}

// RunPreSteps executes all pre_steps sequentially. The first failing step
// aborts the launch unless it has IgnoreErrors set.
func RunPreSteps(cfg *Config, opts RunOptions) error {
	base := opts.Ctx
	if base == nil {
		base = context.Background()
	}
	for i, step := range cfg.PreSteps {
		if err := base.Err(); err != nil {
			return fmt.Errorf("launch cancelled: %w", err)
		}
		emit := func(e StepEvent) {
			if opts.OnStep != nil {
				opts.OnStep(e)
			}
		}
		name := step.Name
		if name == "" {
			name = step.Command
		}
		emit(StepEvent{Index: i, Name: name, Status: "running"})

		cwd := cfg.ResolvePath(step.CWD)
		cmdName, cmdArgs := ResolveCommand(step.Command, step.Args)
		env := cfg.MergedEnv(step.Env, os.Environ())

		ctx, cancel := context.WithCancel(base)
		if step.Timeout != "" {
			if d, err := time.ParseDuration(step.Timeout); err == nil && d > 0 {
				ctx, cancel = context.WithTimeout(base, d)
			}
		}
		cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
		cmd.Dir = cwd
		cmd.Env = env
		cmd.Stdin = nil

		var buf stepBuffer
		w := io.Writer(&buf)
		if opts.Out != nil {
			w = io.MultiWriter(opts.Out, &buf)
		}
		cmd.Stdout = chunkWriter{w: w, emit: func(chunk string) {
			emit(StepEvent{Index: i, Name: name, Status: "running", Output: chunk})
		}}
		cmd.Stderr = cmd.Stdout

		err := cmd.Run()
		cancel()

		if err != nil {
			if step.IgnoreErrors {
				emit(StepEvent{Index: i, Name: name, Status: "skipped", Output: buf.String()})
				continue
			}
			emit(StepEvent{Index: i, Name: name, Status: "failed", Output: buf.String()})
			return fmt.Errorf("pre_step %q: %w", name, err)
		}
		emit(StepEvent{Index: i, Name: name, Status: "done", Output: buf.String()})
	}
	return nil
}

// StartedProcess records a process that the launch has started.
type StartedProcess struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// StartProcesses starts every process in the config through the Starter.
// Process names are prefixed with the config name (<cfg>.<proc>). The env
// passed to the Starter contains only config-level overrides; the backend is
// responsible for merging them onto its own process environment (like the
// daemon does for `runx start`). It returns the list of started processes;
// on error the processes started so far are left running and the error
// reports which one failed.
func StartProcesses(cfg *Config, st Starter) ([]StartedProcess, error) {
	var started []StartedProcess
	for _, p := range cfg.Processes {
		cwd := cfg.ResolvePath(p.CWD)
		cmdName, cmdArgs := ResolveCommand(p.Command, p.Args)
		env := cfg.MergedEnv(p.Env, nil)
		id, err := st.Start(cfg.ProcessName(p), cwd, cmdName, cmdArgs, env)
		if err != nil {
			return started, fmt.Errorf("start %q: %w", p.Name, err)
		}
		started = append(started, StartedProcess{Name: p.Name, ID: id})
	}
	return started, nil
}

// WaitHealthy polls the process health URL until it responds or the timeout
// expires. Responding means any status code below 500 (the server is up).
func WaitHealthy(h *HealthCheck) error {
	timeout := 30 * time.Second
	interval := time.Second
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err == nil {
			timeout = d
		}
	}
	if h.Interval != "" {
		if d, err := time.ParseDuration(h.Interval); err == nil {
			interval = d
		}
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(h.URL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("health check timeout for %s (%s)", h.URL, timeout)
}

// stepBuffer accumulates step output for the final StepEvent.
type stepBuffer struct {
	buf []byte
}

func (b *stepBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *stepBuffer) String() string {
	return string(b.buf)
}

// chunkWriter forwards every write both to the underlying writer and to the
// event callback, so the GUI can stream pre_step output live.
type chunkWriter struct {
	w    io.Writer
	emit func(chunk string)
}

func (c chunkWriter) Write(p []byte) (int, error) {
	if c.w != nil {
		c.w.Write(p)
	}
	if c.emit != nil {
		c.emit(string(p))
	}
	return len(p), nil
}
