package launch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.yaml")
	content := `name: dev
root: /srv/app
env:
  NODE_ENV: test
pre_steps:
  - name: db
    command: docker compose up -d
  - name: migrate
    command: make migrate
    ignore_errors: true
processes:
  - name: backend
    cwd: ./backend
    command: go
    args: [run, .]
    health:
      url: http://localhost:8080/health
  - name: frontend
    command: npm run dev
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "dev" {
		t.Errorf("name = %q, want dev", cfg.Name)
	}
	if cfg.Root != "/srv/app" {
		t.Errorf("root = %q, want /srv/app", cfg.Root)
	}
	if len(cfg.PreSteps) != 2 {
		t.Errorf("pre_steps = %d, want 2", len(cfg.PreSteps))
	}
	if len(cfg.Processes) != 2 {
		t.Errorf("processes = %d, want 2", len(cfg.Processes))
	}
	if !cfg.PreSteps[1].IgnoreErrors {
		t.Error("migrate step should have ignore_errors")
	}

	// Root defaults to the config file directory.
	path2 := filepath.Join(dir, "nested.yaml")
	if err := os.WriteFile(path2, []byte("name: nested\nprocesses:\n  - name: api\n    command: go run .\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(path2)
	if err != nil {
		t.Fatalf("Load nested: %v", err)
	}
	if cfg2.Root != dir {
		t.Errorf("root = %q, want %q", cfg2.Root, dir)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"no name", "processes:\n  - name: a\n    command: x\n"},
		{"no processes", "name: x\n"},
		{"process without command", "name: x\nprocesses:\n  - name: a\n"},
		{"duplicate process", "name: x\nprocesses:\n  - name: a\n    command: x\n  - name: a\n    command: y\n"},
		{"step without command", "name: x\nprocesses:\n  - name: a\n    command: x\npre_steps:\n  - name: s\n"},
		{"bad timeout", "name: x\nprocesses:\n  - name: a\n    command: x\npre_steps:\n  - name: s\n    command: y\n    timeout: nope\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"npm run dev", []string{"npm", "run", "dev"}},
		{"go run .", []string{"go", "run", "."}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{`docker compose -f 'compose dev.yaml' up`, []string{"docker", "compose", "-f", "compose dev.yaml", "up"}},
		{"", nil},
		{"   ", nil},
	}
	for _, tc := range cases {
		got := SplitCommand(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("SplitCommand(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitCommand(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestResolveCommand(t *testing.T) {
	bin, args := ResolveCommand("npm", []string{"run", "dev"})
	if bin != "npm" || len(args) != 2 {
		t.Errorf("explicit args: got %q %v", bin, args)
	}
	bin, args = ResolveCommand("npm run dev", nil)
	if bin != "npm" || len(args) != 2 {
		t.Errorf("full line: got %q %v", bin, args)
	}
}

func TestLoadResolvesRoot(t *testing.T) {
	dir := t.TempDir()
	// root: . must resolve to the config file dir, not the process cwd.
	path := filepath.Join(dir, "cfg.yaml")
	content := "name: app\nroot: .\nprocesses:\n  - name: api\n    cwd: ./svc\n    command: go run .\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.Root) {
		t.Errorf("root %q should be absolute", cfg.Root)
	}
	if cfg.ResolvePath("./svc") != filepath.Join(dir, "svc") {
		t.Errorf("ResolvePath = %q, want %q", cfg.ResolvePath("./svc"), filepath.Join(dir, "svc"))
	}
}

func TestProcessNameAndResolvePath(t *testing.T) {
	cfg := &Config{Name: "webapp", Root: "/srv/app"}
	if got := cfg.ProcessName(Process{Name: "backend"}); got != "webapp.backend" {
		t.Errorf("ProcessName = %q", got)
	}
	if got := cfg.ResolvePath("./backend"); got != "/srv/app/backend" {
		t.Errorf("ResolvePath = %q", got)
	}
	if got := cfg.ResolvePath(""); got != "/srv/app" {
		t.Errorf("ResolvePath empty = %q", got)
	}
	if got := cfg.ResolvePath("/abs/path"); got != "/abs/path" {
		t.Errorf("ResolvePath abs = %q", got)
	}
}

func TestMergedEnv(t *testing.T) {
	cfg := &Config{
		Name: "x",
		Env:  map[string]string{"A": "1", "B": "global"},
	}
	env := cfg.MergedEnv(map[string]string{"B": "local", "C": "3"}, []string{"INHERIT=x"})
	got := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				got[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if got["A"] != "1" || got["B"] != "local" || got["C"] != "3" || got["INHERIT"] != "x" {
		t.Errorf("MergedEnv = %v", got)
	}
}

func TestSaveAndReload(t *testing.T) {
	cfg := &Config{
		Name:      "my-app",
		Root:      "/srv",
		Env:       map[string]string{"K": "v"},
		PreSteps:  []PreStep{{Name: "db", Command: "docker compose up -d"}},
		Processes: []Process{{Name: "api", Command: "go", Args: []string{"run", "."}}},
	}
	dir := t.TempDir()
	path, err := Save(dir, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Base(path) != "my-app.yaml" {
		t.Errorf("file = %q, want my-app.yaml", filepath.Base(path))
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Name != "my-app" || len(reloaded.Processes) != 1 {
		t.Errorf("reloaded = %+v", reloaded)
	}
}
