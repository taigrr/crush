package common

import (
	"image"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

// Common defines common UI options and configurations.
type Common struct {
	Config *config.Config
	Styles styles.Styles
}

// DefaultCommon returns the default common UI configurations.
func DefaultCommon(cfg *config.Config) *Common {
	return &Common{
		Config: cfg,
		Styles: styles.DefaultStyles(),
	}
}

// CenterRect returns a new [Rectangle] centered within the given area with the
// specified width and height.
func CenterRect(area uv.Rectangle, width, height int) uv.Rectangle {
	centerX := area.Min.X + area.Dx()/2
	centerY := area.Min.Y + area.Dy()/2
	minX := centerX - width/2
	minY := centerY - height/2
	maxX := minX + width
	maxY := minY + height
	return image.Rect(minX, minY, maxX, maxY)
}
