package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"runx/internal/socket"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	dashboardTab tab = iota
	logsTab
	metricsTab
	eventsTab
)

type ProcessData struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	PID     int     `json:"pid"`
	Status  string  `json:"status"`
	CPU     float64 `json:"cpu"`
	Memory  uint64  `json:"memory"`
	Threads int32   `json:"threads"`
	Uptime  string  `json:"uptime"`
	Command string  `json:"command"`
	Color   string  `json:"color"`
}

type MetricsData struct {
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

type EventData struct {
	Type      string `json:"type"`
	ProcessID string `json:"process_id"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
	Time      time.Time
}

type LogData struct {
	Stream string `json:"stream"`
	Line   string `json:"line"`
	Time   time.Time
}

type tickMsg time.Time
type processesMsg []ProcessData
type metricsMsg *MetricsData
type eventsMsg []EventData
type logsMsg []LogData
type errMsg error

type Model struct {
	width         int
	height        int
	activeTab     tab
	processes     []ProcessData
	metrics       *MetricsData
	events        []EventData
	logLines      []LogData
	selectedIdx   int
	selectedProcID string
	followLogs    bool
	viewports     map[tab]viewport.Model
	cpuHistory    []float64
	memHistory    []float64
	netHistory    []float64
	ready         bool
	client        *socket.Client
	statusMsg     string
	statusClearAt time.Time
}

func NewModel() Model {
	return Model{
		activeTab:   dashboardTab,
		selectedIdx: 0,
		followLogs:  true,
		viewports:   make(map[tab]viewport.Model),
		client:      socket.NewClient(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tick(),
		fetchProcesses(m.client),
	)
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchProcesses(client *socket.Client) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.GetProcesses()
		if err != nil {
			return errMsg(fmt.Errorf("fetch processes: %w", err))
		}
		var data []ProcessData
		if err := json.Unmarshal(resp, &data); err != nil {
			return errMsg(fmt.Errorf("parse processes: %w", err))
		}
		return processesMsg(data)
	}
}

func fetchMetrics(client *socket.Client, id string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.GetMetrics(id)
		if err != nil {
			return nil
		}
		if string(resp) == "null" {
			return nil
		}
		var data MetricsData
		if err := json.Unmarshal(resp, &data); err != nil {
			return nil
		}
		return metricsMsg(&data)
	}
}

func fetchEvents(client *socket.Client) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.GetEvents()
		if err != nil {
			return nil
		}
		var data []EventData
		if err := json.Unmarshal(resp, &data); err != nil {
			return nil
		}
		for i := range data {
			data[i].Time = time.UnixMilli(data[i].Timestamp)
		}
		return eventsMsg(data)
	}
}

func fetchLogs(client *socket.Client, id string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.GetLogs(id, 500)
		if err != nil {
			return nil
		}
		var data []LogData
		if err := json.Unmarshal(resp, &data); err != nil {
			return nil
		}
		return logsMsg(data)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.ready = true
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % 4
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + 4) % 4
		case "down", "j":
			if m.activeTab == dashboardTab {
				if m.selectedIdx < len(m.processes)-1 {
					m.selectedIdx++
				}
				if m.selectedIdx < len(m.processes) {
					m.selectedProcID = m.processes[m.selectedIdx].ID
				}
			}
		case "up":
			if m.activeTab == dashboardTab {
				if m.selectedIdx > 0 {
					m.selectedIdx--
				}
				if m.selectedIdx < len(m.processes) {
					m.selectedProcID = m.processes[m.selectedIdx].ID
				}
			}
		case "enter":
			if m.activeTab == dashboardTab && m.selectedIdx < len(m.processes) {
				m.selectedProcID = m.processes[m.selectedIdx].ID
				m.activeTab = logsTab
				return m, fetchLogs(m.client, m.selectedProcID)
			}
		case "r", "R":
			if m.selectedProcID != "" {
				m.client.RestartProcess(m.selectedProcID)
				m.statusMsg = "restarted " + m.selectedProcID
				m.statusClearAt = time.Now().Add(3 * time.Second)
				return m, fetchProcesses(m.client)
			}
		case "s", "S":
			if m.selectedProcID != "" {
				m.client.StopProcess(m.selectedProcID)
				m.statusMsg = "stopped " + m.selectedProcID
				m.statusClearAt = time.Now().Add(3 * time.Second)
				return m, fetchProcesses(m.client)
			}
		case "k", "K":
			if m.selectedProcID != "" {
				m.client.KillProcess(m.selectedProcID)
				m.statusMsg = "killed " + m.selectedProcID
				m.statusClearAt = time.Now().Add(3 * time.Second)
				return m, fetchProcesses(m.client)
			}
		case "l", "L":
			if m.selectedProcID != "" {
				m.activeTab = logsTab
				return m, fetchLogs(m.client, m.selectedProcID)
			}
		case "m", "M":
			if m.selectedProcID != "" {
				m.activeTab = metricsTab
				return m, fetchMetrics(m.client, m.selectedProcID)
			}
		case "e", "E":
			m.activeTab = eventsTab
		case "d", "D":
			m.activeTab = dashboardTab
		case " ":
			m.followLogs = !m.followLogs
		case "/":
			// Search in logs - placeholder
		}

	case tickMsg:
		if !m.statusClearAt.IsZero() && time.Now().After(m.statusClearAt) {
			m.statusMsg = ""
			m.statusClearAt = time.Time{}
		}
		cmds := []tea.Cmd{tick(), fetchProcesses(m.client), fetchEvents(m.client)}
		if m.selectedProcID != "" {
			cmds = append(cmds, fetchMetrics(m.client, m.selectedProcID))
			if m.activeTab == logsTab {
				cmds = append(cmds, fetchLogs(m.client, m.selectedProcID))
			}
		}
		return m, tea.Batch(cmds...)

	case processesMsg:
		m.processes = []ProcessData(msg)

	case metricsMsg:
		if msg != nil {
			m.metrics = msg
			if m.cpuHistory == nil {
				m.cpuHistory = make([]float64, 0, 100)
				m.memHistory = make([]float64, 0, 100)
				m.netHistory = make([]float64, 0, 100)
			}
			m.cpuHistory = append(m.cpuHistory, msg.CPU)
			m.memHistory = append(m.memHistory, float64(msg.Memory))
			m.netHistory = append(m.netHistory, float64(msg.NetRX))
			if len(m.cpuHistory) > 100 {
				m.cpuHistory = m.cpuHistory[1:]
				m.memHistory = m.memHistory[1:]
				m.netHistory = m.netHistory[1:]
			}
		}

	case eventsMsg:
		m.events = []EventData(msg)
		if len(m.events) > 1000 {
			m.events = m.events[len(m.events)-1000:]
		}

	case logsMsg:
		m.logLines = []LogData(msg)

	case errMsg:
		// Silently handle errors in TUI
	}

	return m, nil
}

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(m.renderTabs())
	b.WriteString("\n")

	var content string
	switch m.activeTab {
	case dashboardTab:
		content = m.dashboardView()
	case logsTab:
		content = m.logsView()
	case metricsTab:
		content = m.metricsView()
	case eventsTab:
		content = m.eventsView()
	}

	b.WriteString(content)
	b.WriteString("\n")
	b.WriteString(m.renderFooter())

	return DocStyle.Render(b.String())
}

func (m Model) renderHeader() string {
	cpu := 0.0
	mem := uint64(0)
	if m.metrics != nil {
		cpu = m.metrics.CPU
		mem = m.metrics.Memory
	}

	info := fmt.Sprintf("runx     CPU: %.1f%%  MEM: %.0fMB  Procs: %d",
		cpu, float64(mem)/1024/1024, len(m.processes))

	return HeaderStyle.Render(info)
}

func (m Model) renderTabs() string {
	tabs := []struct {
		label string
		t     tab
	}{
		{"Dashboard", dashboardTab},
		{"Logs", logsTab},
		{"Metrics", metricsTab},
		{"Events", eventsTab},
	}

	var rendered []string
	for _, t := range tabs {
		if t.t == m.activeTab {
			rendered = append(rendered, ActiveTabStyle.Render(t.label))
		} else {
			rendered = append(rendered, InactiveTabStyle.Render(t.label))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func (m Model) renderFooter() string {
	keys := []string{
		"Tab:next",
		"Enter:logs",
		"R:restart",
		"S:stop",
		"K:kill",
		"M:metrics",
		"E:events",
		"D:dash",
		"Space:follow",
		"Q:quit",
	}

	return StatusBarStyle.Render(strings.Join(keys, " | "))
}

var DimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
