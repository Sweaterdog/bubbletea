//go:build linux
// +build linux

package tea

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal: the test plays the terminal on master, and the query runs
// on slave as it would on a program's own terminal.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		m.Close()
		t.Skipf("cannot unlock a pseudo-terminal: %v", err)
	}
	n, err := unix.IoctlGetUint32(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		m.Close()
		t.Skipf("cannot name a pseudo-terminal: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("cannot open a pseudo-terminal: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		m.Close()
	})
	return m, s
}

// terminal plays a terminal on master: it reads the query and, when answer is not empty,
// answers it after delay.
func terminal(t *testing.T, master *os.File, answer string, delay time.Duration) <-chan string {
	t.Helper()
	asked := make(chan string, 1)
	go func() {
		want := "\x1b]11;?\x1b\\\x1b[6n"
		var got []byte
		buf := make([]byte, 64)
		for len(got) < len(want) {
			n, err := master.Read(buf)
			if err != nil {
				break
			}
			got = append(got, buf[:n]...)
		}
		asked <- string(got)
		if answer != "" {
			time.Sleep(delay)
			_, _ = master.WriteString(answer)
		}
	}()
	return asked
}

func TestBackgroundQueryReadsTheAnswer(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer string
		dark   bool
	}{
		{"light, ST", "\x1b]11;rgb:ffff/ffff/ffff\x1b\\\x1b[5;1R", false},
		{"dark, BEL", "\x1b]11;rgb:1e1e/1e1e/1e1e\x07\x1b[5;1R", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			master, slave := openPTY(t)
			asked := terminal(t, master, c.answer, 0)
			start := time.Now()
			dark, ok := queryDarkBackground(slave, 2*time.Second)
			if !ok || dark != c.dark {
				t.Fatalf("query = %v, %v; want %v, true", dark, ok, c.dark)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("an answering terminal took %v; the query should stop as soon as it answers", elapsed)
			}
			if q := <-asked; q != "\x1b]11;?\x1b\\\x1b[6n" {
				t.Errorf("query sent %q", q)
			}
		})
	}
}

// TestBackgroundQueryWithoutOSC11: a terminal that does not know OSC 11 still reports the
// cursor, so the query knows at once there is no answer.
func TestBackgroundQueryWithoutOSC11(t *testing.T) {
	master, slave := openPTY(t)
	terminal(t, master, "\x1b[5;1R", 0)
	start := time.Now()
	if _, ok := queryDarkBackground(slave, 2*time.Second); ok {
		t.Fatal("a terminal without OSC 11 gave an answer")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v; the cursor report should end the wait", elapsed)
	}
}

// TestBackgroundQueryDoesNotWaitLongOnASilentTerminal is the 5 s stall: nothing answers.
func TestBackgroundQueryDoesNotWaitLongOnASilentTerminal(t *testing.T) {
	master, slave := openPTY(t)
	terminal(t, master, "", 0)
	start := time.Now()
	if _, ok := queryDarkBackground(slave, backgroundQueryTimeout); ok {
		t.Fatal("a silent terminal gave an answer")
	}
	elapsed := time.Since(start)
	// Poll counts in whole milliseconds, so a little under the limit is the limit.
	if elapsed < backgroundQueryTimeout-20*time.Millisecond {
		t.Errorf("gave up after %v, before its %v", elapsed, backgroundQueryTimeout)
	}
	if elapsed > backgroundQueryTimeout+500*time.Millisecond {
		t.Errorf("waited %v for a terminal that never answers (limit %v)", elapsed, backgroundQueryTimeout)
	}
}

// TestBackgroundQueryRestoresTheTerminal: echo and line editing come back as they were.
func TestBackgroundQueryRestoresTheTerminal(t *testing.T) {
	master, slave := openPTY(t)
	before, err := unix.IoctlGetTermios(int(slave.Fd()), ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, master, "\x1b[5;1R", 0)
	queryDarkBackground(slave, time.Second)
	after, err := unix.IoctlGetTermios(int(slave.Fd()), ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag != after.Lflag || before.Cc != after.Cc {
		t.Errorf("terminal settings changed: lflag %#x -> %#x", before.Lflag, after.Lflag)
	}
}

// TestLateAnswerReachesTheInputAsNoKeys: an answer that comes after the wait stays on the
// terminal for the Program's input reader, which drops it.
func TestLateAnswerReachesTheInputAsNoKeys(t *testing.T) {
	master, slave := openPTY(t)
	late := "\x1b]11;rgb:ffff/ffff/ffff\x1b\\\x1b[5;1R"
	terminal(t, master, late, 400*time.Millisecond)
	if _, ok := queryDarkBackground(slave, 100*time.Millisecond); ok {
		t.Fatal("answered before the terminal did")
	}
	// The Program starts: raw input, as initInput sets it. Then the answer comes, and the
	// person types.
	state, err := term.MakeRaw(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(slave.Fd(), state) //nolint:errcheck
	time.Sleep(500 * time.Millisecond)
	_, _ = master.WriteString("ok")
	time.Sleep(100 * time.Millisecond)
	// Fd puts the file back in blocking mode, so it is taken once, before the switch.
	fd := int(slave.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	var pending []byte
	buf := make([]byte, 256)
	for {
		n, err := unix.Read(fd, buf)
		if n <= 0 || err != nil {
			break
		}
		pending = append(pending, buf[:n]...)
	}
	if !bytes.Contains(pending, []byte("\x1b]11;rgb:")) {
		t.Fatalf("expected the late answer on the input, got %q", pending)
	}
	var keys []Msg
	for _, m := range testReadInputs(t, bytes.NewReader(pending)) {
		if k, ok := m.(KeyMsg); ok {
			keys = append(keys, k)
		}
	}
	want := []Msg{KeyMsg{Type: KeyRunes, Runes: []rune("ok")}}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys: got %#v, want only %#v", keys, want)
	}
}

// TestDetectDarkBackgroundOrder: COLORFGBG answers without touching the terminal; anything
// that is not a terminal, or a multiplexer, is not asked and reads as dark.
func TestDetectDarkBackgroundOrder(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	master, slave := openPTY(t)
	_ = master
	start := time.Now()
	if detectDarkBackground(slave, env(map[string]string{"COLORFGBG": "0;15"}), 5*time.Second) {
		t.Error("COLORFGBG 0;15 is a light background")
	}
	if !detectDarkBackground(slave, env(map[string]string{"COLORFGBG": "15;0"}), 5*time.Second) {
		t.Error("COLORFGBG 15;0 is a dark background")
	}
	if !detectDarkBackground(slave, env(map[string]string{"TERM": "tmux-256color"}), 5*time.Second) {
		t.Error("an unasked multiplexer reads as dark")
	}
	if !detectDarkBackground(slave, env(map[string]string{"CI": "true"}), 5*time.Second) {
		t.Error("CI reads as dark")
	}
	// The test does not own the pseudo-terminal as its controlling terminal, so it is not in
	// its foreground and is not asked either.
	if !detectDarkBackground(slave, env(nil), 5*time.Second) {
		t.Error("a terminal this process is not in the foreground of reads as dark")
	}
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipeR.Close()
	defer pipeW.Close()
	if !detectDarkBackground(pipeW, env(nil), 5*time.Second) {
		t.Error("a pipe reads as dark")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("none of these should wait; took %v", elapsed)
	}
}
