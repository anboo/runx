package socket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"runx/internal/process"
	"runx/internal/session"
)

type Server struct {
	pm    *process.Manager
	sm    *session.Manager
	sock  string
	mux   *http.ServeMux
	ready chan struct{}
}

func NewServer(pm *process.Manager, sm *session.Manager) *Server {
	s := &Server{
		pm:    pm,
		sm:    sm,
		sock:  sockPath(),
		ready: make(chan struct{}),
	}
	s.mux = http.NewServeMux()
	s.registerRoutes()
	return s
}

func sockPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".runx")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "runx.sock")
}

func SockPath() string {
	return sockPath()
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/sessions", s.handleSessions)
	s.mux.HandleFunc("/processes", s.handleProcesses)
	s.mux.HandleFunc("/process/", s.handleProcess)
	s.mux.HandleFunc("/logs/", s.handleLogs)
	s.mux.HandleFunc("/metrics/", s.handleMetrics)
	s.mux.HandleFunc("/events", s.handleEvents)
	s.mux.HandleFunc("/start", s.handleStart)
	s.mux.HandleFunc("/stop/", s.handleStop)
	s.mux.HandleFunc("/restart/", s.handleRestart)
	s.mux.HandleFunc("/kill/", s.handleKill)
	s.mux.HandleFunc("/exec/", s.handleExec)
	s.mux.HandleFunc("/gc", s.handleGC)
	s.mux.HandleFunc("/wait/", s.handleWait)
	s.mux.HandleFunc("/wait/http", s.handleWaitHTTP)
	s.mux.HandleFunc("/health/", s.handleSetHealth)
	s.mux.HandleFunc("/health", s.handleHealth)
}

func (s *Server) Start() error {
	os.Remove(s.sock)

	listener, err := net.Listen("unix", s.sock)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// Best effort: on Windows chmod on a unix socket is not supported and
	// would fail the daemon startup, so treat it as advisory there.
	if err := os.Chmod(s.sock, 0777); err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("chmod: %w", err)
	}

	close(s.ready)

	return http.Serve(listener, s.mux)
}

func (s *Server) WaitReady() {
	<-s.ready
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

// handleSetHealth attaches a periodic health probe to a process.
// Body: {"url": "...", "timeout": "2s", "interval": "1s"}
func (s *Server) handleSetHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/health/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "bad request")
		return
	}

	var req struct {
		URL      string `json:"url"`
		Timeout  string `json:"timeout"`
		Interval string `json:"interval"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}

	hc := &process.HealthCheck{URL: req.URL}
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			hc.Timeout = d
		}
	}
	if req.Interval != "" {
		if d, err := time.ParseDuration(req.Interval); err == nil {
			hc.Interval = d
		}
	}

	if err := s.pm.SetHealth(id, hc); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "health monitoring enabled", "id": id, "url": req.URL})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		sessions := s.sm.List()
		writeJSON(w, sessions)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (s *Server) handleProcesses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		procs := s.pm.List()
		writeJSON(w, procs)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (s *Server) handleProcess(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/process/")
	// /process/<id>/detail returns the full on-demand snapshot.
	detail := strings.HasSuffix(id, "/detail")
	id = strings.TrimSuffix(id, "/detail")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	switch r.Method {
	case "GET":
		if detail {
			d, err := s.pm.Detail(id)
			if err != nil {
				writeError(w, 404, err.Error())
				return
			}
			writeJSON(w, d)
			return
		}
		proc, err := s.pm.Get(id)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, proc)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/logs/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	q := process.LogQuery{N: 100}
	query := r.URL.Query()
	if v := query.Get("n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			q.N = n
		}
	}
	if v := query.Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.Since = n
		}
	}
	q.Stream = query.Get("stream")
	q.Grep = query.Get("grep")

	logs, err := s.pm.LogsFiltered(id, q)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, logs)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/metrics/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	metrics, err := s.pm.Metrics(id)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, metrics)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			since = n
		}
	}
	events := s.pm.EventsSince(since, r.URL.Query().Get("process"))
	writeJSON(w, events)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "bad request")
		return
	}

	var req struct {
		Name        string        `json:"name"`
		CWD         string        `json:"cwd"`
		Command     string        `json:"command"`
		Args        []string      `json:"args"`
		Env         []string      `json:"env"`
		TTL         time.Duration `json:"ttl"`
		Ephemeral   bool          `json:"ephemeral"`
		IdleTimeout time.Duration `json:"idle_timeout"`
	}

	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}

	proc, err := s.pm.Start(req.Name, req.CWD, req.Command, req.Args, req.Env, req.TTL, req.Ephemeral, req.IdleTimeout)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	writeJSON(w, proc.ProcessInfo)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/stop/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}
	if err := s.pm.Stop(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "stopped", "id": id})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/restart/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}
	if err := s.pm.Restart(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "restarted", "id": id})
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/kill/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}
	if err := s.pm.Kill(id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "killed", "id": id})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/exec/")
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "bad request")
		return
	}

	var req struct {
		Args []string `json:"args"`
	}
	json.Unmarshal(body, &req)

	output, err := s.pm.Exec(id, req.Args)
	if err != nil {
		writeJSON(w, map[string]string{"output": output, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"output": output})
}

func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	var removed []string
	if force {
		removed = s.pm.GCForce()
	} else {
		removed = s.pm.GC()
	}
	writeJSON(w, map[string]any{"removed": removed})
}

type Client struct {
	sock string
}

func NewClient() *Client {
	return &Client{sock: sockPath()}
}

func (c *Client) doRequest(method, path string, body io.Reader) ([]byte, error) {
	conn, err := net.DialTimeout("unix", c.sock, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	req, err := http.NewRequest(method, fmt.Sprintf("http://unix%s", path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Write(conn)

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, fmt.Errorf("response: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("daemon: %s", strings.TrimSpace(string(respBody)))
	}

	return respBody, nil
}

func (c *Client) GetSessions() ([]byte, error) {
	return c.doRequest("GET", "/sessions", nil)
}

func (c *Client) GetProcesses() ([]byte, error) {
	return c.doRequest("GET", "/processes", nil)
}

func (c *Client) GetProcess(id string) ([]byte, error) {
	return c.doRequest("GET", "/process/"+id, nil)
}

// GetProcessDetail returns the full on-demand snapshot: process tree, ports,
// connections, fd paths, exe/cwd and tree-aggregated metrics.
func (c *Client) GetProcessDetail(id string) ([]byte, error) {
	return c.doRequest("GET", "/process/"+id+"/detail", nil)
}

func (c *Client) GetLogs(id string, n int) ([]byte, error) {
	return c.LogsFiltered(id, LogFilter{})
}

// LogFilter carries the optional cursor/filter params for /logs/<id>.
type LogFilter struct {
	Since  int64
	Stream string
	Grep   string
	N      int
}

// LogsFiltered reads process logs with a since cursor, stream and grep
// filters, and an optional line cap.
func (c *Client) LogsFiltered(id string, f LogFilter) ([]byte, error) {
	path := "/logs/" + id
	q := make(url.Values)
	if f.N > 0 {
		q.Set("n", strconv.Itoa(f.N))
	}
	if f.Since > 0 {
		q.Set("since", strconv.FormatInt(f.Since, 10))
	}
	if f.Stream != "" {
		q.Set("stream", f.Stream)
	}
	if f.Grep != "" {
		q.Set("grep", f.Grep)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.doRequest("GET", path, nil)
}

func (c *Client) GetMetrics(id string) ([]byte, error) {
	return c.doRequest("GET", "/metrics/"+id, nil)
}

// GetEventsSince returns lifecycle events newer than the cursor, optionally
// filtered by process id.
func (c *Client) GetEventsSince(since int64, processID string) ([]byte, error) {
	path := "/events"
	q := make(url.Values)
	if since > 0 {
		q.Set("since", strconv.FormatInt(since, 10))
	}
	if processID != "" {
		q.Set("process", processID)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.doRequest("GET", path, nil)
}

// GetEvents returns the full event log (all events, all processes).
func (c *Client) GetEvents() ([]byte, error) {
	return c.doRequest("GET", "/events", nil)
}

// Wait blocks until the requested condition for a process is met.
func (c *Client) Wait(id string, req WaitRequest) ([]byte, error) {
	b, _ := json.Marshal(req)
	return c.doRequest("POST", "/wait/"+id, bytes.NewReader(b))
}

// WaitHTTP blocks until an HTTP endpoint responds (any status below 500).
func (c *Client) WaitHTTP(req WaitHTTPRequest) ([]byte, error) {
	b, _ := json.Marshal(req)
	return c.doRequest("POST", "/wait/http", bytes.NewReader(b))
}

func (c *Client) StartProcess(name, cwd, command string, args, env []string, ttl time.Duration, ephemeral bool, idle time.Duration) ([]byte, error) {
	body := map[string]interface{}{
		"name":         name,
		"cwd":          cwd,
		"command":      command,
		"args":         args,
		"env":          env,
		"ttl":          ttl,
		"ephemeral":    ephemeral,
		"idle_timeout": idle,
	}
	b, _ := json.Marshal(body)
	return c.doRequest("POST", "/start", bytes.NewReader(b))
}

func (c *Client) StopProcess(id string) ([]byte, error) {
	return c.doRequest("POST", "/stop/"+id, nil)
}

func (c *Client) RestartProcess(id string) ([]byte, error) {
	return c.doRequest("POST", "/restart/"+id, nil)
}

func (c *Client) KillProcess(id string) ([]byte, error) {
	return c.doRequest("POST", "/kill/"+id, nil)
}

func (c *Client) ExecProcess(id string, args []string) ([]byte, error) {
	body := map[string]interface{}{"args": args}
	b, _ := json.Marshal(body)
	return c.doRequest("POST", "/exec/"+id, bytes.NewReader(b))
}

// GC requests daemon-side cleanup of finished processes and old data.
func (c *Client) GC() ([]byte, error) {
	return c.doRequest("POST", "/gc", nil)
}

// GCForce requests removal of every finished process record regardless of age.
func (c *Client) GCForce() ([]byte, error) {
	return c.doRequest("POST", "/gc?force=true", nil)
}

func (c *Client) Health() ([]byte, error) {
	return c.doRequest("GET", "/health", nil)
}

// SetHealth attaches a periodic health probe to a process.
func (c *Client) SetHealth(id string, hc process.HealthCheck) ([]byte, error) {
	body := map[string]string{
		"url":      hc.URL,
		"timeout":  hc.Timeout.String(),
		"interval": hc.Interval.String(),
	}
	b, _ := json.Marshal(body)
	return c.doRequest("POST", "/health/"+id, bytes.NewReader(b))
}

func (c *Client) Ping() error {
	_, err := c.doRequest("GET", "/health", nil)
	return err
}

// Starter adapts the socket client to launch.Starter so stack launches
// (`runx up` and the MCP stack tools) start processes inside the daemon and
// they keep running after the caller exits.
type Starter struct {
	Client *Client
}

// Start satisfies launch.Starter by creating a process through the daemon.
func (s Starter) Start(name, cwd, command string, args, env []string) (string, error) {
	resp, err := s.Client.StartProcess(name, cwd, command, args, env, 0, false, 0)
	if err != nil {
		return "", err
	}
	var info struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(resp, &info); err != nil {
		return "", fmt.Errorf("parse start response: %w", err)
	}
	return info.ID, nil
}
