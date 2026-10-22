//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd
// +build darwin dragonfly freebsd linux netbsd openbsd

package tea

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/unix"
)

// backgroundQueryTimeout is the longest the background query waits for the terminal. A
// terminal answers in a few milliseconds, over SSH in one round trip, and the query stops as
// soon as it has; only a terminal that never answers waits this long (termenv waits 5 s).
const backgroundQueryTimeout = 200 * time.Millisecond

// initBackground answers Lip Gloss's background question once, quickly (see background.go).
func initBackground() {
	lipgloss.SetHasDarkBackground(detectDarkBackground(os.Stdout, os.Getenv, backgroundQueryTimeout))
}

// detectDarkBackground decides whether out's terminal has a dark background: COLORFGBG first,
// then the terminal's answer within timeout, otherwise dark.
func detectDarkBackground(out *os.File, getenv func(string) string, timeout time.Duration) bool {
	if dark, ok := darkFromCOLORFGBG(getenv("COLORFGBG")); ok {
		return dark
	}
	if !mayQueryTerminal(out, getenv) {
		return true
	}
	if dark, ok := queryDarkBackground(out, timeout); ok {
		return dark
	}
	return true
}

// mayQueryTerminal applies termenv's own rules for when a terminal may be asked: out is a
// terminal, not a CI log, not a multiplexer that cannot pass the query on, and this process is
// in the terminal's foreground (a background process that reads the terminal is stopped).
func mayQueryTerminal(out *os.File, getenv func(string) string) bool {
	if out == nil || getenv("CI") != "" {
		return false
	}
	term := getenv("TERM")
	if strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "dumb") {
		return false
	}
	fd := int(out.Fd()) //nolint:gosec
	if _, err := unix.IoctlGetTermios(fd, ioctlGetTermios); err != nil {
		return false // not a terminal
	}
	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && pgrp == unix.Getpgrp()
}

// queryDarkBackground asks the terminal on tty for its background colour (OSC 11), then for
// the cursor position, which every terminal answers, and waits at most timeout for both. ok is
// false when the terminal did not say: it does not support OSC 11, or did not answer in time.
// Echo and line buffering are off while it waits, as termenv does, and restored after.
func queryDarkBackground(tty *os.File, timeout time.Duration) (dark, ok bool) {
	fd := int(tty.Fd()) //nolint:gosec
	saved, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return false, false
	}
	quiet := *saved
	quiet.Lflag &^= unix.ECHO | unix.ICANON
	quiet.Cc[unix.VMIN] = 1
	quiet.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return false, false
	}
	defer unix.IoctlSetTermios(fd, ioctlSetTermios, saved) //nolint:errcheck

	if _, err := tty.WriteString("\x1b]11;?\x1b\\\x1b[6n"); err != nil {
		return false, false
	}
	deadline := time.Now().Add(timeout)
	var got []byte
	buf := make([]byte, 256)
	for {
		if osc, done := backgroundReplies(got); done {
			return darkFromOSC11Reply(osc)
		}
		if len(got) > 1024 { //nolint:mnd
			return false, false // not an answer to this query
		}
		left := time.Until(deadline)
		if left <= 0 {
			return false, false
		}
		ready, err := waitReadable(fd, left)
		if err != nil || !ready {
			return false, false
		}
		n, err := unix.Read(fd, buf)
		if err != nil || n <= 0 {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			return false, false
		}
		got = append(got, buf[:n]...)
	}
}

// waitReadable waits up to timeout for fd to have input.
func waitReadable(fd int, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		ms := int(time.Until(deadline) / time.Millisecond)
		if ms < 0 {
			ms = 0
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} //nolint:gosec
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			if time.Now().After(deadline) {
				return false, nil
			}
			continue
		}
		if err != nil {
			return false, err //nolint:wrapcheck
		}
		return n > 0 && fds[0].Revents&unix.POLLIN != 0, nil
	}
}
