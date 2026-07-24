package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) dashboardView() string {
	var b strings.Builder

	title := SectionTitleStyle.Render(" Processes ")
	b.WriteString(title)
	b.WriteString("\n\n")

	b.WriteString(tableHeader())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", m.width-2))
	b.WriteString("\n")

	for i, p := range m.processes {
		row := tableRow(p, i == m.selectedIdx)
		b.WriteString(row)
		b.WriteString("\n")
	}

	b.WriteString(strings.Repeat("-", m.width-2))
	b.WriteString("\n")

	procCount := len(m.processes)
	running := 0
	for _, p := range m.processes {
		if p.Status == "running" {
			running++
		}
	}
	b.WriteString(fmt.Sprintf("\n Total: %d | Running: %d | Stopped: %d\n",
		procCount, running, procCount-running))

	if m.statusMsg != "" {
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")).Bold(true)
		b.WriteString("\n" + statusStyle.Render("> " + m.statusMsg) + "\n")
	}

	return b.String()
}

func tableHeader() string {
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7B68EE"))
	w := func(s string, n int) string {
		return fmt.Sprintf("%-"+fmt.Sprintf("%d", n)+"s", s)
	}
	return style.Render(
		w("NAME", 16) + w("PID", 8) + w("STATUS", 10) +
			w("CPU%", 8) + w("MEM", 10) + w("THR", 6) + w("UPTIME", 12) + "COMMAND",
	)
}

func tableRow(p ProcessData, selected bool) string {
	w := func(s string, n int) string {
		return fmt.Sprintf("%-"+fmt.Sprintf("%d", n)+"s", s)
	}

	memStr := fmt.Sprintf("%.0fMB", float64(p.Memory)/1024/1024)

	var statusStyle lipgloss.Style
	switch p.Status {
	case "running":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#32CD32"))
	case "stopped", "exited", "killed":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6347"))
	default:
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	}

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(p.Color))
	if selected {
		nameStyle = nameStyle.Background(lipgloss.Color("#555"))
	}

	uptime := p.Uptime
	if uptime == "" {
		uptime = "-"
	}

	cmd := truncateStr(p.Command, 30)

	row := nameStyle.Render(w(p.Name, 16)) +
		w(fmt.Sprintf("%d", p.PID), 8) +
		statusStyle.Render(w(p.Status, 10)) +
		w(fmt.Sprintf("%.1f", p.CPU), 8) +
		w(memStr, 10) +
		w(fmt.Sprintf("%d", p.Threads), 6) +
		w(uptime, 12) +
		cmd

	if selected {
		row = "> " + row
	} else {
		row = "  " + row
	}

	return row
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
