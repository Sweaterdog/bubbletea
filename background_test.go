package tea

import (
	"bytes"
	"reflect"
	"testing"
)

func TestDarkFromCOLORFGBG(t *testing.T) {
	cases := []struct {
		value    string
		dark, ok bool
	}{
		{"15;0", true, true},
		{"0;15", false, true},
		{"0;default;15", false, true},
		{"7;0", true, true},
		{"0;7", false, true},
		// termenv's own test, lightness under one half: grey (8) already counts as light.
		{"0;8", false, true},
		{"15;4", true, true},
		{" 15;0 ", true, true},
		{"", false, false},
		{"default", false, false},
		{"15;default", false, false},
		{"15;300", false, false},
		{"15;-1", false, false},
	}
	for _, c := range cases {
		dark, ok := darkFromCOLORFGBG(c.value)
		if dark != c.dark || ok != c.ok {
			t.Errorf("darkFromCOLORFGBG(%q) = %v, %v; want %v, %v", c.value, dark, ok, c.dark, c.ok)
		}
	}
}

func TestDarkFromOSC11Reply(t *testing.T) {
	cases := []struct {
		reply    string
		dark, ok bool
	}{
		{"\x1b]11;rgb:0000/0000/0000\x1b\\", true, true},
		{"\x1b]11;rgb:ffff/ffff/ffff\x07", false, true},
		{"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\", true, true},
		{"\x1b]11;rgb:fdfd/f6f6/e3e3\x1b\\", false, true},
		{"\x1b]11;rgba:ffff/ffff/ffff/ffff\x1b\\", false, true},
		{"\x1b]11;rgb:fa/fa/fa\x07", false, true},
		{"\x1b]11;rgb:f/f/f\x07", false, true},
		{"noise\x1b]11;rgb:0000/0000/0000\x07", true, true},
		{"\x1b]11;rgb:0000/0000/0000", false, false},
		{"\x1b]11;?\x07", false, false},
		{"\x1b]11;rgb:zz/zz/zz\x07", false, false},
		{"\x1b]11;rgb:00000/0/0\x07", false, false},
		{"\x1b]11;rgb:0/0\x07", false, false},
		{"", false, false},
	}
	for _, c := range cases {
		dark, ok := darkFromOSC11Reply([]byte(c.reply))
		if dark != c.dark || ok != c.ok {
			t.Errorf("darkFromOSC11Reply(%q) = %v, %v; want %v, %v", c.reply, dark, ok, c.dark, c.ok)
		}
	}
}

func TestBackgroundRepliesWaitsForTheCursorReport(t *testing.T) {
	osc := "\x1b]11;rgb:ffff/ffff/ffff\x1b\\"
	cases := []struct {
		got     string
		wantOSC string
		done    bool
	}{
		{osc + "\x1b[24;80R", osc, true},
		{"\x1b[24;80R", "", true},
		{osc, osc, false},
		{osc + "\x1b[24;8", osc + "\x1b[24;8", false},
		{"\x1b[R", "\x1b[R", false},
	}
	for _, c := range cases {
		gotOSC, done := backgroundReplies([]byte(c.got))
		if string(gotOSC) != c.wantOSC || done != c.done {
			t.Errorf("backgroundReplies(%q) = %q, %v; want %q, %v", c.got, gotOSC, done, c.wantOSC, c.done)
		}
	}
}

func TestDetectOSCReply(t *testing.T) {
	reply := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"
	cases := []struct {
		in    string
		more  bool
		found bool
		width int
	}{
		{reply, false, true, len(reply)},
		{reply + "hi", false, true, len(reply)},
		{"\x1b]11;rgb:ffff/ffff/ffff\x07", false, true, len("\x1b]11;rgb:ffff/ffff/ffff\x07")},
		// Alt+] and Alt+] then a letter are keys, not replies.
		{"\x1b]", false, false, 0},
		{"\x1b]a", false, false, 0},
		{"\x1b]1a", false, false, 0},
		// Cut off: more is asked for only when the read buffer was full.
		{"\x1b]11;rgb:1e1e", true, true, 0},
		{"\x1b]11;rgb:1e1e", false, false, 0},
		{"\x1b]11;rgb:1e1e\x1b", true, true, 0},
		{"\x1b]11", true, true, 0},
		// A control character inside is not part of a reply.
		{"\x1b]11;rgb\r:1e1e\x07", false, false, 0},
	}
	for _, c := range cases {
		found, width, msg := detectOSCReply([]byte(c.in), c.more)
		if found != c.found || width != c.width {
			t.Errorf("detectOSCReply(%q, %v) = %v, %d; want %v, %d", c.in, c.more, found, width, c.found, c.width)
			continue
		}
		if found && width > 0 {
			if _, ok := msg.(unknownOSCSequenceMsg); !ok {
				t.Errorf("detectOSCReply(%q) gave %T, want unknownOSCSequenceMsg", c.in, msg)
			}
		}
	}
}

// TestLateBackgroundReplyIsNotTyped: a terminal that answers the background query after it
// stopped waiting sends the reply to the Program's input. It must not reach the model as keys
// (Alt+], then "11;rgb:...", then Alt+\ typed into whatever has focus).
func TestLateBackgroundReplyIsNotTyped(t *testing.T) {
	input := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b[24;1Rhi"
	msgs := testReadInputs(t, bytes.NewReader([]byte(input)))
	var keys []Msg
	for _, m := range msgs {
		if k, ok := m.(KeyMsg); ok {
			keys = append(keys, k)
		}
	}
	want := []Msg{KeyMsg{Type: KeyRunes, Runes: []rune("hi")}}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys from a late reply: got %#v, want only the typed %#v (all messages: %#v)", keys, want, msgs)
	}
}

// TestAltBracketStillAKey: the reply detection leaves Alt+] alone.
func TestAltBracketStillAKey(t *testing.T) {
	msgs := testReadInputs(t, bytes.NewReader([]byte("\x1b]")))
	want := []Msg{KeyMsg{Type: KeyRunes, Runes: []rune("]"), Alt: true}}
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("Alt+]: got %#v, want %#v", msgs, want)
	}
}
