package tui

import "github.com/charmbracelet/lipgloss"

var (
	DocStyle = lipgloss.NewStyle().
			Padding(0, 1, 1, 1)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF")).
			Background(lipgloss.Color("#7B68EE")).
			Bold(true).
			Padding(0, 1).
			Width(80)

	ActiveTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF")).
			Background(lipgloss.Color("#483D8B")).
			Bold(true).
			Padding(0, 2)

	InactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888")).
				Background(lipgloss.Color("#333")).
				Padding(0, 2)

	RunningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#32CD32"))

	StoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6347"))

	WarningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFD700"))

	InfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#87CEEB"))

	SelectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#555"))

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAA")).
			Background(lipgloss.Color("#222")).
			Padding(0, 1).
			Width(80)

	SectionTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#7B68EE")).
				Bold(true).
				Underline(true)
)
