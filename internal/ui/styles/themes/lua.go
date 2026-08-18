package themes

import (
	"fmt"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/crush/internal/ui/styles"
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

	// Diff view.
	DiffAddFg        string
	DiffAddBg        string
	DiffAddBgEmph    string
	DiffRemoveFg     string
	DiffRemoveBg     string
	DiffRemoveBgEmph string

	// Brand accent for the Hypercredit icon/count.
	Hypercredit string

	// Syntax highlighting roles.
	SyntaxLink            string
	SyntaxImage           string
	SyntaxCommentPreproc  string
	SyntaxKeywordReserved string
	SyntaxKeywordType     string
	SyntaxOperator        string
	SyntaxNameBuiltin     string
	SyntaxNameTag         string
	SyntaxNameAttribute   string
	SyntaxNameClass       string
	SyntaxNameDecorator   string
	SyntaxLiteralString   string

	// Brand surfaces (optional). When empty, these cascade to the brand
	// pair (secondary/primary) so themes that don't override stay
	// visually consistent.
	HeaderCharm     string
	HeaderDiagonals string
	LogoGradFrom    string
	LogoGradTo      string
	WorkingGradFrom string
	WorkingGradTo   string
}

// toStyles builds a Styles from the palette. Empty fields fall back to the
// default Charmtone palette so partial themes still produce a usable UI.
func (p Palette) toStyles() styles.Styles {
	def := defaultPalette()
	c := func(v, fallback string) color.Color {
		if strings.TrimSpace(v) == "" {
			return lipgloss.Color(fallback)
		}
		return lipgloss.Color(v)
	}
	return styles.QuickStyle(styles.QuickStyleOpts{
		Primary:           c(p.Primary, def.Primary),
		Secondary:         c(p.Secondary, def.Secondary),
		Accent:            c(p.Accent, def.Accent),
		Keyword:           c(p.Keyword, def.Keyword),
		FgBase:            c(p.FgBase, def.FgBase),
		BgBase:            c(p.BgBase, def.BgBase),
		Separator:         c(p.Separator, def.Separator),
		FgSubtle:          c(p.FgSubtle, def.FgSubtle),
		FgMoreSubtle:      c(p.FgMoreSubtle, def.FgMoreSubtle),
		FgMostSubtle:      c(p.FgMostSubtle, def.FgMostSubtle),
		OnPrimary:         c(p.OnPrimary, def.OnPrimary),
		BgMostVisible:     c(p.BgMostVisible, def.BgMostVisible),
		BgLessVisible:     c(p.BgLessVisible, def.BgLessVisible),
		BgLeastVisible:    c(p.BgLeastVisible, def.BgLeastVisible),
		Destructive:       c(p.Destructive, def.Destructive),
		Error:             c(p.Error, def.Error),
		Warning:           c(p.Warning, def.Warning),
		WarningSubtle:     c(p.WarningSubtle, def.WarningSubtle),
		Denied:            c(p.Denied, def.Denied),
		Busy:              c(p.Busy, def.Busy),
		Info:              c(p.Info, def.Info),
		InfoMoreSubtle:    c(p.InfoMoreSubtle, def.InfoMoreSubtle),
		InfoMostSubtle:    c(p.InfoMostSubtle, def.InfoMostSubtle),
		Success:           c(p.Success, def.Success),
		SuccessMoreSubtle: c(p.SuccessMoreSubtle, def.SuccessMoreSubtle),
		SuccessMostSubtle: c(p.SuccessMostSubtle, def.SuccessMostSubtle),

		DiffAddFg:        c(p.DiffAddFg, def.DiffAddFg),
		DiffAddBg:        c(p.DiffAddBg, def.DiffAddBg),
		DiffAddBgEmph:    c(p.DiffAddBgEmph, def.DiffAddBgEmph),
		DiffRemoveFg:     c(p.DiffRemoveFg, def.DiffRemoveFg),
		DiffRemoveBg:     c(p.DiffRemoveBg, def.DiffRemoveBg),
		DiffRemoveBgEmph: c(p.DiffRemoveBgEmph, def.DiffRemoveBgEmph),

		Hypercredit: c(p.Hypercredit, def.Hypercredit),

		SyntaxLink:            c(p.SyntaxLink, def.SyntaxLink),
		SyntaxImage:           c(p.SyntaxImage, def.SyntaxImage),
		SyntaxCommentPreproc:  c(p.SyntaxCommentPreproc, def.SyntaxCommentPreproc),
		SyntaxKeywordReserved: c(p.SyntaxKeywordReserved, def.SyntaxKeywordReserved),
		SyntaxKeywordType:     c(p.SyntaxKeywordType, def.SyntaxKeywordType),
		SyntaxOperator:        c(p.SyntaxOperator, def.SyntaxOperator),
		SyntaxNameBuiltin:     c(p.SyntaxNameBuiltin, def.SyntaxNameBuiltin),
		SyntaxNameTag:         c(p.SyntaxNameTag, def.SyntaxNameTag),
		SyntaxNameAttribute:   c(p.SyntaxNameAttribute, def.SyntaxNameAttribute),
		SyntaxNameClass:       c(p.SyntaxNameClass, def.SyntaxNameClass),
		SyntaxNameDecorator:   c(p.SyntaxNameDecorator, def.SyntaxNameDecorator),
		SyntaxLiteralString:   c(p.SyntaxLiteralString, def.SyntaxLiteralString),

		// Optional brand surfaces — leave nil when the theme didn't set
		// them so quickStyle's cascade picks up the brand pair.
		HeaderCharm:     optColor(p.HeaderCharm),
		HeaderDiagonals: optColor(p.HeaderDiagonals),
		LogoGradFrom:    optColor(p.LogoGradFrom),
		LogoGradTo:      optColor(p.LogoGradTo),
		WorkingGradFrom: optColor(p.WorkingGradFrom),
		WorkingGradTo:   optColor(p.WorkingGradTo),
	})
}

// optColor returns nil for empty input so quickStyle's cascade kicks in.
func optColor(v string) color.Color {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return lipgloss.Color(v)
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

		DiffAddFg:        "#629657",
		DiffAddBg:        "#323931",
		DiffAddBgEmph:    "#2b322a",
		DiffRemoveFg:     "#a45c59",
		DiffRemoveBg:     "#383030",
		DiffRemoveBgEmph: "#312929",

		Hypercredit: charmtone.Dolly.Hex(),

		SyntaxLink:            charmtone.Zinc.Hex(),
		SyntaxImage:           charmtone.Cheeky.Hex(),
		SyntaxCommentPreproc:  charmtone.Bengal.Hex(),
		SyntaxKeywordReserved: charmtone.Pony.Hex(),
		SyntaxKeywordType:     charmtone.Guppy.Hex(),
		SyntaxOperator:        charmtone.Salmon.Hex(),
		SyntaxNameBuiltin:     charmtone.Cheeky.Hex(),
		SyntaxNameTag:         charmtone.Mauve.Hex(),
		SyntaxNameAttribute:   charmtone.Hazy.Hex(),
		SyntaxNameClass:       charmtone.Salt.Hex(),
		SyntaxNameDecorator:   charmtone.Citron.Hex(),
		SyntaxLiteralString:   charmtone.Cumin.Hex(),
	}
}

// UserTheme is a theme loaded from a user Lua file.
type UserTheme struct {
	Name   string
	IsDark bool
	Styles styles.Styles

	// Swarm holds the theme's optional swarm identity configuration —
	// the palette used to hash session colors and the animal list.
	// A separate top-level Lua table (swarm = { palette = "html",
	// animals = { ... } }) keeps this decoupled from the visual
	// Palette above; themes that don't set it inherit the built-in
	// defaults.
	Swarm SwarmThemeConfig
}

// SwarmThemeConfig mirrors the theme's swarm table. Empty values mean
// "use the built-in default" (see internal/swarm.Default).
type SwarmThemeConfig struct {
	Palette string
	Animals []string
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

		DiffAddFg:        str("diff_add_fg"),
		DiffAddBg:        str("diff_add_bg"),
		DiffAddBgEmph:    str("diff_add_bg_emph"),
		DiffRemoveFg:     str("diff_remove_fg"),
		DiffRemoveBg:     str("diff_remove_bg"),
		DiffRemoveBgEmph: str("diff_remove_bg_emph"),

		Hypercredit: str("hypercredit"),

		SyntaxLink:            str("syntax_link"),
		SyntaxImage:           str("syntax_image"),
		SyntaxCommentPreproc:  str("syntax_comment_preproc"),
		SyntaxKeywordReserved: str("syntax_keyword_reserved"),
		SyntaxKeywordType:     str("syntax_keyword_type"),
		SyntaxOperator:        str("syntax_operator"),
		SyntaxNameBuiltin:     str("syntax_name_builtin"),
		SyntaxNameTag:         str("syntax_name_tag"),
		SyntaxNameAttribute:   str("syntax_name_attribute"),
		SyntaxNameClass:       str("syntax_name_class"),
		SyntaxNameDecorator:   str("syntax_name_decorator"),
		SyntaxLiteralString:   str("syntax_literal_string"),

		HeaderCharm:     str("header_charm"),
		HeaderDiagonals: str("header_diagonals"),
		LogoGradFrom:    str("logo_grad_from"),
		LogoGradTo:      str("logo_grad_to"),
		WorkingGradFrom: str("working_grad_from"),
		WorkingGradTo:   str("working_grad_to"),
	}

	return UserTheme{
		Name:   name,
		IsDark: isDark,
		Styles: palette.toStyles(),
		Swarm:  loadSwarmTheme(tbl),
	}, nil
}

// loadSwarmTheme extracts the optional top-level `swarm` sub-table.
// The table has the shape:
//
//	swarm = {
//	  palette = "html",         -- optional, string
//	  animals = { "cat", ... }, -- optional, list of strings
//	}
//
// Any other keys are ignored. Missing or malformed values fall back to
// the swarm package defaults so a partially-authored theme still works.
func loadSwarmTheme(tbl *lua.LTable) SwarmThemeConfig {
	v := tbl.RawGetString("swarm")
	sub, ok := v.(*lua.LTable)
	if !ok {
		return SwarmThemeConfig{}
	}
	cfg := SwarmThemeConfig{}
	if s, ok := sub.RawGetString("palette").(lua.LString); ok {
		cfg.Palette = strings.TrimSpace(string(s))
	}
	if arr, ok := sub.RawGetString("animals").(*lua.LTable); ok {
		arr.ForEach(func(_, val lua.LValue) {
			if s, ok := val.(lua.LString); ok {
				name := strings.TrimSpace(strings.ToLower(string(s)))
				if name == "" {
					return
				}
				if err := swarm.ValidateAnimalName(name); err != nil {
					slog.Warn("Skipping invalid swarm animal name in theme",
						"name", string(s), "reason", err.Error())
					return
				}
				cfg.Animals = append(cfg.Animals, name)
			}
		})
	}
	return cfg
}
