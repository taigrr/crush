package checkpoint

import "testing"

func TestMatchesExcludePattern(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact match", "node_modules", "node_modules", true},
		{"exact nested path", "src/gen/out.go", "src/gen/out.go", true},
		{"base name at depth", "node_modules", "src/app/node_modules", true},
		{"base name file", "cache", "a/b/cache", true},
		{"doublestar nested dir", "**/node_modules", "a/b/node_modules", true},
		{"glob extension", "*.pyc", "foo.pyc", true},
		{"doublestar extension nested", "**/*.pyc", "a/b/foo.pyc", true},

		{"no match different name", "node_modules", "src/vendor", false},
		{"glob extension non-match", "*.pyc", "foo.py", false},
		{"prefix only is not a match", "build", "buildscript", false},
		{"partial path not base", "gen/out.go", "src/gen/out.go", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchesExcludePattern(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("matchesExcludePattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestIsExcluded(t *testing.T) {
	t.Parallel()

	r := &Repo{config: &Config{Exclude: []string{"**/node_modules", "*.pyc", "**/*.pyc", "dist"}}}

	cases := map[string]bool{
		"src/node_modules": true,
		"node_modules":     true, // base-name match
		"a/b/foo.pyc":      true, // nested, needs **/*.pyc
		"top.pyc":          true,
		"dist":             true,
		"src/main.go":      false,
		"distribution":     false,
	}
	for path, want := range cases {
		if got := r.isExcluded(path); got != want {
			t.Errorf("isExcluded(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestDefaultConfigExcludesNested guards against regressing the default
// exclusions: nested build artifacts must be excluded, not just top-level
// ones. A bare "*.pyc" glob does not cross directory separators, so the
// "**/" variants are required.
func TestDefaultConfigExcludesNested(t *testing.T) {
	t.Parallel()

	r := &Repo{config: DefaultConfig()}

	for _, path := range []string{
		"a/b/foo.pyc",
		"top.pyc",
		"src/app.log",
		"app.log",
		"a/b/node_modules",
		"a/b/__pycache__",
	} {
		if !r.isExcluded(path) {
			t.Errorf("default config should exclude %q", path)
		}
	}
}
