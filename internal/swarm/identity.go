// Package swarm provides the cross-session "swarm" identity system:
// every session is assigned a deterministic human-readable
// (color, animal) pair derived from its UUID via colorhash+animals.
// Together with a 4-character session-id suffix these form the
// "color-animal[-shorthash]" address the swarm tool uses to route
// messages between sessions across all workspaces.
package swarm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/taigrr/animals"
	"github.com/taigrr/colorhash"
	"github.com/taigrr/simplecolorpalettes/palettes/html"
	"github.com/taigrr/simplecolorpalettes/simplecolor"
)

// Config controls how session identities are generated. It is populated
// from the active theme's swarm_palette / swarm_animals fields (with
// sensible defaults so nothing is required for the feature to work).
type Config struct {
	// Palette is the case-insensitive name of a simplecolorpalettes
	// palette to hash into. Only "html" (default) is currently
	// supported; unknown names fall back to html. Additional
	// palettes can be plumbed through by extending namedPalette.
	Palette string
	// Animals overrides the animal name list; empty means use the
	// full animals.Names() list.
	Animals []string
}

// Default returns the built-in defaults: HTML named palette (aliceblue,
// tomato, ...) and the full animals list.
func Default() Config {
	return Config{Palette: "html"}
}

// namedPalette resolves cfg.Palette to a NamedPalette. Unknown palette
// names fall back to html.
func namedPalette(cfg Config) simplecolor.NamedPalette {
	switch strings.ToLower(strings.TrimSpace(cfg.Palette)) {
	case "", "html":
		return html.GetNamedPalette()
	}
	return html.GetNamedPalette()
}

// animalList resolves cfg.Animals to a stable, sorted slice. Empty
// means use the built-in animals package.
func animalList(cfg Config) []string {
	if len(cfg.Animals) > 0 {
		out := make([]string, 0, len(cfg.Animals))
		for _, a := range cfg.Animals {
			a = strings.TrimSpace(strings.ToLower(a))
			if a != "" {
				out = append(out, a)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return animals.Names()
}

// Identity is a session's (color, animal) pair.
type Identity struct {
	Color  string
	Animal string
}

// String returns "color-animal", the canonical human-readable form.
func (i Identity) String() string {
	if i.Color == "" && i.Animal == "" {
		return ""
	}
	return i.Color + "-" + i.Animal
}

// Assign derives a stable Identity from a session id using the given
// config. Two identical (sessionID, cfg) inputs always produce the same
// output, so the identity can be recomputed rather than persisted if
// needed — but the swarm tool persists it to sessions.color/animal so
// palette/animal-list changes do not silently rename live sessions.
func Assign(sessionID string, cfg Config) Identity {
	palette := namedPalette(cfg)
	// Copy before sorting so we don't mutate a slice the palette
	// package might share across callers (map-order defensively).
	colorNames := append([]string(nil), palette.Names()...)
	slices.Sort(colorNames)
	if len(colorNames) == 0 {
		colorNames = []string{"white"}
	}
	list := append([]string(nil), animalList(cfg)...)
	slices.Sort(list)
	if len(list) == 0 {
		list = []string{"unknown"}
	}
	// Two independent hashes salted differently so color and animal
	// don't correlate. modIndex normalizes signed-int hash outputs
	// to a non-negative index defensively.
	h1 := colorhash.HashString("color:" + sessionID)
	h2 := colorhash.HashString("animal:" + sessionID)
	return Identity{
		Color:  colorNames[modIndex(h1, len(colorNames))],
		Animal: list[modIndex(h2, len(list))],
	}
}

// modIndex maps a possibly-signed hash h into [0, n).
func modIndex(h, n int) int {
	if n <= 0 {
		return 0
	}
	m := h % n
	if m < 0 {
		m += n
	}
	return m
}

// ShortHash returns the last 4 characters of the session id used to
// disambiguate identity collisions. Callers should always compare it
// case-insensitively.
func ShortHash(sessionID string) string {
	if len(sessionID) <= 4 {
		return strings.ToLower(sessionID)
	}
	return strings.ToLower(sessionID[len(sessionID)-4:])
}

// Address is a parsed swarm address ("color-animal" or
// "color-animal-shorthash" or a raw session id).
type Address struct {
	// Raw is the unparsed input as given by the caller.
	Raw string
	// Color, Animal are lowercased if the input parsed as
	// color-animal[-shorthash]. Empty means the input was treated as
	// a raw session id.
	Color  string
	Animal string
	// ShortHash is the (optional) session-id suffix disambiguator,
	// lowercased. Empty when the caller didn't supply one.
	ShortHash string
	// SessionID is non-empty only when the input was a full UUID
	// (i.e. long enough not to be a color-animal).
	SessionID string
}

// ParseAddress splits an address string. It accepts:
//
//   - "color-animal"
//   - "color-animal-<4 lowercase-hex chars>"
//   - a canonical UUIDv4 (36 chars, dashes at 8/13/18/23) or a bare
//     32-hex string
//
// Animals may themselves contain hyphens (e.g. "polar-bear"), so
// parsing splits from the right: the last token that looks like a
// 4-hex shorthash is peeled off first, then the first token is the
// color and the remainder is the animal. This means the animal list
// must not contain a token of the form `<4hex>`.
//
// Case insensitive. Returns (Address{Raw: s}, false) if the input is
// blank or clearly malformed.
func ParseAddress(s string) (Address, bool) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Address{}, false
	}
	lower := strings.ToLower(raw)
	// Canonical UUID shape or bare 32-hex → raw session id.
	if isUUIDShape(lower) {
		return Address{Raw: raw, SessionID: lower}, true
	}
	parts := strings.Split(lower, "-")
	if len(parts) < 2 {
		return Address{}, false
	}
	// Peel a 4-hex shorthash off the tail if present.
	shortHash := ""
	if len(parts) >= 3 && isShortHash(parts[len(parts)-1]) {
		shortHash = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return Address{}, false
	}
	color := parts[0]
	animal := strings.Join(parts[1:], "-")
	return Address{Raw: raw, Color: color, Animal: animal, ShortHash: shortHash}, true
}

// MatchesColorAnimal reports whether a session with the given color,
// animal, and id satisfies the color/animal (and optional shorthash)
// portion of the address. It is only meaningful for a color-animal
// address (a.SessionID == ""); it centralizes the comparison so every
// lookup path — live workspaces, detached-root peeks, and post-attach
// re-verification — filters identically and cannot silently drift.
func (a Address) MatchesColorAnimal(color, animal, sessionID string) bool {
	if !strings.EqualFold(color, a.Color) || !strings.EqualFold(animal, a.Animal) {
		return false
	}
	if a.ShortHash != "" && !strings.EqualFold(ShortHash(sessionID), a.ShortHash) {
		return false
	}
	return true
}

// isUUIDShape reports whether s is a canonical UUID (36 chars,
// dashes at 8/13/18/23, hex elsewhere) or a bare 32-hex string.
func isUUIDShape(s string) bool {
	switch len(s) {
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return false
		}
		for i, r := range s {
			if i == 8 || i == 13 || i == 18 || i == 23 {
				continue
			}
			if !isHex(r) {
				return false
			}
		}
		return true
	case 32:
		for _, r := range s {
			if !isHex(r) {
				return false
			}
		}
		return true
	}
	return false
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

// isShortHash reports whether s looks like the 4-char shorthash
// suffix produced by [ShortHash] (4 lowercase hex chars).
func isShortHash(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// FormatAddress builds "color-animal[-shorthash]" from an identity
// and (optionally) a session id used to compute the disambiguator.
// Pass empty sessionID to skip the shorthash.
func FormatAddress(id Identity, sessionID string) string {
	base := id.String()
	if base == "" {
		return sessionID
	}
	if sessionID == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, ShortHash(sessionID))
}

// ValidateAnimalName reports why an animal-list entry supplied by a
// user (e.g. a theme's swarm.animals list) is unusable for
// addressing, or nil if it is fine. The rules mirror the invariants
// [ParseAddress] depends on: a lowercase, non-empty name whose
// final hyphen-separated token is NOT a bare 4-hex string (which
// would be indistinguishable from a shorthash suffix).
func ValidateAnimalName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("swarm animal name is empty")
	}
	if strings.ToLower(trimmed) != trimmed {
		return fmt.Errorf("swarm animal %q must be lowercase", name)
	}
	// Reject a bare 4-hex entry entirely — as the sole animal it
	// masquerades as a shorthash.
	if isShortHash(trimmed) {
		return fmt.Errorf("swarm animal %q is 4 hex chars; ambiguous with a shorthash suffix", name)
	}
	// Reject any name whose last hyphen-separated token is 4-hex
	// (e.g. "deer-abcd"): ParseAddress would peel "abcd" off as a
	// shorthash and mis-resolve.
	if idx := strings.LastIndex(trimmed, "-"); idx >= 0 {
		if isShortHash(trimmed[idx+1:]) {
			return fmt.Errorf("swarm animal %q ends in a 4-hex token; would collide with the shorthash suffix", name)
		}
	}
	return nil
}
