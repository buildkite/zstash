package console

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
)

var (
	infoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("32"))  // blue
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // amber/orange
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("34"))  // green
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
)

type Printer struct {
	stream   io.Writer
	indent   string
	renderer *lipgloss.Renderer
}

// NewPrinter creates a new Printer instance with the specified output stream.
func NewPrinter(stream io.Writer) *Printer {
	return &Printer{
		stream:   stream,
		indent:   "  ",
		renderer: lipgloss.NewRenderer(stream),
	}
}

func (p *Printer) Info(emoji string, format string, a ...any) {
	p.printWithStyle(infoStyle, emoji, format, a...)
}

func (p *Printer) Success(emoji string, format string, a ...any) {
	p.printWithStyle(successStyle, emoji, format, a...)
}

func (p *Printer) Warn(emoji string, format string, a ...any) {
	p.printWithStyle(warnStyle, emoji, format, a...)
}

func (p *Printer) Error(emoji string, format string, a ...any) {
	p.printWithStyle(errorStyle, emoji, format, a...)
}

func (p *Printer) printWithStyle(style lipgloss.Style, emoji string, format string, a ...any) {
	formattedMessage := p.indent + withEmoji(emoji) + format
	styledOutput := p.renderer.NewStyle().Inherit(style).Render(fmt.Sprintf(formattedMessage, a...))
	_, _ = fmt.Fprintln(p.stream, styledOutput)
}

func withEmoji(emoji string) string {
	if emoji == "" {
		return ""
	}
	return emoji + " "
}
