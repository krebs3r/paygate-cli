package ui

import (
	"errors"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ErrAborted is returned when the user cancels a prompt with ctrl+c / esc.
var ErrAborted = errors.New("cancelled")

// FormTheme is the huh theme in the Paygate palette.
func FormTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = t.Focused.Base.BorderForeground(ColAccent)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(ColText).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(ColText).Bold(true).MarginBottom(1)
	t.Focused.Description = t.Focused.Description.Foreground(ColMuted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(ColDanger)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(ColDanger)
	t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(ColAccent2).SetString("▌ ")
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(ColAccent2)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(ColAccent2)
	t.Focused.Option = t.Focused.Option.Foreground(ColMuted2)
	t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(ColAccent2).SetString("▌ ")
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(ColSelectionFg).Bold(true)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(ColSuccess).SetString("● ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(ColMuted).SetString("○ ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(ColMuted2)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("#ffffff")).Background(ColAccent).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(ColMuted2).Background(colNeutralBg)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(ColAccent2)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(ColMuted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(ColAccent)
	t.Focused.TextInput.Text = t.Focused.TextInput.Text.Foreground(ColText)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = t.Blurred.Title.Foreground(ColMuted2).Bold(false)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	t.Help.ShortKey = t.Help.ShortKey.Foreground(ColMuted2)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(ColMuted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(ColMuted)
	return t
}

// RunForm runs a huh form with the CLI theme and maps aborts to ErrAborted.
func RunForm(groups ...*huh.Group) error {
	// Replace huh's default program options to drop focus reporting, which
	// the CLI does not need and some web terminals handle poorly.
	err := huh.NewForm(groups...).
		WithProgramOptions(tea.WithOutput(os.Stderr)).
		WithTheme(FormTheme()).
		WithShowHelp(true).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// Confirm asks a yes/no question.
func Confirm(title, description string, def bool) (bool, error) {
	v := def
	err := RunForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Description(description).Affirmative("Yes").Negative("No").Value(&v),
	))
	return v, err
}
