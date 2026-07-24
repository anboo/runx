package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) logsView() string {
	var b strings.Builder

	title := SectionTitleStyle.Render(" Logs ")
	b.WriteString(title)

	statusLine := ""
	if m.followLogs {
		statusLine = DimStyle.Render(" [FOLLOW]")
	}
	b.WriteString(statusLine)
	b.WriteString("\n\n")

	if len(m.logLines) == 0 {
		b.WriteString(DimStyle.Render("No logs. Select a process from Dashboard tab and press Enter.\n"))
		return b.String()
	}

	start := 0
	if m.followLogs && len(m.logLines) > m.height-10 {
		start = len(m.logLines) - (m.height - 10)
		if start < 0 {
			start = 0
		}
	}

	for i, entry := range m.logLines {
		if i < start {
			continue
		}
		prefix := ""
		color := lipgloss.Color("#AAA")
		if entry.Stream == "stderr" {
			prefix = DimStyle.Render("ERR ")
			color = lipgloss.Color("#FF6347")
		} else {
			prefix = DimStyle.Render("    ")
		}
		lineColor := lipgloss.NewStyle().Foreground(color)
		b.WriteString(prefix + lineColor.Render(entry.Line) + "\n")
	}

	return b.String()
}

func (m *Model) metricsView() string {
	var b strings.Builder

	title := SectionTitleStyle.Render(" Metrics ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if m.selectedProcID == "" {
		b.WriteString(DimStyle.Render("Select a process from Dashboard and press M.\n"))
		return b.String()
	}

	met := m.metrics
	if met == nil {
		b.WriteString(DimStyle.Render("Waiting for metrics...\n"))
		return b.String()
	}

	graphW := m.width - 20
	if graphW < 20 {
		graphW = 20
	}
	if graphW > 60 {
		graphW = 60
	}

	cpuGraph := renderLineGraph(m.cpuHistory, graphW, 8, 100.0)
	memVals := make([]float64, len(m.memHistory))
	for i, v := range m.memHistory {
		memVals[i] = v / 1024 / 1024
	}
	memGraph := renderLineGraph(memVals, graphW, 8, 0)
	netVals := make([]float64, len(m.netHistory))
	for i, v := range m.netHistory {
		netVals[i] = v / 1024
	}
	netGraph := renderLineGraph(netVals, graphW, 6, 0)

	b.WriteString("CPU\n")
	b.WriteString(cpuGraph)
	b.WriteString(fmt.Sprintf("     %.1f%%\n\n", met.CPU))

	b.WriteString("MEM\n")
	b.WriteString(memGraph)
	b.WriteString(fmt.Sprintf("     %.0fMB\n\n", float64(met.Memory)/1024/1024))

	b.WriteString("NET\n")
	b.WriteString(netGraph)
	b.WriteString(fmt.Sprintf("     %.1fKB/s\n\n", float64(met.NetRX)/1024))

	sep := lipgloss.NewStyle().Foreground(lipgloss.Color("#555")).Render(strings.Repeat("-", graphW+10))
	b.WriteString(sep + "\n")

	fields := []struct {
		label string
		value string
	}{
		{"RSS", fmt.Sprintf("%.0fMB", float64(met.RSS)/1024/1024)},
		{"VMEM", fmt.Sprintf("%.0fMB", float64(met.VMem)/1024/1024)},
		{"Threads", fmt.Sprintf("%d", met.Threads)},
		{"FD", fmt.Sprintf("%d", met.FDCount)},
		{"TX", fmt.Sprintf("%.1fKB", float64(met.NetTX)/1024)},
		{"Disk R", fmt.Sprintf("%.1fMB", float64(met.DiskRead)/1024/1024)},
		{"Disk W", fmt.Sprintf("%.1fMB", float64(met.DiskWrite)/1024/1024)},
		{"IOWait", fmt.Sprintf("%.1f%%", met.IOWait)},
	}

	for _, f := range fields {
		line := fmt.Sprintf(" %-8s %s\n", f.label+":", f.value)
		b.WriteString(line)
	}

	return b.String()
}

func (m *Model) eventsView() string {
	var b strings.Builder

	title := SectionTitleStyle.Render(" Events ")
	b.WriteString(title)
	b.WriteString("\n\n")

	if len(m.events) == 0 {
		b.WriteString(DimStyle.Render("No events yet.\n"))
		return b.String()
	}

	for _, e := range m.events {
		color := lipgloss.Color("#AAA")
		symbol := " "
		switch e.Type {
		case "started":
			color = lipgloss.Color("#32CD32")
			symbol = ">"
		case "stopped", "exited", "killed":
			color = lipgloss.Color("#FF6347")
			symbol = "X"
		case "restarted":
			color = lipgloss.Color("#FFD700")
			symbol = "R"
		}
		style := lipgloss.NewStyle().Foreground(color)
		t := e.Time.Format("15:04:05")
		line := fmt.Sprintf("[%s] %s %-15s %s\n", t, style.Render(symbol), e.Type, e.Message)
		b.WriteString(line)
	}

	return b.String()
}

func renderLineGraph(values []float64, width int, height int, fixedMax float64) string {
	if len(values) == 0 {
		return ""
	}

	maxVal := fixedMax
	if maxVal <= 0 {
		for _, v := range values {
			if v > maxVal {
				maxVal = v
			}
		}
	}
	if maxVal <= 0 {
		maxVal = 1
	}

	step := float64(len(values)) / float64(width)
	if step < 1 {
		step = 1
	}

	block := " ▁▂▃▄▅▆▇█"
	var result strings.Builder

	for row := height; row >= 0; row-- {
		threshold := float64(row) / float64(height) * maxVal

		label := fmt.Sprintf("%4.0f ", threshold)
		if row == height {
			label = fmt.Sprintf("%4.0f ", maxVal)
		} else if row == 0 {
			label = "    0 "
		} else if row == height/2 {
			label = fmt.Sprintf("%4.0f ", maxVal/2)
		} else {
			label = "     "
		}
		result.WriteString(label)

		for i := 0; i < width; i++ {
			idx := int(float64(i) * step)
			if idx >= len(values) {
				idx = len(values) - 1
			}
			v := values[idx]
			if v < 0 {
				v = 0
			}
			pct := v / maxVal
			if pct > 1 {
				pct = 1
			}
			bi := int(pct * float64(len(block)-1))
			if bi >= len(block) {
				bi = len(block) - 1
			}
			if float64(row) <= pct*float64(height) {
				result.WriteString(string(block[bi]))
			} else {
				result.WriteString(" ")
			}
		}
		result.WriteString("\n")
	}

	return result.String()
}
