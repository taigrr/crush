package dialog

import (
	"hash/fnv"
	"image"
	_ "image/jpeg" // register JPEG format
	_ "image/png"  // register PNG format
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/ui/common"
	fimage "github.com/charmbracelet/crush/internal/ui/image"
	uv "github.com/charmbracelet/ultraviolet"
)

var (
	transmittedImages = map[uint64]struct{}{}
	transmittedMutex  sync.RWMutex
)

// FilePickerID is the identifier for the FilePicker dialog.
const FilePickerID = "filepicker"

// FilePicker is a dialog that allows users to select files or directories.
type FilePicker struct {
	com *common.Common

	width                       int
	imgPrevWidth, imgPrevHeight int
	imageCaps                   *fimage.Capabilities

	img             *fimage.Image
	fp              filepicker.Model
	help            help.Model
	previewingImage bool // indicates if an image is being previewed

	km struct {
		Select,
		Down,
		Up,
		Forward,
		Backward,
		Navigate,
		Close key.Binding
	}
}

var _ Dialog = (*FilePicker)(nil)

// NewFilePicker creates a new [FilePicker] dialog.
func NewFilePicker(com *common.Common) (*FilePicker, Action) {
	f := new(FilePicker)
	f.com = com

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	f.help = help

	f.km.Select = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "accept"),
	)
	f.km.Down = key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("down/j", "move down"),
	)
	f.km.Up = key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("up/k", "move up"),
	)
	f.km.Forward = key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("right/l", "move forward"),
	)
	f.km.Backward = key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("left/h", "move backward"),
	)
	f.km.Navigate = key.NewBinding(
		key.WithKeys("right", "l", "left", "h", "up", "k", "down", "j"),
		key.WithHelp("↑↓←→", "navigate"),
	)
	f.km.Close = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "close/exit"),
	)

	fp := filepicker.New()
	fp.AllowedTypes = []string{".jpg", ".jpeg", ".png"}
	fp.ShowPermissions = false
	fp.ShowSize = false
	fp.AutoHeight = false
	fp.Styles = com.Styles.FilePicker
	fp.Cursor = ""
	fp.CurrentDirectory = f.WorkingDir()

	f.fp = fp

	return f, ActionCmd{f.fp.Init()}
}

// SetImageCapabilities sets the image capabilities for the [FilePicker].
func (f *FilePicker) SetImageCapabilities(caps *fimage.Capabilities) {
	f.imageCaps = caps
}

// WorkingDir returns the current working directory of the [FilePicker].
func (f *FilePicker) WorkingDir() string {
	wd := f.com.Config().WorkingDir()
	if len(wd) > 0 {
		return wd
	}

	cwd, err := os.Getwd()
	if err != nil {
		return home.Dir()
	}

	return cwd
}

// SetWindowSize sets the desired size of the [FilePicker] dialog window.
func (f *FilePicker) SetWindowSize(width, height int) {
	f.width = width
	f.imgPrevWidth = width/2 - f.com.Styles.Dialog.ImagePreview.GetHorizontalFrameSize()
	// Use square preview for simplicity same size as width
	f.imgPrevHeight = width/2 - f.com.Styles.Dialog.ImagePreview.GetVerticalFrameSize()
	f.fp.SetHeight(height)
	innerWidth := width - f.com.Styles.Dialog.View.GetHorizontalFrameSize()
	styles := f.com.Styles.FilePicker
	styles.File = styles.File.Width(innerWidth)
	styles.Directory = styles.Directory.Width(innerWidth)
	styles.Selected = styles.Selected.PaddingLeft(1).Width(innerWidth)
	styles.DisabledSelected = styles.DisabledSelected.PaddingLeft(1).Width(innerWidth)
	f.fp.Styles = styles
}

// ShortHelp returns the short help key bindings for the [FilePicker] dialog.
func (f *FilePicker) ShortHelp() []key.Binding {
	return []key.Binding{
		f.km.Navigate,
		f.km.Select,
		f.km.Close,
	}
}

// FullHelp returns the full help key bindings for the [FilePicker] dialog.
func (f *FilePicker) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{
			f.km.Select,
			f.km.Down,
			f.km.Up,
			f.km.Forward,
		},
		{
			f.km.Backward,
			f.km.Close,
		},
	}
}

// ID returns the identifier of the [FilePicker] dialog.
func (f *FilePicker) ID() string {
	return FilePickerID
}

// Init implements the [Dialog] interface.
func (f *FilePicker) Init() tea.Cmd {
	return f.fp.Init()
}

// HandleMsg updates the [FilePicker] dialog based on the given message.
func (f *FilePicker) HandleMsg(msg tea.Msg) Action {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, f.km.Close):
			return ActionClose{}
		}
	}

	var cmd tea.Cmd
	f.fp, cmd = f.fp.Update(msg)
	if selFile := f.fp.HighlightedPath(); selFile != "" {
		var allowed bool
		for _, allowedExt := range f.fp.AllowedTypes {
			if strings.HasSuffix(strings.ToLower(selFile), allowedExt) {
				allowed = true
				break
			}
		}

		f.previewingImage = allowed
		if allowed {
			id := uniquePathID(selFile)

			transmittedMutex.RLock()
			_, transmitted := transmittedImages[id]
			transmittedMutex.RUnlock()
			if !transmitted {
				img, err := loadImage(selFile)
				if err != nil {
					f.previewingImage = false
				}

				timg, err := fimage.New(selFile, img, f.imgPrevWidth, f.imgPrevHeight)
				if err != nil {
					f.previewingImage = false
				}

				f.img = timg
				if err == nil {
					cmds = append(cmds, f.img.Transmit())
					transmittedMutex.Lock()
					transmittedImages[id] = struct{}{}
					transmittedMutex.Unlock()
				}
			}
		}
	}
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	return ActionCmd{tea.Batch(cmds...)}
}

// Draw renders the [FilePicker] dialog as a string.
func (f *FilePicker) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := f.com.Styles
	titleStyle := f.com.Styles.Dialog.Title
	dialogStyle := f.com.Styles.Dialog.View
	header := common.DialogTitle(t, "Add Image",
		max(0, f.width-dialogStyle.GetHorizontalFrameSize()-
			titleStyle.GetHorizontalFrameSize()))
	files := strings.TrimSpace(f.fp.View())
	filesHeight := f.fp.Height()
	imgPreview := t.Dialog.ImagePreview.Render(f.imagePreview())
	view := HeaderInputListHelpView(t, f.width, filesHeight, header, imgPreview, files, f.help.View(f))
	DrawCenter(scr, area, view)
	return nil
}

// imagePreview returns the image preview section of the [FilePicker] dialog.
func (f *FilePicker) imagePreview() string {
	if !f.previewingImage || f.img == nil {
		// TODO: Cache this?
		var sb strings.Builder
		for y := 0; y < f.imgPrevHeight; y++ {
			for x := 0; x < f.imgPrevWidth; x++ {
				sb.WriteRune('╱')
			}
			if y < f.imgPrevHeight-1 {
				sb.WriteRune('\n')
			}
		}
		return sb.String()
	}

	return f.img.Render()
}

func uniquePathID(path string) uint64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, path)
	return h.Sum64()
}

func loadImage(path string) (img image.Image, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err = image.Decode(file)
	if err != nil {
		return nil, err
	}

	return img, nil
}
