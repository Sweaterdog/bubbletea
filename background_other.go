//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd
// +build !darwin,!dragonfly,!freebsd,!linux,!netbsd,!openbsd

package tea

import "github.com/charmbracelet/lipgloss"

// initBackground asks Lip Gloss, as upstream does. termenv does not query the terminal on
// Windows and the other platforms here (it reports a default), except Solaris and z/OS, which
// keep upstream's behaviour.
func initBackground() {
	_ = lipgloss.HasDarkBackground()
}
