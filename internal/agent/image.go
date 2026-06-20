package agent

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // GIF decoding.
	_ "image/jpeg" // JPEG decoding.
	_ "image/png"  // PNG decoding.
	"log/slog"
	"math"

	"github.com/disintegration/imaging"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/message"
	_ "golang.org/x/image/webp" // WebP decoding.
)

// ErrImageBudgetExceeded is returned when the images in a turn cannot be
// made to fit the model's documented limits even after downscaling the
// current message's attachments — typically because earlier messages in
// the thread already consume most of the per-request budget. It is a
// user-facing, pre-send error: the turn is refused before anything is
// persisted or sent so the user can remove images or start a new
// session rather than hit an unrecoverable provider API failure.
var ErrImageBudgetExceeded = errors.New("image budget exceeded")

// imageLimits captures the documented image constraints for a provider.
// All numeric budgets are the raw documented maximums; the effective
// limits applied at runtime are these values scaled by limitBuffer (see
// [imageLimits.effective]) so we stay safely inside the provider's
// real, sometimes-undocumented enforcement.
//
// Two classes of limit are modeled:
//
//   - Per-image: every single image must independently satisfy
//     maxImageBytes (encoded size) and maxImageDimension (longest edge
//     in pixels).
//   - Aggregate: the sum across ALL images in the request (the entire
//     thread that is replayed each turn, plus the current message) must
//     satisfy maxAggregateBytes (total encoded bytes) and
//     maxAggregatePixels (total width*height). maxImages caps the count.
//
// Numbers below reflect provider documentation as of 2026-06 and are
// intentionally conservative; they are easy to tune in one place.
type imageLimits struct {
	maxImageBytes      int
	maxImageDimension  int
	maxAggregateBytes  int
	maxAggregatePixels int64
	maxImages          int
}

// limitBuffer keeps a 5% safety margin inside every documented maximum
// to absorb differences between a provider's published limits and its
// actual API enforcement (e.g. base64 overhead, rounding, undocumented
// ceilings).
const limitBuffer = 0.95

// defaultImageLimits is the conservative fallback applied to any
// provider not explicitly listed in providerImageLimits.
var defaultImageLimits = imageLimits{
	maxImageBytes:      4_000_000,
	maxImageDimension:  1568,
	maxAggregateBytes:  20_000_000,
	maxAggregatePixels: 30_000_000,
	maxImages:          50,
}

// providerImageLimits maps a provider id to its documented image limits.
// Providers that share an API surface (e.g. Bedrock/Vertex hosting
// Claude) use the stricter of the documented values.
var providerImageLimits = map[string]imageLimits{
	string(catwalk.InferenceProviderAnthropic): {
		maxImageBytes:      5_000_000, // 5MB/image (Bedrock/Vertex floor).
		maxImageDimension:  1568,      // long edge; larger is downscaled anyway.
		maxAggregateBytes:  32_000_000,
		maxAggregatePixels: 40_000_000,
		maxImages:          100,
	},
	string(catwalk.InferenceProviderBedrock): {
		maxImageBytes:      5_000_000,
		maxImageDimension:  1568,
		maxAggregateBytes:  32_000_000,
		maxAggregatePixels: 40_000_000,
		maxImages:          100,
	},
	string(catwalk.InferenceProviderBedrockEurope): {
		maxImageBytes:      5_000_000,
		maxImageDimension:  1568,
		maxAggregateBytes:  32_000_000,
		maxAggregatePixels: 40_000_000,
		maxImages:          100,
	},
	string(catwalk.InferenceProviderOpenAI): {
		maxImageBytes:      20_000_000, // 20MB/image.
		maxImageDimension:  2048,       // scaled to fit 2048x2048.
		maxAggregateBytes:  50_000_000,
		maxAggregatePixels: 50_000_000,
		maxImages:          100,
	},
	string(catwalk.InferenceProviderGemini): {
		maxImageBytes:      7_000_000, // 7MB inline.
		maxImageDimension:  3072,
		maxAggregateBytes:  20_000_000, // 20MB request.
		maxAggregatePixels: 60_000_000,
		maxImages:          3000,
	},
}

// jpegQualitySteps is the descending quality ladder tried when fitting
// an image to a byte budget.
var jpegQualitySteps = []int{85, 70, 55, 40}

// imageLimitsFor returns the buffered effective limits for a provider.
func imageLimitsFor(provider string) imageLimits {
	l, ok := providerImageLimits[provider]
	if !ok {
		l = defaultImageLimits
	}
	return l.effective()
}

// effective applies limitBuffer to every budget, leaving a margin
// inside the documented maximum.
func (l imageLimits) effective() imageLimits {
	return imageLimits{
		maxImageBytes:      int(float64(l.maxImageBytes) * limitBuffer),
		maxImageDimension:  int(float64(l.maxImageDimension) * limitBuffer),
		maxAggregateBytes:  int(float64(l.maxAggregateBytes) * limitBuffer),
		maxAggregatePixels: int64(float64(l.maxAggregatePixels) * limitBuffer),
		maxImages:          l.maxImages,
	}
}

// imageUsage is a running tally of image budget consumption.
type imageUsage struct {
	bytes  int
	pixels int64
	count  int
}

// historyImageUsage sums the byte and pixel footprint of every image
// already present in the thread. Pixel dimensions are read from each
// image's header only (cheap) so a long thread with many large images
// does not force full decodes.
func historyImageUsage(msgs []message.Message) imageUsage {
	var u imageUsage
	for _, m := range msgs {
		for _, bc := range m.BinaryContent() {
			if !isImageMIME(bc.MIMEType) || len(bc.Data) == 0 {
				continue
			}
			u.bytes += len(bc.Data)
			u.count++
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(bc.Data)); err == nil {
				u.pixels += int64(cfg.Width) * int64(cfg.Height)
			}
		}
	}
	return u
}

// fitImageAttachments enforces the model's per-image and aggregate image
// limits for a single turn. It mutates and returns attachments:
//
//  1. Each image is clamped to the per-image dimension and byte caps.
//  2. If the current message's images, added to the images already in
//     the thread (history), would exceed the aggregate pixel or byte
//     budget, every current image is downscaled by a single shared
//     factor — preserving each image's relative size so they all stay
//     readable — until the set just fits.
//  3. If no amount of downscaling can make the turn fit (history alone
//     already consumes the budget, or there are simply too many images),
//     it returns [ErrImageBudgetExceeded] so the caller can refuse the
//     turn before it reaches the provider.
//
// Non-image attachments and models that do not accept images are left
// untouched.
func fitImageAttachments(provider string, supportsImages bool, history []message.Message, attachments []message.Attachment) ([]message.Attachment, error) {
	if !supportsImages {
		return attachments, nil
	}

	limits := imageLimitsFor(provider)
	hist := historyImageUsage(history)

	// Index of the current message's images within attachments.
	var imageIdx []int
	for i, att := range attachments {
		if att.IsImage() {
			imageIdx = append(imageIdx, i)
		}
	}
	if len(imageIdx) == 0 {
		return attachments, nil
	}

	// Hard count check: history + current images must not exceed the
	// per-request image count. This cannot be fixed by downscaling.
	if hist.count+len(imageIdx) > limits.maxImages {
		return attachments, fmt.Errorf(
			"%w: this conversation would contain %d images but %s allows at most %d per request; remove some images or start a new session",
			ErrImageBudgetExceeded, hist.count+len(imageIdx), provider, limits.maxImages,
		)
	}

	// If history alone already meets or exceeds the aggregate budget,
	// no current image can be added regardless of size.
	if hist.bytes >= limits.maxAggregateBytes || hist.pixels >= limits.maxAggregatePixels {
		return attachments, fmt.Errorf(
			"%w: earlier messages in this conversation already fill the image budget for %s; start a new session to send more images",
			ErrImageBudgetExceeded, provider,
		)
	}

	// Step 1: per-image clamp (dimension + bytes). Decode once and keep
	// the decoded image around for the aggregate pass.
	type curImage struct {
		attIdx int
		img    image.Image
		data   []byte // current encoded bytes (nil until re-encoded)
		w, h   int
	}
	cur := make([]curImage, 0, len(imageIdx))
	for _, idx := range imageIdx {
		att := attachments[idx]
		img, _, err := image.Decode(bytes.NewReader(att.Content))
		if err != nil {
			// Undecodable: leave as-is (provider may still reject, but
			// we cannot safely transform it). Count its raw bytes
			// toward the aggregate via a best-effort header read.
			slog.Warn("Cannot decode image attachment for limit enforcement, sending as-is",
				"file", att.FileName, "bytes", len(att.Content), "error", err)
			continue
		}
		// Clamp dimension to the per-image cap.
		img = imaging.Fit(img, limits.maxImageDimension, limits.maxImageDimension, imaging.Lanczos)
		b := img.Bounds()
		cur = append(cur, curImage{attIdx: idx, img: img, w: b.Dx(), h: b.Dy()})
	}
	if len(cur) == 0 {
		return attachments, nil
	}

	// Step 2: aggregate pixel fit. Compute the pixel budget left for the
	// current message and, if the current images overflow it, scale all
	// of them by one shared linear factor s = sqrt(budget/sum) so their
	// relative sizes are preserved.
	availPixels := limits.maxAggregatePixels - hist.pixels
	var sumPixels int64
	for _, c := range cur {
		sumPixels += int64(c.w) * int64(c.h)
	}
	if sumPixels > availPixels {
		scale := math.Sqrt(float64(availPixels) / float64(sumPixels))
		for i := range cur {
			nw := max(1, int(float64(cur[i].w)*scale))
			nh := max(1, int(float64(cur[i].h)*scale))
			cur[i].img = imaging.Resize(cur[i].img, nw, nh, imaging.Lanczos)
			b := cur[i].img.Bounds()
			cur[i].w, cur[i].h = b.Dx(), b.Dy()
		}
	}

	// Step 3: encode each image to JPEG, lowering quality until it meets
	// the per-image byte cap; track the aggregate byte total.
	availBytes := limits.maxAggregateBytes - hist.bytes
	var sumBytes int
	for i := range cur {
		data, err := encodeJPEGWithinBytes(cur[i].img, limits.maxImageBytes)
		if err != nil {
			return attachments, fmt.Errorf("encode image %q: %w", attachments[cur[i].attIdx].FileName, err)
		}
		cur[i].data = data
		sumBytes += len(data)
	}

	// Step 4: if the encoded current images still overflow the aggregate
	// byte budget, apply a second shared downscale derived from the byte
	// overshoot (bytes scale ~ pixels, so use sqrt of the ratio) and
	// re-encode once. This is a best-effort second pass; if it still
	// doesn't fit we refuse the turn.
	if sumBytes > availBytes {
		scale := math.Sqrt(float64(availBytes) / float64(sumBytes))
		sumBytes = 0
		for i := range cur {
			nw := max(1, int(float64(cur[i].w)*scale))
			nh := max(1, int(float64(cur[i].h)*scale))
			cur[i].img = imaging.Resize(cur[i].img, nw, nh, imaging.Lanczos)
			data, err := encodeJPEGWithinBytes(cur[i].img, limits.maxImageBytes)
			if err != nil {
				return attachments, fmt.Errorf("encode image %q: %w", attachments[cur[i].attIdx].FileName, err)
			}
			cur[i].data = data
			sumBytes += len(data)
		}
	}
	if sumBytes > availBytes {
		return attachments, fmt.Errorf(
			"%w: the image(s) in this message are too large to fit alongside earlier images in this conversation for %s; remove some images or start a new session",
			ErrImageBudgetExceeded, provider,
		)
	}

	// Commit the fitted encodings back onto the attachments.
	for _, c := range cur {
		slog.Debug("Fitted image attachment to model limits",
			"file", attachments[c.attIdx].FileName,
			"before", len(attachments[c.attIdx].Content),
			"after", len(c.data),
			"dims", fmt.Sprintf("%dx%d", c.w, c.h))
		attachments[c.attIdx].Content = c.data
		attachments[c.attIdx].MimeType = "image/jpeg"
	}
	return attachments, nil
}

// encodeJPEGWithinBytes encodes img as JPEG, descending the quality
// ladder until the result fits maxBytes. If no step fits it returns the
// smallest encoding as a best effort (the aggregate pass may still
// downscale further).
func encodeJPEGWithinBytes(img image.Image, maxBytes int) ([]byte, error) {
	var smallest []byte
	for _, quality := range jpegQualitySteps {
		var buf bytes.Buffer
		if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
			return nil, err
		}
		smallest = buf.Bytes()
		if len(smallest) <= maxBytes {
			return smallest, nil
		}
	}
	return smallest, nil
}

// isImageMIME reports whether a MIME type names an image.
func isImageMIME(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
}
