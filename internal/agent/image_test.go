package agent

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
)

const testProvider = "anthropic"

// testModel builds a Model for the anthropic provider with image
// support and no per-model override (so it resolves the provider-family
// defaults from catwalk).
func testModel() Model {
	return Model{
		CatwalkCfg: catwalk.Model{SupportsImages: true},
		ModelCfg:   config.SelectedModel{Provider: testProvider},
	}
}

// unsupportedModel is a model that does not accept images.
func unsupportedModel() Model {
	return Model{
		CatwalkCfg: catwalk.Model{SupportsImages: false},
		ModelCfg:   config.SelectedModel{Provider: testProvider},
	}
}

// noisyPNG builds an incompressible PNG of the given dimensions. Noise
// keeps the encoded result large so byte-budget paths are exercised.
func noisyPNG(t *testing.T, dim int) []byte {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, ^uint64(1)))
	img := image.NewRGBA(image.Rect(0, 0, dim, dim))
	for i := 0; i+8 <= len(img.Pix); i += 8 {
		binary.LittleEndian.PutUint64(img.Pix[i:], rng.Uint64())
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// solidPNG builds a trivially-compressible PNG of the given dimensions
// (small bytes, large pixels) to isolate the pixel-budget path.
func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func decodeDims(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	return cfg.Width, cfg.Height
}

func imageMsg(data []byte) message.Message {
	return message.Message{
		Role:  message.User,
		Parts: []message.ContentPart{message.BinaryContent{MIMEType: "image/png", Data: data}},
	}
}

func TestFitImageAttachments_NoImagesOrUnsupported(t *testing.T) {
	t.Parallel()

	text := []message.Attachment{{FileName: "a.txt", MimeType: "text/plain", Content: []byte("hi")}}

	// No images: unchanged.
	out, err := fitImageAttachments(testModel(), nil, text)
	require.NoError(t, err)
	require.Equal(t, text, out)

	// Model without image support: images pass through untouched.
	img := []message.Attachment{{FileName: "x.png", MimeType: "image/png", Content: noisyPNG(t, 64)}}
	out, err = fitImageAttachments(unsupportedModel(), nil, img)
	require.NoError(t, err)
	require.Equal(t, img, out)
}

func TestFitImageAttachments_ClampsPerImageDimension(t *testing.T) {
	t.Parallel()

	// 4000px image, no history. Anthropic per-image cap is 1568*0.95.
	atts := []message.Attachment{
		{FileName: "big.png", MimeType: "image/png", Content: solidPNG(t, 4000, 3000)},
	}
	out, err := fitImageAttachments(testModel(), nil, atts)
	require.NoError(t, err)

	w, h := decodeDims(t, out[0].Content)
	limit := imageLimitsFor(testModel()).maxImageDimension
	require.LessOrEqual(t, w, limit)
	require.LessOrEqual(t, h, limit)
	require.Equal(t, "image/jpeg", out[0].MimeType)
	// Aspect ratio preserved (4:3).
	require.InDelta(t, 4.0/3.0, float64(w)/float64(h), 0.05)
}

func TestFitImageAttachments_ProportionalMultiImageDownscale(t *testing.T) {
	t.Parallel()

	// Two images that individually fit the per-image dimension cap but
	// together (added to a nearly-full history) overflow the aggregate
	// pixel budget. They must be downscaled by a shared factor that
	// preserves the 2:1 size ratio between them so both stay readable.
	big := solidPNG(t, 1400, 1400)
	small := solidPNG(t, 700, 700)
	atts := []message.Attachment{
		{FileName: "big.png", MimeType: "image/png", Content: big},
		{FileName: "small.png", MimeType: "image/png", Content: small},
	}

	// Fill most of the aggregate pixel budget with history so only a
	// small pixel allowance remains for the current message, forcing the
	// proportional downscale path. Solid PNGs keep history bytes tiny so
	// the byte budget stays out of the way.
	limits := imageLimitsFor(testModel())
	var hist []message.Message
	var histPixels int64
	for histPixels+4_000_000 <= limits.maxAggregatePixels-2_000_000 {
		hist = append(hist, imageMsg(solidPNG(t, 2000, 2000)))
		histPixels += 2000 * 2000
	}

	out, err := fitImageAttachments(testModel(), hist, atts)
	require.NoError(t, err)

	bw, bh := decodeDims(t, out[0].Content)
	sw, sh := decodeDims(t, out[1].Content)

	// Aggregate pixel budget respected (history + current).
	total := histPixels + int64(bw)*int64(bh) + int64(sw)*int64(sh)
	require.LessOrEqual(t, total, limits.maxAggregatePixels)

	// Both were actually downscaled (proportional path ran).
	require.Less(t, bw, 1400)

	// Relative proportion preserved: big stays ~2x the small on each edge.
	require.InDelta(t, 2.0, float64(bw)/float64(sw), 0.15)
	require.InDelta(t, 2.0, float64(bh)/float64(sh), 0.15)
}

func TestFitImageAttachments_BlocksWhenHistoryFull(t *testing.T) {
	t.Parallel()

	// History already exceeds the aggregate byte budget.
	limits := imageLimitsFor(testModel())
	hist := make([]message.Message, 0)
	var acc int
	for acc < limits.maxAggregateBytes {
		data := noisyPNG(t, 1200)
		hist = append(hist, imageMsg(data))
		acc += len(data)
	}

	atts := []message.Attachment{
		{FileName: "new.png", MimeType: "image/png", Content: noisyPNG(t, 256)},
	}
	_, err := fitImageAttachments(testModel(), hist, atts)
	require.ErrorIs(t, err, ErrImageBudgetExceeded)
}

func TestFitImageAttachments_BlocksWhenTooManyImages(t *testing.T) {
	t.Parallel()

	limits := imageLimitsFor(testModel())
	// One more than the count cap, split across history and current.
	hist := make([]message.Message, limits.maxImages)
	for i := range hist {
		hist[i] = imageMsg(solidPNG(t, 16, 16))
	}
	atts := []message.Attachment{
		{FileName: "extra.png", MimeType: "image/png", Content: solidPNG(t, 16, 16)},
	}
	_, err := fitImageAttachments(testModel(), hist, atts)
	require.ErrorIs(t, err, ErrImageBudgetExceeded)
}

func TestFitImageAttachments_FitsAlongsideHistory(t *testing.T) {
	t.Parallel()

	// Modest history leaves room; a large current image is downscaled to
	// fit the remaining budget without erroring.
	hist := []message.Message{imageMsg(solidPNG(t, 1000, 1000))}
	atts := []message.Attachment{
		{FileName: "new.png", MimeType: "image/png", Content: solidPNG(t, 6000, 6000)},
	}

	out, err := fitImageAttachments(testModel(), hist, atts)
	require.NoError(t, err)

	limits := imageLimitsFor(testModel())
	hu := historyImageUsage(hist)
	w, h := decodeDims(t, out[0].Content)
	require.LessOrEqual(t, hu.pixels+int64(w)*int64(h), limits.maxAggregatePixels)
	require.LessOrEqual(t, len(out[0].Content), limits.maxImageBytes)
}

func TestFitImageAttachments_UndecodableLeftAsIs(t *testing.T) {
	t.Parallel()

	garbage := bytes.Repeat([]byte{0xff}, 1024)
	atts := []message.Attachment{
		{FileName: "broken.png", MimeType: "image/png", Content: garbage},
	}
	out, err := fitImageAttachments(testModel(), nil, atts)
	require.NoError(t, err)
	require.Equal(t, garbage, out[0].Content)
	require.Equal(t, "image/png", out[0].MimeType)
}

func TestImageLimitsFor_BufferApplied(t *testing.T) {
	t.Parallel()

	raw := catwalk.DefaultImageLimits(catwalk.TypeAnthropic)
	eff := imageLimitsFor(testModel())
	require.Equal(t, int(float64(raw.MaxBytesPerImage)*limitBuffer), eff.maxImageBytes)
	require.Equal(t, int(float64(raw.MaxLongEdge)*limitBuffer), eff.maxImageDimension)
	require.Less(t, int64(eff.maxAggregateBytes), raw.MaxAggregateBytes)
}

func TestImageLimitsFor_PerModelOverride(t *testing.T) {
	t.Parallel()

	// A model carrying a per-model long-edge override (e.g. Claude
	// Opus 4.7+) gets the larger dimension; other limits stay at the
	// provider-family default.
	m := Model{
		CatwalkCfg: catwalk.Model{SupportsImages: true, Image: catwalk.ImageLimits{MaxLongEdge: 2576}},
		ModelCfg:   config.SelectedModel{Provider: testProvider},
	}
	eff := imageLimitsFor(m)
	var edge int64 = 2576
	require.Equal(t, int(float64(edge)*limitBuffer), eff.maxImageDimension)

	def := imageLimitsFor(testModel())
	require.Greater(t, eff.maxImageDimension, def.maxImageDimension)
}

func TestHistoryImageUsage_CountsOnlyImages(t *testing.T) {
	t.Parallel()

	hist := []message.Message{
		imageMsg(solidPNG(t, 100, 100)),
		imageMsg(solidPNG(t, 200, 50)),
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "no image"}}},
	}
	u := historyImageUsage(hist)
	require.Equal(t, 2, u.count)
	require.Equal(t, int64(100*100+200*50), u.pixels)
	require.Positive(t, u.bytes)
}

func TestEncodeJPEGWithinBytes_BestEffort(t *testing.T) {
	t.Parallel()

	img, _, err := image.Decode(bytes.NewReader(noisyPNG(t, 800)))
	require.NoError(t, err)

	// Impossibly small budget: returns the smallest encoding, no error.
	out, err := encodeJPEGWithinBytes(img, 10)
	require.NoError(t, err)
	require.NotEmpty(t, out)
	_, _, err = image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
}
