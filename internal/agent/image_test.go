package agent

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// bigPNG builds a large, noisy PNG that exceeds maxImageBytes, mimicking a
// clipboard-pasted photo re-encoded losslessly. Noise is incompressible, so
// the encoded result stays comfortably over the budget.
func bigPNG(t *testing.T) []byte {
	t.Helper()
	const (
		dim  = 1024
		seed = 1
	)
	rng := rand.New(rand.NewPCG(seed, ^uint64(seed)))
	img := image.NewRGBA(image.Rect(0, 0, dim, dim))

	for i := 0; i+8 <= len(img.Pix); i += 8 {
		binary.LittleEndian.PutUint64(img.Pix[i:], rng.Uint64())
	}

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.Greater(t, buf.Len(), maxImageBytes, "fixture must exceed budget")
	return buf.Bytes()
}

func TestNormalizeImageAttachments_ShrinksOversizedImage(t *testing.T) {
	t.Parallel()

	data := bigPNG(t)
	atts := []message.Attachment{
		{FileName: "IMG_0400.png", MimeType: "image/png", Content: data},
	}

	out := normalizeImageAttachments(atts)

	require.Len(t, out, 1)
	require.LessOrEqual(t, len(out[0].Content), maxImageBytes)
	require.Equal(t, "image/jpeg", out[0].MimeType)
	_, _, err := image.Decode(bytes.NewReader(out[0].Content))
	require.NoError(t, err)
}

func TestNormalizeImageAttachments_LeavesSmallAndNonImages(t *testing.T) {
	t.Parallel()

	small := []byte("not really an image but small")
	text := []byte("hello")
	atts := []message.Attachment{
		{FileName: "tiny.png", MimeType: "image/png", Content: small},
		{FileName: "notes.txt", MimeType: "text/plain", Content: text},
	}

	out := normalizeImageAttachments(atts)

	require.Equal(t, small, out[0].Content)
	require.Equal(t, "image/png", out[0].MimeType)
	require.Equal(t, text, out[1].Content)
}

func TestNormalizeImageAttachments_UndecodableImageSentAsIs(t *testing.T) {
	t.Parallel()

	garbage := bytes.Repeat([]byte{0xff}, maxImageBytes+1)
	atts := []message.Attachment{
		{FileName: "broken.png", MimeType: "image/png", Content: garbage},
	}

	out := normalizeImageAttachments(atts)

	require.Equal(t, garbage, out[0].Content)
	require.Equal(t, "image/png", out[0].MimeType)
}

func TestShrinkImage_BestEffortWhenOverBudget(t *testing.T) {
	t.Parallel()

	out, err := shrinkImage(bigPNG(t))

	require.NoError(t, err)
	require.NotEmpty(t, out)
	_, _, err = image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
}
