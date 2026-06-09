package styles

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	lua "github.com/yuin/gopher-lua"
)

// Palette is a theme's full color palette expressed as hex strings (e.g.
// "#1e1e2e"). It mirrors the internal quickStyle palette and is the format
// user themes are authored in (via Lua) before being built into Styles.
type Palette struct {
	// Brand.
	Primary   string
	Secondary string
	Accent    string
	Keyword   string

	// Default foreground/background.
	FgBase string
	BgBase string

	// Dividers and rules.
	Separator string

	// Foreground subtlety ramp.
	FgSubtle     string
	FgMoreSubtle string
	FgMostSubtle string

	// Foreground designed to sit on a primary background.
	OnPrimary string

	// Background visibility ramp.
	BgMostVisible  string
	BgLessVisible  string
	BgLeastVisible string

	// Statuses.
	Destructive       string
	Error             string
	Warning           string
	WarningSubtle     string
	Denied            string
	Busy              string
	Info              string
	InfoMoreSubtle    string
	InfoMostSubtle    string
	Success           string
	SuccessMoreSubtle string
	SuccessMostSubtle string
}

// toStyles builds a Styles from the palette. Empty fields fall back to the
// default Charmtone palette so partial themes still produce a usable UI.
func (p Palette) toStyles() Styles {
	def := defaultPalette()
	c := func(v, fallback string) color.Color {
		if strings.TrimSpace(v) == "" {
			return lipgloss.Color(fallback)
		}
		return lipgloss.Color(v)
	}
	return quickStyle(quickStyleOpts{
		primary:           c(p.Primary, def.Primary),
		secondary:         c(p.Secondary, def.Secondary),
		accent:            c(p.Accent, def.Accent),
		keyword:           c(p.Keyword, def.Keyword),
		fgBase:            c(p.FgBase, def.FgBase),
		bgBase:            c(p.BgBase, def.BgBase),
		separator:         c(p.Separator, def.Separator),
		fgSubtle:          c(p.FgSubtle, def.FgSubtle),
		fgMoreSubtle:      c(p.FgMoreSubtle, def.FgMoreSubtle),
		fgMostSubtle:      c(p.FgMostSubtle, def.FgMostSubtle),
		onPrimary:         c(p.OnPrimary, def.OnPrimary),
		bgMostVisible:     c(p.BgMostVisible, def.BgMostVisible),
		bgLessVisible:     c(p.BgLessVisible, def.BgLessVisible),
		bgLeastVisible:    c(p.BgLeastVisible, def.BgLeastVisible),
		destructive:       c(p.Destructive, def.Destructive),
		error:             c(p.Error, def.Error),
		warning:           c(p.Warning, def.Warning),
		warningSubtle:     c(p.WarningSubtle, def.WarningSubtle),
		denied:            c(p.Denied, def.Denied),
		busy:              c(p.Busy, def.Busy),
		info:              c(p.Info, def.Info),
		infoMoreSubtle:    c(p.InfoMoreSubtle, def.InfoMoreSubtle),
		infoMostSubtle:    c(p.InfoMostSubtle, def.InfoMostSubtle),
		success:           c(p.Success, def.Success),
		successMoreSubtle: c(p.SuccessMoreSubtle, def.SuccessMoreSubtle),
		successMostSubtle: c(p.SuccessMostSubtle, def.SuccessMostSubtle),
	})
}

// defaultPalette returns the Charmtone Pantera palette as hex strings, used
// to fill in any fields a user theme omits.
func defaultPalette() Palette {
	return Palette{
		Primary:           charmtone.Charple.Hex(),
		Secondary:         charmtone.Dolly.Hex(),
		Accent:            charmtone.Bok.Hex(),
		Keyword:           charmtone.Blush.Hex(),
		FgBase:            charmtone.Sash.Hex(),
		FgMoreSubtle:      charmtone.Squid.Hex(),
		FgSubtle:          charmtone.Smoke.Hex(),
		FgMostSubtle:      charmtone.Oyster.Hex(),
		OnPrimary:         charmtone.Butter.Hex(),
		BgBase:            charmtone.Pepper.Hex(),
		BgLeastVisible:    charmtone.BBQ.Hex(),
		BgLessVisible:     charmtone.Char.Hex(),
		BgMostVisible:     charmtone.Iron.Hex(),
		Separator:         charmtone.Char.Hex(),
		Destructive:       charmtone.Coral.Hex(),
		Error:             charmtone.Sriracha.Hex(),
		WarningSubtle:     charmtone.Zest.Hex(),
		Warning:           charmtone.Mustard.Hex(),
		Denied:            charmtone.Tang.Hex(),
		Busy:              charmtone.Citron.Hex(),
		Info:              charmtone.Malibu.Hex(),
		InfoMoreSubtle:    charmtone.Sardine.Hex(),
		InfoMostSubtle:    charmtone.Damson.Hex(),
		Success:           charmtone.Julep.Hex(),
		SuccessMoreSubtle: charmtone.Bok.Hex(),
		SuccessMostSubtle: charmtone.Guac.Hex(),
	}
}

// UserTheme is a theme loaded from a user Lua file.
type UserTheme struct {
	Name   string
	IsDark bool
	Styles Styles
}

// LoadUserThemes loads every *.lua theme file from dir. Files that fail to
// parse are skipped (with their error returned in errs) so one bad theme
// doesn't break the picker. Themes whose names collide with a builtin or an
// earlier user theme are skipped. Returns themes sorted by name.
func LoadUserThemes(dir string) (themes []UserTheme, errs []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A missing themes directory is not an error: the user simply has
		// no custom themes.
		if !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("read themes dir: %w", err))
		}
		return nil, errs
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".lua") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		theme, err := LoadThemeFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		key := normalizeThemeName(theme.Name)
		if key == "" {
			errs = append(errs, fmt.Errorf("%s: theme has no name", entry.Name()))
			continue
		}
		if IsBuiltinTheme(key) {
			errs = append(errs, fmt.Errorf("%s: name %q collides with a builtin theme", entry.Name(), theme.Name))
			continue
		}
		if _, dup := seen[key]; dup {
			errs = append(errs, fmt.Errorf("%s: duplicate theme name %q", entry.Name(), theme.Name))
			continue
		}
		seen[key] = struct{}{}
		themes = append(themes, theme)
	}

	sort.Slice(themes, func(i, j int) bool {
		return normalizeThemeName(themes[i].Name) < normalizeThemeName(themes[j].Name)
	})
	return themes, errs
}

// LoadThemeFile loads and evaluates a single Lua theme file. The file must
// return a table with a "name" field and color fields (snake_case keys
// matching the Palette, e.g. "bg_base", "fg_subtle"). An optional "is_dark"
// boolean defaults to true.
func LoadThemeFile(path string) (UserTheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return UserTheme{}, err
	}

	L := lua.NewState(lua.Options{
		// No standard library: theme files are pure data, and this keeps
		// untrusted theme files from touching the filesystem or os.
		SkipOpenLibs: true,
	})
	defer L.Close()

	if err := L.DoString(string(data)); err != nil {
		return UserTheme{}, fmt.Errorf("evaluate lua: %w", err)
	}

	ret := L.Get(-1)
	tbl, ok := ret.(*lua.LTable)
	if !ok {
		return UserTheme{}, fmt.Errorf("theme file must return a table, got %s", ret.Type())
	}

	str := func(key string) string {
		v := tbl.RawGetString(key)
		if s, ok := v.(lua.LString); ok {
			return string(s)
		}
		return ""
	}

	name := strings.TrimSpace(str("name"))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	isDark := true
	if v := tbl.RawGetString("is_dark"); v != lua.LNil {
		if b, ok := v.(lua.LBool); ok {
			isDark = bool(b)
		}
	}

	palette := Palette{
		Primary:           str("primary"),
		Secondary:         str("secondary"),
		Accent:            str("accent"),
		Keyword:           str("keyword"),
		FgBase:            str("fg_base"),
		BgBase:            str("bg_base"),
		Separator:         str("separator"),
		FgSubtle:          str("fg_subtle"),
		FgMoreSubtle:      str("fg_more_subtle"),
		FgMostSubtle:      str("fg_most_subtle"),
		OnPrimary:         str("on_primary"),
		BgMostVisible:     str("bg_most_visible"),
		BgLessVisible:     str("bg_less_visible"),
		BgLeastVisible:    str("bg_least_visible"),
		Destructive:       str("destructive"),
		Error:             str("error"),
		Warning:           str("warning"),
		WarningSubtle:     str("warning_subtle"),
		Denied:            str("denied"),
		Busy:              str("busy"),
		Info:              str("info"),
		InfoMoreSubtle:    str("info_more_subtle"),
		InfoMostSubtle:    str("info_most_subtle"),
		Success:           str("success"),
		SuccessMoreSubtle: str("success_more_subtle"),
		SuccessMostSubtle: str("success_most_subtle"),
	}

	return UserTheme{
		Name:   name,
		IsDark: isDark,
		Styles: palette.toStyles(),
	}, nil
}
