package image

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"io"
	"log/slog"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/uiutil"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/charmbracelet/x/mosaic"
)

// Capabilities represents the capabilities of displaying images on the
// terminal.
type Capabilities struct {
	// SupportsKittyGraphics indicates whether the terminal supports the Kitty
	// graphics protocol.
	SupportsKittyGraphics bool
}

// RequestCapabilities is a [tea.Cmd] that requests the terminal to report
// its image related capabilities to the program.
func RequestCapabilities() tea.Cmd {
	return tea.Raw(
		// ID 31 is just a random ID used to detect Kitty graphics support.
		ansi.KittyGraphics([]byte("AAAA"), "i=31", "s=1", "v=1", "a=q", "t=d", "f=24"),
	)
}

// Encoding represents the encoding format of the image.
type Encoding byte

// Image encodings.
const (
	EncodingBlocks Encoding = iota
	EncodingKitty
)

// Image represents an image that can be displayed on the terminal.
type Image struct {
	id         int
	img        image.Image
	cols, rows int // in terminal cells
	enc        Encoding
}

// New creates a new [Image] instance with the given unique id, image, and
// dimensions in terminal cells.
func New(id string, img image.Image, cols, rows int) (*Image, error) {
	i := new(Image)
	h := fnv.New64a()
	if _, err := io.WriteString(h, id); err != nil {
		return nil, err
	}
	i.id = int(h.Sum64())
	i.img = img
	i.cols = cols
	i.rows = rows
	return i, nil
}

// SetEncoding sets the encoding format for the image.
func (i *Image) SetEncoding(enc Encoding) {
	i.enc = enc
}

// Transmit returns a [tea.Cmd] that sends the image data to the terminal.
// This is needed for the [EncodingKitty] protocol so that the terminal can
// cache the image for later rendering.
//
// This should only happen once per image.
func (i *Image) Transmit() tea.Cmd {
	if i.enc != EncodingKitty {
		return nil
	}

	var buf bytes.Buffer
	bounds := i.img.Bounds()
	imgWidth := bounds.Dx()
	imgHeight := bounds.Dy()

	// RGBA is 4 bytes per pixel
	imgSize := imgWidth * imgHeight * 4

	if err := kitty.EncodeGraphics(&buf, i.img, &kitty.Options{
		ID:               i.id,
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.RGBA,
		Size:             imgSize,
		Width:            imgWidth,
		Height:           imgHeight,
		Columns:          i.cols,
		Rows:             i.rows,
		VirtualPlacement: true,
		Quite:            2,
	}); err != nil {
		slog.Error("failed to encode image for kitty graphics", "err", err)
		return uiutil.ReportError(fmt.Errorf("failed to encode image"))
	}

	return tea.Raw(buf.String())
}

// Render renders the image to a string that can be displayed on the terminal.
func (i *Image) Render() string {
	// Check cache first
	switch i.enc {
	case EncodingBlocks:
		m := mosaic.New().Width(i.cols).Height(i.rows).Scale(2)
		return m.Render(i.img)
	case EncodingKitty:
		// Build Kitty graphics unicode place holders
		var fg color.Color
		var extra int
		var r, g, b int
		extra, r, g, b = i.id>>24&0xff, i.id>>16&0xff, i.id>>8&0xff, i.id&0xff

		if r == 0 && g == 0 {
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
		for y := 0; y < i.rows; y++ {
			// As an optimization, we only write the fg color sequence id, and
			// column-row data once on the first cell. The terminal will handle
			// the rest.
			buf.WriteString(fgStyle)
			buf.WriteRune(kitty.Placeholder)
			buf.WriteRune(kitty.Diacritic(y))
			buf.WriteRune(kitty.Diacritic(0))
			if extra > 0 {
				buf.WriteRune(kitty.Diacritic(extra))
			}
			for x := 1; x < i.cols; x++ {
				buf.WriteString(fgStyle)
				buf.WriteRune(kitty.Placeholder)
			}
			if y < i.rows-1 {
				buf.WriteByte('\n')
			}
		}

		return buf.String()

	default:
		return ""
	}
}
