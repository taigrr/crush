package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		stdout  string
		stderr  string
		execErr error
		want    string
	}{
		{
			name:   "stdout only",
			stdout: "hello",
			want:   "hello",
		},
		{
			name:   "stderr only has no leading newline",
			stderr: "boom",
			want:   "boom",
		},
		{
			name:   "both outputs separated by blank line",
			stdout: "out",
			stderr: "err",
			want:   "out\n\nerr",
		},
		{
			name:    "execErr surfaces when stderr empty",
			stdout:  "out",
			execErr: errors.New("kaboom"),
			want:    "out\nkaboom\nExit code 1",
		},
		{
			name: "empty everything",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatOutput(tc.stdout, tc.stderr, tc.execErr)
			if got != tc.want {
				t.Fatalf("formatOutput = %q, want %q", got, tc.want)
			}
			if strings.HasPrefix(got, "\n") {
				t.Fatalf("output should not start with a newline: %q", got)
			}
		})
	}
}

func TestTruncateOutput(t *testing.T) {
	t.Parallel()

	short := "small output"
	if got := TruncateOutput(short); got != short {
		t.Fatalf("short output changed: %q", got)
	}

	big := strings.Repeat("a\n", MaxOutputLength)
	got := TruncateOutput(big)
	if len(got) >= len(big) {
		t.Fatal("output was not truncated")
	}
	if !strings.Contains(got, "lines truncated") {
		t.Fatalf("missing truncation marker: %q", got[:80])
	}
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	cases := map[string]int{
		"":           0,
		"one":        1,
		"one\ntwo":   2,
		"a\nb\nc":    3,
		"trailing\n": 2,
	}
	for in, want := range cases {
		if got := countLines(in); got != want {
			t.Fatalf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
}
