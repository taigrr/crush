package voice

import (
	"fmt"
	"os"
)

// MicCaptureSubcommand is the hidden argv[1] consumers re-exec themselves
// with to capture microphone audio in a short-lived helper process on
// macOS. Intercepted via [MaybeRunCaptureSubprocess] at the very top of
// main, before any TUI init.
const MicCaptureSubcommand = "__mic-capture"

// MaybeRunCaptureSubprocess returns a non-nil exit code when this
// process was re-exec'd as the hidden mic-capture helper.
func MaybeRunCaptureSubprocess() *int {
	if !IsCaptureSubcommand(os.Args) {
		return nil
	}
	code := runCaptureChildCLI(os.Args[2:])
	return &code
}

// IsCaptureSubcommand reports whether argv (including argv[0]) invokes
// the hidden mic-capture helper.
func IsCaptureSubcommand(argv []string) bool {
	return len(argv) > 1 && argv[1] == MicCaptureSubcommand
}

func runCaptureChildCLI(_ []string) int {
	fmt.Fprintln(os.Stdout, "ERR mic-capture helper unavailable in this build")
	return 2
}
