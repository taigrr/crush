package common

import (
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/styles"
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
