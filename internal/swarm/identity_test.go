package swarm

import "testing"

func TestParseAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		ok    bool
		color string
		anim  string
		hash  string
		sid   string
	}{
		{"aliceblue-tiger", true, "aliceblue", "tiger", "", ""},
		{"aliceblue-tiger-1a2b", true, "aliceblue", "tiger", "1a2b", ""},
		{"AliceBlue-Tiger-1A2B", true, "aliceblue", "tiger", "1a2b", ""},
		// Hyphenated animal name, no shorthash.
		{"red-polar-bear", true, "red", "polar-bear", "", ""},
		// Hyphenated animal with shorthash.
		{"red-polar-bear-abcd", true, "red", "polar-bear", "abcd", ""},
		// Long color name shouldn't be mistaken for a UUID (regression).
		{"lightgoldenrodyellow-polar-bear-abcd", true, "lightgoldenrodyellow", "polar-bear", "abcd", ""},
		{"mediumaquamarine-saltwater-crocodile", true, "mediumaquamarine", "saltwater-crocodile", "", ""},
		// Canonical UUID.
		{"123e4567-e89b-12d3-a456-426614174000", true, "", "", "", "123e4567-e89b-12d3-a456-426614174000"},
		// Bare 32-hex.
		{"0123456789abcdef0123456789abcdef", true, "", "", "", "0123456789abcdef0123456789abcdef"},
		// Non-hex 4-char tail is NOT a shorthash.
		{"red-polar-bear-zzzz", true, "red", "polar-bear-zzzz", "", ""},
		// Malformed.
		{"", false, "", "", "", ""},
		{"onlyone", false, "", "", "", ""},
	}
	for _, c := range cases {
		got, ok := ParseAddress(c.in)
		if ok != c.ok {
			t.Errorf("ParseAddress(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Color != c.color || got.Animal != c.anim || got.ShortHash != c.hash || got.SessionID != c.sid {
			t.Errorf("ParseAddress(%q) = %+v", c.in, got)
		}
	}
}

func TestAssignStable(t *testing.T) {
	t.Parallel()
	cfg := Default()
	id := "123e4567-e89b-12d3-a456-426614174000"
	a := Assign(id, cfg)
	b := Assign(id, cfg)
	if a != b {
		t.Fatalf("Assign not deterministic: %+v vs %+v", a, b)
	}
	if a.Color == "" || a.Animal == "" {
		t.Fatalf("Assign returned empty identity: %+v", a)
	}
}

func TestShortHash(t *testing.T) {
	t.Parallel()
	if got := ShortHash("abcdefghij"); got != "ghij" {
		t.Errorf("ShortHash long got %q", got)
	}
	if got := ShortHash("XY"); got != "xy" {
		t.Errorf("ShortHash short got %q", got)
	}
}
