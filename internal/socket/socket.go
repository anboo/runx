package socket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	s.mux.HandleFunc("/health", s.handleHealth)
}

func (s *Server) Start() error {
	os.Remove(s.sock)

	listener, err := net.Listen("unix", s.sock)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	if err := os.Chmod(s.sock, 0777); err != nil {
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
	if id == "" {
		writeError(w, 400, "process id required")
		return
	}

	switch r.Method {
	case "GET":
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

	n := 100
	if r.URL.Query().Get("n") != "" {
		if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil {
			n = v
		}
	}

	logs, err := s.pm.Logs(id, n)
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
	events := s.pm.Events()
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

	go process.CollectMetrics(proc)

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

	return io.ReadAll(resp.Body)
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

func (c *Client) GetLogs(id string, n int) ([]byte, error) {
	path := "/logs/" + id
	if n > 0 {
		path += "?n=" + strconv.Itoa(n)
	}
	return c.doRequest("GET", path, nil)
}

func (c *Client) GetMetrics(id string) ([]byte, error) {
	return c.doRequest("GET", "/metrics/"+id, nil)
}

func (c *Client) GetEvents() ([]byte, error) {
	return c.doRequest("GET", "/events", nil)
}

func (c *Client) StartProcess(name, cwd, command string, args, env []string, ttl time.Duration, ephemeral bool, idle time.Duration) ([]byte, error) {
	body := map[string]interface{}{
		"name":    name,
		"cwd":     cwd,
		"command": command,
		"args":    args,
		"env":     env,
		"ttl":     ttl,
		"ephemeral": ephemeral,
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

func (c *Client) Health() ([]byte, error) {
	return c.doRequest("GET", "/health", nil)
}

func (c *Client) Ping() error {
	_, err := c.doRequest("GET", "/health", nil)
	return err
}
