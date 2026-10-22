package tea

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/muesli/termenv"
)

// Answering "is the terminal's background dark?" before a Program starts.
//
// Upstream asks Lip Gloss in init, and Lip Gloss asks the terminal through termenv: an OSC 11
// query, then a wait of up to termenv.OSCTimeout (5 s) for each byte of the reply. A terminal
// that never replies (a pseudo-terminal with nothing behind it, as under script(1) without a
// terminal, some CI runners and multiplexer setups) held every program that imports Bubble Tea
// for 5 s before main ran, whether or not it ever drew an adaptive colour.
//
// Here the question is answered in three steps:
//   - COLORFGBG, when the terminal sets it, answers without asking (it is the same hint termenv
//     falls back to);
//   - otherwise the terminal is asked once, followed by a cursor position request that every
//     terminal answers, so a terminal without OSC 11 support is known at once; and the whole
//     exchange waits at most backgroundQueryTimeout;
//   - with no answer, the background is taken as dark, which is what termenv reports then.
//
// The answer is given to Lip Gloss (SetHasDarkBackground), so it never queries the terminal
// itself once a Program owns the input. A reply that arrives after the wait is read by the
// Program's input reader, which drops it (detectOSCReply in key_sequences.go) instead of
// typing it out as keys.

// darkFromCOLORFGBG reads the background from COLORFGBG ("fg;bg" or "fg;default;bg", colour
// numbers), the way termenv does when a terminal does not answer. ok is false when the variable
// is unset or its last field is not a colour number.
func darkFromCOLORFGBG(value string) (dark, ok bool) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, ";") {
		return false, false
	}
	fields := strings.Split(value, ";")
	n, err := strconv.Atoi(strings.TrimSpace(fields[len(fields)-1]))
	if err != nil || n < 0 || n > 255 {
		return false, false
	}
	return colorIsDark(termenv.ANSI256Color(n)), true
}

// darkFromOSC11Reply reads a terminal's answer to OSC 11 ("ESC ] 11 ; rgb:RRRR/GGGG/BBBB"
// ended by BEL or ST; some terminals say rgba and add an alpha channel). ok is false when the
// reply is not one.
func darkFromOSC11Reply(reply []byte) (dark, ok bool) {
	start := bytes.Index(reply, []byte("\x1b]11;"))
	if start < 0 {
		return false, false
	}
	body := reply[start+len("\x1b]11;"):]
	if end := bytes.IndexAny(body, "\x07\x1b"); end >= 0 {
		body = body[:end]
	} else {
		return false, false // not finished
	}
	spec := string(body)
	switch {
	case strings.HasPrefix(spec, "rgb:"):
		spec = strings.TrimPrefix(spec, "rgb:")
	case strings.HasPrefix(spec, "rgba:"):
		spec = strings.TrimPrefix(spec, "rgba:")
	default:
		return false, false
	}
	channels := strings.Split(spec, "/")
	if len(channels) < 3 {
		return false, false
	}
	hex := "#"
	for _, channel := range channels[:3] {
		// One to four hex digits per channel; the top byte is what a colour needs, as termenv
		// takes it.
		if len(channel) == 0 || len(channel) > 4 {
			return false, false
		}
		if _, err := strconv.ParseUint(channel, 16, 16); err != nil {
			return false, false
		}
		if len(channel) == 1 {
			channel += channel
		}
		hex += channel[:2]
	}
	return colorIsDark(termenv.RGBColor(hex)), true
}

// colorIsDark is termenv's own test (Output.HasDarkBackground): lightness under one half.
func colorIsDark(c termenv.Color) bool {
	_, _, l := termenv.ConvertToRGB(c).Hsl()
	return l < 0.5 //nolint:mnd
}

// backgroundReplies is what the terminal sent back to the background query so far: whether the
// cursor position report (the last answer, sent by every terminal) has arrived, and the OSC 11
// answer before it, when there is one.
func backgroundReplies(got []byte) (osc []byte, done bool) {
	// The cursor position report is "ESC [ row ; col R".
	for i := 0; i+1 < len(got); i++ {
		if got[i] != '\x1b' || got[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(got) && (got[j] >= '0' && got[j] <= '9' || got[j] == ';') {
			j++
		}
		if j < len(got) && got[j] == 'R' && j > i+2 {
			return got[:i], true
		}
	}
	return got, false
}
