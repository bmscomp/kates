package cmd

import "github.com/charmbracelet/lipgloss"

var (
	amber = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render
	blue  = lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Render
	green = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render
	red   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render
	dim   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render
	bold  = lipgloss.NewStyle().Bold(true).Render
)
