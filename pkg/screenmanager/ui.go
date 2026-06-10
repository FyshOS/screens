package screenmanager

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/xproto"
)

func NewGUI(w fyne.Window) (fyne.CanvasObject, func()) {
	g := newScreensGUI()
	ui := g.makeUI()

	c, err := xgb.NewConn()
	if err != nil {
		dialog.ShowError(err, w)
	} else {
		conn = c
		err = randr.Init(conn)
		root = xproto.Setup(conn).DefaultScreen(conn).Root

		// Start listening for monitor hotplug / configuration changes
		// before the first load so no events are missed.
		g.setup(w)
		g.loadScreens(w)
	}

	return ui, func() {
		if conn != nil {
			conn.Close()
		}
	}
}
