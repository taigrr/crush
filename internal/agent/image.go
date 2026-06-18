package agent

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

const (
	maxImageBytes     = 3_500_000
	maxImageDimension = 1568
)

// normalizeImageAttachments downscales oversized image attachments in place and
// returns the same slice. Clipboard pastes in particular arrive as lossless
// PNGs that balloon well past provider limits.
func normalizeImageAttachments(attachments []message.Attachment) []message.Attachment {
	for i, att := range attachments {
		if !att.IsImage() || len(att.Content) <= maxImageBytes {
			continue
		}

		data, err := shrinkImage(att.Content)

		if err != nil {
			slog.Warn("Failed to shrink oversized image attachment, sending as-is", "file", att.FileName, "bytes", len(att.Content), "error", err)
			continue
		}

		if len(data) > maxImageBytes {
			slog.Warn("Image still exceeds budget after downscaling", "file", att.FileName, "bytes", len(data))
		}

		slog.Debug("Shrank oversized image attachment", "file", att.FileName, "before", len(att.Content), "after", len(data))
		attachments[i].Content = data
		attachments[i].MimeType = "image/jpeg"
	}
	return attachments
}

// shrinkImage fits the image within maxImageDimension and re-encodes it as
// JPEG, lowering quality until the result fits within maxImageBytes. If no
// quality step fits, it returns the smallest encoding as a best effort.
func shrinkImage(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	img = imaging.Fit(img, maxImageDimension, maxImageDimension, imaging.Lanczos)

	var smallest []byte
	for _, quality := range []int{85, 70, 55, 40} {
		var buf bytes.Buffer

		if err := imaging.Encode(&buf, img, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
			return nil, err
		}

		smallest = buf.Bytes()

		if len(smallest) <= maxImageBytes {
			return smallest, nil
		}
	}
	return smallest, nil
}
