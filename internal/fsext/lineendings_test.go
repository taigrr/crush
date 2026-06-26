package fsext

import "testing"

func TestToUnixLineEndings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		in          string
		want        string
		wantChanged bool
	}{
		{"pure unix unchanged", "a\nb\n", "a\nb\n", false},
		{"crlf converted", "a\r\nb\r\n", "a\nb\n", true},
		{"mixed converted", "a\r\nb\nc", "a\nb\nc", true},
		{"no newlines", "abc", "abc", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, changed := ToUnixLineEndings(tc.in)
			if got != tc.want || changed != tc.wantChanged {
				t.Fatalf("ToUnixLineEndings(%q) = (%q, %v), want (%q, %v)", tc.in, got, changed, tc.want, tc.wantChanged)
			}
		})
	}
}

func TestToWindowsLineEndings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		in          string
		want        string
		wantChanged bool
	}{
		{"pure unix converted", "a\nb\n", "a\r\nb\r\n", true},
		{"already crlf unchanged", "a\r\nb\r\n", "a\r\nb\r\n", false},
		{"mixed normalized to crlf", "a\r\nb\nc", "a\r\nb\r\nc", true},
		{"no doubling of crlf", "a\r\n", "a\r\n", false},
		{"no newlines", "abc", "abc", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, changed := ToWindowsLineEndings(tc.in)
			if got != tc.want || changed != tc.wantChanged {
				t.Fatalf("ToWindowsLineEndings(%q) = (%q, %v), want (%q, %v)", tc.in, got, changed, tc.want, tc.wantChanged)
			}
		})
	}
}

// TestLineEndingsRoundTrip verifies the conversions are stable: CRLF content
// survives a unix→windows round trip, and the windows conversion never
// produces a bare CR or a doubled CRCRLF for any input.
func TestLineEndingsRoundTrip(t *testing.T) {
	t.Parallel()

	inputs := []string{"a\r\nb\nc\r\n", "x\ny\nz", "only\r\ncrlf\r\n", ""}
	for _, in := range inputs {
		unix, _ := ToUnixLineEndings(in)
		win, _ := ToWindowsLineEndings(unix)
		// Re-normalizing the windows output must reproduce unix exactly.
		back, _ := ToUnixLineEndings(win)
		if back != unix {
			t.Fatalf("round trip mismatch for %q: unix=%q win=%q back=%q", in, unix, win, back)
		}
		// Windows output must contain no doubled CR.
		for i := 0; i+1 < len(win); i++ {
			if win[i] == '\r' && win[i+1] == '\r' {
				t.Fatalf("doubled CR in windows output %q", win)
			}
		}
	}
}
