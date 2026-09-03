// Package launch defines the YAML launch configuration format for RunX.
//
// A launch config describes a full dev environment: one-shot pre_steps that
// run before anything else (docker compose up, migrations, codegen), and a
// set of long-running processes (frontend, backend) that get started
// afterwards. Both `runx up` and the desktop GUI consume this package.
package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of a launch configuration file.
type Config struct {
	// Name is the unique identifier of the config. It is required and is used
	// as a prefix for managed process names (<name>.<process>).
	Name string `yaml:"name" json:"name"`
	// Root is the base directory. Relative cwd values in pre_steps and
	// processes resolve against it. Defaults to the config file directory.
	Root string `yaml:"root,omitempty" json:"root,omitempty"`
	// Env is merged into the environment of every pre_step and process.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// PreSteps run sequentially, in order, before any process starts.
	PreSteps []PreStep `yaml:"pre_steps,omitempty" json:"pre_steps,omitempty"`
	// Processes are long-running commands managed by RunX. They all start in
	// parallel after the pre_steps complete.
	Processes []Process `yaml:"processes" json:"processes"`
}

// PreStep is a one-shot command that must finish before the next step or
// process starts. Typical use: docker compose up, db migrations, codegen.
type PreStep struct {
	// Name is a short label used in logs and the GUI. Optional.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// CWD is the working directory, relative to Config.Root.
	CWD string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	// Command is either the full command line ("docker compose up -d") or the
	// executable name when Args is set ("docker").
	Command string `yaml:"command" json:"command"`
	// Args are the executable arguments. Optional when Command is a full line.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Env is merged on top of the config-level Env for this step only.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// IgnoreErrors keeps the launch going even if this step exits non-zero.
	IgnoreErrors bool `yaml:"ignore_errors,omitempty" json:"ignore_errors,omitempty"`
	// Timeout is a duration string ("60s", "5m"). Zero means no timeout.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Process is a long-running process managed by RunX (frontend, backend, ...).
type Process struct {
	// Name is the process label. The managed process name becomes
	// <Config.Name>.<Process.Name> so a whole stack can be stopped at once.
	Name string `yaml:"name" json:"name"`
	// CWD is the working directory, relative to Config.Root.
	CWD string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	// Command is either the full command line ("npm run dev") or the
	// executable name when Args is set ("npm").
	Command string `yaml:"command" json:"command"`
	// Args are the executable arguments. Optional when Command is a full line.
	Args []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Env is merged on top of the config-level Env for this process only.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Health is an optional readiness check. When set, the launch waits until
	// the URL responds before declaring the process ready.
	Health *HealthCheck `yaml:"health,omitempty" json:"health,omitempty"`
}

// HealthCheck polls an HTTP endpoint until it responds.
type HealthCheck struct {
	// URL to poll, e.g. http://localhost:8080/health.
	URL string `yaml:"url" json:"url"`
	// Timeout is a duration string; default 30s.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Interval is a duration string between polls; default 1s.
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"`
}

// Load reads and parses a launch config file. Root defaults to the directory
// of the file, so relative cwd values just work.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Dir(path)
	} else if !filepath.IsAbs(cfg.Root) {
		// A relative root is resolved against the config file directory, so a
		// `root: .` means "next to this file" regardless of the launch cwd.
		cfg.Root = filepath.Join(filepath.Dir(path), cfg.Root)
	}
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	cfg.Root = abs
	return cfg, nil
}

// Parse parses launch config YAML and validates it.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks that the config is usable: a name, at least one process and
// every process/step has a command.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("config name is required")
	}
	if len(c.Processes) == 0 {
		return fmt.Errorf("config %q has no processes", c.Name)
	}
	seen := map[string]bool{}
	for i := range c.Processes {
		p := &c.Processes[i]
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("process #%d: name is required", i+1)
		}
		if seen[p.Name] {
			return fmt.Errorf("process %q: duplicate name", p.Name)
		}
		seen[p.Name] = true
		if strings.TrimSpace(p.Command) == "" {
			return fmt.Errorf("process %q: command is required", p.Name)
		}
	}
	stepNames := map[string]bool{}
	for i := range c.PreSteps {
		s := &c.PreSteps[i]
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("pre_step #%d: command is required", i+1)
		}
		if s.Name != "" && stepNames[s.Name] {
			return fmt.Errorf("pre_step %q: duplicate name", s.Name)
		}
		if s.Name != "" {
			stepNames[s.Name] = true
		}
		if s.Timeout != "" {
			if _, err := time.ParseDuration(s.Timeout); err != nil {
				return fmt.Errorf("pre_step %q: bad timeout %q", s.Name, s.Timeout)
			}
		}
	}
	return nil
}

// Save writes the config as YAML. The config name becomes the file name.
func Save(dir string, cfg *Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	path := filepath.Join(dir, safeName(cfg.Name)+".yaml")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// ResolvePath joins a config-relative path onto the config root.
func (c *Config) ResolvePath(p string) string {
	if p == "" {
		return c.Root
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Root, p)
}

// MergedEnv combines config-level env with item-level env. Returns KEY=VALUE
// pairs ready for exec.Cmd.Env.
func (c *Config) MergedEnv(item map[string]string, inherit []string) []string {
	merged := map[string]string{}
	for _, kv := range inherit {
		if k, v, ok := strings.Cut(kv, "="); ok {
			merged[k] = v
		}
	}
	for k, v := range c.Env {
		merged[k] = v
	}
	for k, v := range item {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

// ResolveCommand turns a Process or PreStep into an executable and its args.
// When Args is empty, the Command line is split shell-style ("npm run dev" ->
// ["npm", "run", "dev"]).
func ResolveCommand(command string, args []string) (string, []string) {
	if len(args) > 0 {
		return command, args
	}
	parts := SplitCommand(command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// ProcessName returns the managed process name: <config>.<process>.
func (c *Config) ProcessName(p Process) string {
	return c.Name + "." + p.Name
}

func safeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// SplitCommand splits a command line into tokens, honoring single and double
// quotes. Backslash escaping is not supported.
func SplitCommand(s string) []string {
	var tokens []string
	var cur strings.Builder
	var quote byte
	inToken := false
	for i := 0; i < len(s); i++ {
		r := s[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteByte(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteByte(r)
			inToken = true
		}
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
