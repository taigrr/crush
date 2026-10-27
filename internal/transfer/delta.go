package transfer

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// FileSig identifies a file the destination already has, so the sender
// can tell it to reuse its copy instead of streaming the bytes again.
type FileSig struct {
	Path   string `json:"p"`
	Size   int64  `json:"s"`
	SHA256 string `json:"h"`
}

const (
	// reuseMinSize skips tiny files: re-sending them is cheaper than
	// listing and hashing them.
	reuseMinSize = 4 << 10
	// maxIndexEntries keeps the preflight response well under the
	// cluster RPC body limit.
	maxIndexEntries = 40_000
	// maxIndexBytes caps the encoded index size the same way.
	maxIndexBytes = 4 << 20
	// indexBudget bounds how long the destination spends hashing during
	// preflight; files not hashed in time are simply re-sent.
	indexBudget = 20 * time.Second

	paxReuse = "CRUSH.reuse"
)

// IndexTree hashes the files under root that a transfer could reuse,
// largest first, within a fixed time budget.
func IndexTree(ctx context.Context, root string, opts TreeOptions) []FileSig {
	type cand struct {
		rel  string
		size int64
	}
	var cands []cand
	_ = walkTree(ctx, root, opts, func(rel string, info fs.FileInfo) error {
		if info.Mode().IsRegular() && info.Size() >= reuseMinSize {
			cands = append(cands, cand{rel, info.Size()})
		}
		return nil
	})
	slices.SortFunc(cands, func(a, b cand) int { return cmp.Compare(b.size, a.size) })
	if len(cands) > maxIndexEntries {
		cands = cands[:maxIndexEntries]
	}
	deadline := time.Now().Add(indexBudget)
	sigs := make([]FileSig, 0, len(cands))
	var encoded int
	for _, c := range cands {
		if encoded += len(c.rel) + 100; encoded > maxIndexBytes {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		sum, err := hashFile(joinRel(root, c.rel))
		if err != nil {
			continue
		}
		sigs = append(sigs, FileSig{Path: c.rel, Size: c.size, SHA256: sum})
	}
	return sigs
}

// SigIndex maps paths to signatures for quick lookup.
func SigIndex(sigs []FileSig) map[string]FileSig {
	if len(sigs) == 0 {
		return nil
	}
	m := make(map[string]FileSig, len(sigs))
	for _, s := range sigs {
		m[s.Path] = s
	}
	return m
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GitIgnored lists the paths under root that git ignores (directories
// collapsed to a single entry), or nil when root is not a git work tree.
func GitIgnored(ctx context.Context, root string) map[string]bool {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for p := range bytes.SplitSeq(out, []byte{0}) {
		if s := strings.TrimSuffix(string(p), "/"); s != "" {
			set[s] = true
		}
	}
	return set
}
