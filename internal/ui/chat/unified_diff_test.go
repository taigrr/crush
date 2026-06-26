package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff_GitSingleFile(t *testing.T) {
	t.Parallel()
	in := "diff --git a/foo.go b/foo.go\n" +
		"index 123..456 100644\n" +
		"--- a/foo.go\n" +
		"+++ b/foo.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" ctx\n" +
		"-old\n" +
		"+new\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	require.Equal(t, "foo.go", files[0].path)
	require.Equal(t, "ctx\nold", files[0].before)
	require.Equal(t, "ctx\nnew", files[0].after)
}

func TestParseUnifiedDiff_MultipleFiles(t *testing.T) {
	t.Parallel()
	in := "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n" +
		"diff --git a/b.go b/b.go\n" +
		"--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-p\n+q\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 2)
	require.Equal(t, "a.go", files[0].path)
	require.Equal(t, "x", files[0].before)
	require.Equal(t, "y", files[0].after)
	require.Equal(t, "b.go", files[1].path)
	require.Equal(t, "p", files[1].before)
	require.Equal(t, "q", files[1].after)
}

func TestParseUnifiedDiff_PlainNoGitHeader(t *testing.T) {
	t.Parallel()
	in := "--- a/foo.txt\n+++ b/foo.txt\n@@ -1 +1 @@\n-a\n+b\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	require.Equal(t, "foo.txt", files[0].path)
	require.Equal(t, "a", files[0].before)
	require.Equal(t, "b", files[0].after)
}

func TestParseUnifiedDiff_NewFileDevNull(t *testing.T) {
	t.Parallel()
	in := "diff --git a/new.go b/new.go\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/new.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+line1\n+line2\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	// path comes from the git header (b/new.go); /dev/null must not overwrite it.
	require.Equal(t, "new.go", files[0].path)
	require.Empty(t, files[0].before)
	require.Equal(t, "line1\nline2", files[0].after)
}

func TestParseUnifiedDiff_DeletedFile(t *testing.T) {
	t.Parallel()
	in := "diff --git a/gone.go b/gone.go\n" +
		"deleted file mode 100644\n" +
		"--- a/gone.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-old1\n-old2\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	require.Equal(t, "gone.go", files[0].path)
	require.Equal(t, "old1\nold2", files[0].before)
	require.Empty(t, files[0].after)
}

func TestParseUnifiedDiff_TabSuffixStripped(t *testing.T) {
	t.Parallel()
	in := "--- a/foo.txt\t2024-01-01 00:00:00\n" +
		"+++ b/foo.txt\t2024-01-02 00:00:00\n" +
		"@@ -1 +1 @@\n-a\n+b\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	require.Equal(t, "foo.txt", files[0].path)
}

func TestParseUnifiedDiff_PlainMultipleHunksSameFile(t *testing.T) {
	t.Parallel()
	in := "--- a/f.go\n+++ b/f.go\n" +
		"@@ -1 +1 @@\n-a\n+b\n" +
		"@@ -10 +10 @@\n-c\n+d\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 1)
	require.Equal(t, "a\nc", files[0].before)
	require.Equal(t, "b\nd", files[0].after)
}

func TestParseUnifiedDiff_Empty(t *testing.T) {
	t.Parallel()
	require.Empty(t, parseUnifiedDiff(""))
}

func TestParseUnifiedDiff_NoFileHeaderIgnored(t *testing.T) {
	t.Parallel()
	// Content with no recognizable file header produces no files; the +/-
	// lines have no current file to attach to.
	in := "just some text\n-foo\n+bar\n"
	require.Empty(t, parseUnifiedDiff(in))
}

func TestParseUnifiedDiff_ConsecutivePlainFiles(t *testing.T) {
	t.Parallel()
	// Two plain (non-git) file sections back to back: a new "--- " while in a
	// hunk whose next line is "+++ " starts a new file.
	in := "--- a/one\n+++ b/one\n@@ -1 +1 @@\n-x\n+y\n" +
		"--- a/two\n+++ b/two\n@@ -1 +1 @@\n-p\n+q\n"
	files := parseUnifiedDiff(in)
	require.Len(t, files, 2)
	require.Equal(t, "one", files[0].path)
	require.Equal(t, "two", files[1].path)
}

func TestParseUnifiedDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []parsedDiffFile
	}{
		{
			name: "simple diff with additions and removals",
			input: `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main
 
+import "fmt"
+
 func main() {
-    println("hello")
+    fmt.Println("hello")
 }
`,
			want: []parsedDiffFile{
				{
					path:   "main.go",
					before: "package main\n\nfunc main() {\n    println(\"hello\")\n}",
					after:  "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"hello\")\n}",
				},
			},
		},
		{
			name: "new file creation",
			input: `diff --git a/newfile.go b/newfile.go
new file mode 100644
--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`,
			want: []parsedDiffFile{
				{
					path:   "newfile.go",
					before: "",
					after:  "package main\n\nfunc main() {}",
				},
			},
		},
		{
			name: "file deletion",
			input: `diff --git a/oldfile.go b/oldfile.go
deleted file mode 100644
--- a/oldfile.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}
`,
			want: []parsedDiffFile{
				{
					path:   "oldfile.go",
					before: "package main\n\nfunc main() {}",
					after:  "",
				},
			},
		},
		{
			name:  "non-diff content",
			input: "Just some regular text",
			want:  nil,
		},
		{
			name: "diff with timestamp in header",
			input: `diff --git a/config.yml b/config.yml
--- a/config.yml	2024-01-15 10:30:00
+++ b/config.yml	2024-01-15 10:31:00
@@ -1,3 +1,4 @@
 name: myapp
-version: 1.0
+version: 1.1
+debug: true
`,
			want: []parsedDiffFile{
				{
					path:   "config.yml",
					before: "name: myapp\nversion: 1.0",
					after:  "name: myapp\nversion: 1.1\ndebug: true",
				},
			},
		},
		{
			name: "multi-file diff",
			input: `diff --git a/one.txt b/one.txt
--- a/one.txt
+++ b/one.txt
@@ -1,3 +1,3 @@
 line one
-line two
+line two updated
 line three
diff --git a/two.txt b/two.txt
--- a/two.txt
+++ b/two.txt
@@ -1,2 +1,3 @@
 alpha
+beta
 gamma
`,
			want: []parsedDiffFile{
				{
					path:   "one.txt",
					before: "line one\nline two\nline three",
					after:  "line one\nline two updated\nline three",
				},
				{
					path:   "two.txt",
					before: "alpha\ngamma",
					after:  "alpha\nbeta\ngamma",
				},
			},
		},
		{
			name: "non-git unified patch",
			input: `--- old.c
+++ old.c
@@ -1,3 +1,4 @@
 #include <stdio.h>
-int main() {
+int main(int argc, char **argv) {
     return 0;
 }
`,
			want: []parsedDiffFile{
				{
					path:   "old.c",
					before: "#include <stdio.h>\nint main() {\n    return 0;\n}",
					after:  "#include <stdio.h>\nint main(int argc, char **argv) {\n    return 0;\n}",
				},
			},
		},
		{
			name: "non-git new file from /dev/null",
			input: `--- /dev/null
+++ newfile.txt
@@ -0,0 +1,2 @@
+hello
+world
`,
			want: []parsedDiffFile{
				{
					path:   "newfile.txt",
					before: "",
					after:  "hello\nworld",
				},
			},
		},
		{
			name: "non-git new file with only +++ header",
			input: `+++ brand_new.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`,
			want: []parsedDiffFile{
				{
					path:   "brand_new.go",
					before: "",
					after:  "package main\n\nfunc main() {}",
				},
			},
		},
		{
			name: "multi-hunk single file",
			input: `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -1,4 +1,5 @@
 package main
+import "os"
 
 func init() {
@@ -10,3 +11,3 @@
-    println("done")
+    fmt.Println("done")
 }
`,
			want: []parsedDiffFile{
				{
					path:   "big.go",
					before: "package main\n\nfunc init() {\n    println(\"done\")\n}",
					after:  "package main\nimport \"os\"\n\nfunc init() {\n    fmt.Println(\"done\")\n}",
				},
			},
		},
		{
			name: "hunk content starting with header-like prefixes",
			input: `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1,3 +1,3 @@
---- tricky
++++ newer
 keep
`,
			want: []parsedDiffFile{
				{
					path:   "file.txt",
					before: "--- tricky\nkeep",
					after:  "+++ newer\nkeep",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseUnifiedDiff(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("parseUnifiedDiff() returned %d files, want %d", len(got), len(tt.want))
				return
			}
			for i, w := range tt.want {
				if got[i].path != w.path {
					t.Errorf("parseUnifiedDiff()[%d].path = %q, want %q", i, got[i].path, w.path)
				}
				if got[i].before != w.before {
					t.Errorf("parseUnifiedDiff()[%d].before = %q, want %q", i, got[i].before, w.before)
				}
				if got[i].after != w.after {
					t.Errorf("parseUnifiedDiff()[%d].after = %q, want %q", i, got[i].after, w.after)
				}
			}
		})
	}
}
