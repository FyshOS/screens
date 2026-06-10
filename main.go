package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/FyshOS/screens/pkg/screenmanager"
)

func main() {
	a := app.New()
	loadTheme(a)

	w := a.NewWindow("Screens")
	w.Resize(fyne.NewSize(376, 263))
	screens, done := screenmanager.NewGUI(w)
	g := newGUI()
	ui := g.makeUI()
	g.screens.Objects = []fyne.CanvasObject{screens}
	g.setupActions()
	w.SetContent(ui)

	w.ShowAndRun()
	done()
}

// here you can add some button / callbacks code using widget IDs
func (g *gui) setupActions() {
}
