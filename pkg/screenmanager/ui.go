package screenmanager

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/xproto"
)

// Screens is a custom widget presenting the screen-management UI, together with
// its lifecycle controls and change notifications. Obtain one from New and add
// it to a container or window like any other Fyne widget.
type Screens struct {
	widget.BaseWidget

	// OnConfigurationChanged, if set, is invoked whenever the screen or layout
	// configuration changes — for example a monitor being plugged in or removed,
	// or a resolution or position change. It is called on the main goroutine
	// after the internal state and UI have been refreshed, so it is safe to
	// query the current configuration or update Fyne widgets from within it.
	OnConfigurationChanged func()

	content fyne.CanvasObject
	closeFn func()
}

// CreateRenderer returns the renderer for the widget, drawing the screen-manager
// UI built by makeUI as its content. It implements fyne.Widget.
func (s *Screens) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

// Close releases the X resources held by the screen manager. Call it once the
// UI is no longer needed, for example when the owning window closes.
func (s *Screens) Close() {
	if s.closeFn != nil {
		s.closeFn()
	}
}

func New(w fyne.Window) *Screens {
	g := newScreensGUI()
	s := &Screens{content: g.makeUI()}
	s.ExtendBaseWidget(s)

	c, err := xgb.NewConn()
	if err != nil {
		dialog.ShowError(err, w)
	} else {
		conn = c
		err = randr.Init(conn)
		root = xproto.Setup(conn).DefaultScreen(conn).Root

		// Start listening for monitor hotplug / configuration changes
		// before the first load so no events are missed.
		g.setup(w, s)
		g.loadScreens(w)
	}

	s.closeFn = func() {
		if conn != nil {
			conn.Close()
		}
	}
	return s
}
