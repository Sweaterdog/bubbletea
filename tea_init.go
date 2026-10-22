package tea

func init() {
	// XXX: This is a workaround to make assure that Lip Gloss and Termenv
	// query the terminal before any Bubble Tea Program runs and acquires the
	// terminal. Without this, Programs that use Lip Gloss/Termenv might hang
	// while waiting for a a [termenv.OSCTimeout] while querying the terminal
	// for its background/foreground colors.
	//
	// This happens because Bubble Tea acquires the terminal before termenv
	// reads any responses.
	//
	// Note that this will only affect programs running on the default IO i.e.
	// [os.Stdout] and [os.Stdin].
	//
	// The question is answered quickly and without waiting on a terminal that
	// never replies (see background.go), and the answer is handed to Lip Gloss
	// so it never asks again while a Program owns the terminal.
	//
	// This workaround will be removed in v2.
	initBackground()
}
