package console

import (
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("32"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("31"))
)

type Printer struct {
	stream io.Writer
	indent string
}

// NewPrinter creates a new Printer instance with the specified output stream.
func NewPrinter(stream io.Writer) *Printer {

	os.Setenv("CLICOLOR_FORCE", "1")

	return &Printer{
		stream: stream,
		indent: "  ",
	}
}

func (p *Printer) Info(emoji string, format string, a ...any) {
	prefix := p.indent + withEmoji(emoji)
	_, _ = fmt.Fprintf(p.stream, prefix+format+"\n", a...)
}

func (p *Printer) Success(emoji string, format string, a ...any) {
	format = p.indent + withEmoji(emoji) + format
	_, _ = fmt.Fprintln(p.stream, successStyle.Render(fmt.Sprintf(format, a...)))
}

func (p *Printer) Warn(emoji string, format string, a ...any) {
	format = p.indent + withEmoji(emoji) + format
	_, _ = fmt.Fprintln(p.stream, warnStyle.Render(fmt.Sprintf(format, a...)))
}

func (p *Printer) Error(emoji string, format string, a ...any) {
	format = p.indent + withEmoji(emoji) + format
	_, _ = fmt.Fprintln(p.stream, errorStyle.Render(fmt.Sprintf(format, a...)))
}

func withEmoji(emoji string) string {
	if emoji == "" {
		return ""
	}
	return emoji + " "
}
