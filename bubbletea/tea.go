// Package bubbletea provides middleware for serving bubbletea apps over SSH.
package bubbletea

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/muesli/termenv"
)

// BubbleTeaHandler is the function Bubble Tea apps implement to hook into the
// SSH Middleware. This will create a new tea.Program for every connection and
// start it with the tea.ProgramOptions returned.
//
// Deprecated: use Handler instead.
type BubbleTeaHandler = Handler // nolint: revive

// Handler is the function Bubble Tea apps implement to hook into the
// SSH Middleware. This will create a new tea.Program for every connection and
// start it with the tea.ProgramOptions returned.
type Handler func(ssh.Session) (tea.Model, []tea.ProgramOption)

// ProgramHandler is the function Bubble Tea apps implement to hook into the SSH
// Middleware. This should return a new tea.Program. This handler is different
// from the default handler in that it returns a tea.Program instead of
// (tea.Model, tea.ProgramOptions).
//
// Make sure to set the tea.WithInput and tea.WithOutput to the ssh.Session
// otherwise the program will not function properly.
type ProgramHandler func(ssh.Session) *tea.Program

// Middleware takes a Handler and hooks the input and output for the
// ssh.Session into the tea.Program.
//
// It also captures window resize events and sends them to the tea.Program
// as tea.WindowSizeMsgs.
func Middleware(bth Handler) wish.Middleware {
	return MiddlewareWithProgramHandler(newDefaultProgramHandler(bth), termenv.Ascii)
}

// MiddlewareWithColorProfile allows you to specify the minimum number of colors
// this program needs to work properly.
//
// If the client's color profile has less colors than p, p will be forced.
// Use with caution.
func MiddlewareWithColorProfile(bth Handler, p termenv.Profile) wish.Middleware {
	return MiddlewareWithProgramHandler(newDefaultProgramHandler(bth), p)
}

// MiddlewareWithProgramHandler allows you to specify the ProgramHandler to be
// able to access the underlying tea.Program, and the minimum supported color
// profile.
//
// This is useful for creating custom middlewares that need access to
// tea.Program for instance to use p.Send() to send messages to tea.Program.
//
// Make sure to set the tea.WithInput and tea.WithOutput to the ssh.Session
// otherwise the program will not function properly. The recommended way
// of doing so is by using MakeOptions.
//
// If the client's color profile has less colors than p, p will be forced.
// Use with caution.
func MiddlewareWithProgramHandler(bth ProgramHandler, p termenv.Profile) wish.Middleware {
	return func(h ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			s.Context().SetValue(minColorProfileKey, p)
			_, windowChanges, ok := s.Pty()
			if !ok {
				wish.Fatalln(s, "no active terminal, skipping")
				return
			}
			p := bth(s)
			if p == nil {
				h(s)
				return
			}
			ctx, cancel := context.WithCancel(s.Context())
			go func() {
				for {
					select {
					case <-ctx.Done():
						p.Quit()
						return
					case w := <-windowChanges:
						p.Send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
					}
				}
			}()
			if _, err := p.Run(); err != nil {
				log.Error("app exit with error", "error", err)
			}
			// p.Kill() will force kill the program if it's still running,
			// and restore the terminal to its original state in case of a
			// tui crash
			p.Kill()
			cancel()
			h(s)
		}
	}
}

var minColorProfileKey struct{}

var profileNames = [4]string{"TrueColor", "ANSI256", "ANSI", "Ascii"}

// MakeRenderer returns a lipgloss renderer for the current session.
// This function handle PTYs as well, and should be used to style your application.
func MakeRenderer(s ssh.Session) *lipgloss.Renderer {
	cp, ok := s.Context().Value(minColorProfileKey).(termenv.Profile)
	if !ok {
		cp = termenv.Ascii
	}
	r := newRenderer(s)
	if r.ColorProfile() > cp {
		wish.Printf(s, "Warning: Client's terminal is %q, forcing %q\r\n", profileNames[r.ColorProfile()], profileNames[cp])
		r.SetColorProfile(cp)
	}
	return r
}

// MakeOptions returns the tea.WithInput and tea.WithOutput program options
// taking into account possible Emulated or Allocated PTYs.
func MakeOptions(s ssh.Session) []tea.ProgramOption {
	return makeOpts(s)
}

type sshEnviron []string

var _ termenv.Environ = sshEnviron(nil)

// Environ implements termenv.Environ.
func (e sshEnviron) Environ() []string {
	return e
}

// Getenv implements termenv.Environ.
func (e sshEnviron) Getenv(k string) string {
	for _, v := range e {
		if strings.HasPrefix(v, k+"=") {
			return v[len(k)+1:]
		}
	}
	return ""
}

func newDefaultProgramHandler(bth Handler) ProgramHandler {
	return func(s ssh.Session) *tea.Program {
		m, opts := bth(s)
		if m == nil {
			return nil
		}
		return tea.NewProgram(m, append(opts, makeOpts(s)...)...)
	}
}
