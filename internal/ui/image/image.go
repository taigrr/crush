package image

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"io"
	"log/slog"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/disintegration/imaging"
	paintbrush "github.com/jordanella/go-ansi-paintbrush"
	"github.com/taigrr/crush/internal/ui/util"
)

// TransmittedMsg is a message indicating that an image has been transmitted to
// the terminal.
type TransmittedMsg struct {
	ID string
}

// Encoding represents the encoding format of the image.
type Encoding byte

// Image encodings.
const (
	EncodingBlocks Encoding = iota
	EncodingKitty
)

type imageKey struct {
	id   string
	cols int
	rows int
}

// Hash returns a hash value for the image key.
// This uses FNV-32a for simplicity and speed.
func (k imageKey) Hash() uint32 {
	h := fnv.New32a()
	_, _ = io.WriteString(h, k.ID())
	return h.Sum32()
}

// ID returns a unique string representation of the image key.
func (k imageKey) ID() string {
	return fmt.Sprintf("%s-%dx%d", k.id, k.cols, k.rows)
}

// CellSize represents the size of a single terminal cell in pixels.
type CellSize struct {
	Width, Height int
}

type cachedImage struct {
	img        image.Image
	cols, rows int
	// fitCols and fitRows are the image's cell footprint after the
	// aspect-preserving fit. Using them (rather than cols/rows) for the kitty
	// placement and placeholder grid renders the image flush-left instead of
	// letterboxed inside the full box.
	fitCols, fitRows int
}

var (
	cachedImages = map[imageKey]cachedImage{}
	cachedMutex  sync.RWMutex
)

// ResetCache clears the image cache, freeing all cached decoded images.
func ResetCache() {
	cachedMutex.Lock()
	clear(cachedImages)
	cachedMutex.Unlock()
}

// fitImage resizes the image to fit within the specified dimensions in
// terminal cells, maintaining the aspect ratio. It returns the resized image
// along with its cell footprint, which is <= cols/rows because the fit
// preserves aspect ratio.
func fitImage(id string, img image.Image, cs CellSize, cols, rows int) (image.Image, int, int) {
	if img == nil {
		return nil, cols, rows
	}

	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	cached, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if ok {
		return cached.img, cached.fitCols, cached.fitRows
	}

	if cs.Width == 0 || cs.Height == 0 {
		return img, cols, rows
	}

	maxWidth := cols * cs.Width
	maxHeight := rows * cs.Height

	img = imaging.Fit(img, maxWidth, maxHeight, imaging.Lanczos)

	b := img.Bounds()
	fitCols := min(cols, ceilDiv(b.Dx(), cs.Width))
	fitRows := min(rows, ceilDiv(b.Dy(), cs.Height))

	cachedMutex.Lock()
	cachedImages[key] = cachedImage{
		img:     img,
		cols:    cols,
		rows:    rows,
		fitCols: fitCols,
		fitRows: fitRows,
	}
	cachedMutex.Unlock()

	return img, fitCols, fitRows
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

// HasTransmitted checks if the image with the given ID has already been
// transmitted to the terminal.
func HasTransmitted(id string, cols, rows int) bool {
	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	_, ok := cachedImages[key]
	cachedMutex.RUnlock()
	return ok
}

// Transmit transmits the image data to the terminal if needed. This is used to
// cache the image on the terminal for later rendering.
func (e Encoding) Transmit(id string, img image.Image, cs CellSize, cols, rows int, tmux bool) tea.Cmd {
	if img == nil {
		return nil
	}

	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	_, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if ok {
		return nil
	}

	// Cache synchronously so the render in the same update cycle resolves
	// HasTransmitted; only the terminal write stays in the returned command.
	fitted, fitCols, fitRows := fitImage(id, img, cs, cols, rows)

	if e != EncodingKitty {
		return func() tea.Msg {
			return TransmittedMsg{ID: key.ID()}
		}
	}

	return func() tea.Msg {
		var buf bytes.Buffer
		bounds := fitted.Bounds()
		imgWidth := bounds.Dx()
		imgHeight := bounds.Dy()
		imgID := int(key.Hash())
		if err := kitty.EncodeGraphics(&buf, fitted, &kitty.Options{
			ID:               imgID,
			Action:           kitty.TransmitAndPut,
			Transmission:     kitty.Direct,
			Format:           kitty.RGBA,
			ImageWidth:       imgWidth,
			ImageHeight:      imgHeight,
			Columns:          fitCols,
			Rows:             fitRows,
			VirtualPlacement: true,
			Quiet:            1,
			Chunk:            true,
			ChunkFormatter: func(chunk string) string {
				if tmux {
					return ansi.TmuxPassthrough(chunk)
				}
				return chunk
			},
		}); err != nil {
			slog.Error("Failed to encode image for kitty graphics", "err", err)
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  "failed to encode image",
			}
		}

		return tea.RawMsg{Msg: buf.String()}
	}
}

// Render renders the given image within the specified dimensions using the
// specified encoding.
func (e Encoding) Render(id string, cols, rows int) string {
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.RLock()
	cached, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if !ok {
		return ""
	}

	img := cached.img

	switch e {
	case EncodingBlocks:
		canvas := paintbrush.New()
		canvas.SetImage(img)
		canvas.SetWidth(cols)
		canvas.SetHeight(rows)
		canvas.Weights = map[rune]float64{
			'': .95,
			'': .95,
			'▁': .9,
			'▂': .9,
			'▃': .9,
			'▄': .9,
			'▅': .9,
			'▆': .85,
			'█': .85,
			'▊': .95,
			'▋': .95,
			'▌': .95,
			'▍': .95,
			'▎': .95,
			'▏': .95,
			'●': .95,
			'◀': .95,
			'▲': .95,
			'▶': .95,
			'▼': .9,
			'○': .8,
			'◉': .95,
			'◧': .9,
			'◨': .9,
			'◩': .9,
			'◪': .9,
		}
		canvas.Paint()
		return strings.TrimSpace(canvas.GetResult())
	case EncodingKitty:
		fitCols, fitRows := cached.fitCols, cached.fitRows
		if fitCols == 0 || fitRows == 0 {
			fitCols, fitRows = cols, rows
		}

		var fg color.Color
		var extra int
		var r, g, b int
		hashedID := key.Hash()
		id := int(hashedID)
		extra, r, g, b = id>>24&0xff, id>>16&0xff, id>>8&0xff, id&0xff

		if id <= 255 {
			fg = ansi.IndexedColor(b)
		} else {
			fg = color.RGBA{
				R: uint8(r), //nolint:gosec
				G: uint8(g), //nolint:gosec
				B: uint8(b), //nolint:gosec
				A: 0xff,
			}
		}

		fgStyle := ansi.NewStyle().ForegroundColor(fg).String()

		var buf bytes.Buffer
		for y := range fitRows {
			for x := range fitCols {
				buf.WriteString(fgStyle)
				buf.WriteRune(kitty.Placeholder)
				buf.WriteRune(kitty.Diacritic(y))
				buf.WriteRune(kitty.Diacritic(x))
				if extra > 0 {
					buf.WriteRune(kitty.Diacritic(extra))
				}
			}
			if y < fitRows-1 {
				buf.WriteByte('\n')
			}
		}

		return buf.String()

	default:
		return ""
	}
}
