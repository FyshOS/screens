package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/xproto"
)

var (
	conn *xgb.Conn
	root xproto.Window

	state State
)

func main() {
	a := app.New()
	loadTheme(a)

	g := newGUI()
	w := g.makeWindow(a)

	c, err := xgb.NewConn()
	if err != nil {
		dialog.ShowError(err, w)
	} else {
		conn = c
		err = randr.Init(conn)
		root = xproto.Setup(conn).DefaultScreen(conn).Root

		g.loadScreens(w)
	}

	g.setupActions(w)
	w.ShowAndRun()
	if conn != nil {
		conn.Close()
	}
}

// here you can add some button / callbacks code using widget IDs
func (g *gui) setupActions(w fyne.Window) {
	if conn == nil {
		return
	}

	go func() {
		err := randr.SelectInputChecked(conn, root,
			randr.NotifyMaskScreenChange|
				randr.NotifyMaskCrtcChange|
				randr.NotifyMaskOutputChange).Check()
		if err != nil {
			fyne.LogError("Could not connect to Xserver for events", err)
			return
		}

		for {
			ev, err := conn.WaitForEvent()
			if err != nil {
				fyne.LogError("Error waiting for Xserver event", err)
				continue
			}

			switch ev.(type) {
			case randr.ScreenChangeNotifyEvent:
				fyne.Do(func() {
					g.loadScreens(w)
				})
			}
		}
	}()
}

func (g *gui) loadScreens(w fyne.Window) {
	g.connected.RemoveAll()
	g.offline.RemoveAll()

	resources, err := randr.GetScreenResources(conn, root).Reply()
	if err != nil {
		dialog.ShowError(err, w)
		return
	}

	newState := State{
		configTimestamp: resources.ConfigTimestamp,
	}
	for _, mode := range resources.Modes {
		newState.modes = append(newState.modes, Mode{id: randr.Mode(mode.Id), Width: mode.Width, Height: mode.Height})
	}

	for _, crtc := range resources.Crtcs {
		info, err := randr.GetCrtcInfo(conn, crtc, resources.ConfigTimestamp).Reply()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		var m *Mode
		for _, m2 := range newState.modes {
			if m2.id == info.Mode {
				m = &m2
				break
			}
		}

		ctrl := Controller{id: crtc, Mode: m, Outputs: info.Outputs, X: info.X, Y: info.Y}
		newState.controllers = append(newState.controllers, ctrl)
	}

	first := true
	for _, screen := range resources.Outputs {
		info, err := randr.GetOutputInfo(conn, screen, 0).Reply()
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		if info.Connection != randr.ConnectionConnected {
			continue
		}

		var m *Mode
		var modes []Mode
		if len(info.Modes) == 0 {
			continue
		} else {
			for _, mid := range info.Modes {
				for _, m2 := range newState.modes {
					if m2.id == mid {
						modes = append(modes, m2)
					}
				}
			}
			for _, c2 := range newState.controllers {
				if c2.id == info.Crtc {
					m = c2.Mode

					break
				}
			}
		}

		newOutput := Output{id: screen, ctrl: info.Crtc, CurrentMode: m, Modes: modes, Name: string(info.Name)}
		newState.outputs = append(newState.outputs, newOutput)
	}
	state = newState

	for i, output := range newState.outputs {
		if output.CurrentMode == nil {
			other := widget.NewCheck(output.Name, func(on bool) {
				if !on {
					return
				}

				g.activate(state.outputs[i])
			})

			g.offline.Add(other)
			return
		}

		panel := &screenGui{}
		ui := panel.makeUI()
		panel.name.SetText(output.Name)

		panel.active.SetChecked(true)
		if first {
			panel.active.Disable()
			panel.primary.SetChecked(true)

			first = false
		}

		panel.active.OnChanged = func(on bool) {
			if on {
				return
			}

			g.deactivate(state.outputs[i])
		}

		modes := map[string]Mode{}
		panel.resolution.OnChanged = nil
		var options []string
		for _, m := range output.Modes {
			mode := fmt.Sprintf("%dx%d", m.Width, m.Height)

			found := false
			for _, added := range options {
				if added == mode {
					found = true
					break
				}
			}
			if !found {
				options = append(options, mode)
				modes[mode] = m
			}
		}
		panel.resolution.SetOptions(options)
		panel.screen.SetMinSize(fyne.NewSize(150, 100))

		if output.CurrentMode != nil {
			selected := fmt.Sprintf("%dx%d", output.CurrentMode.Width, output.CurrentMode.Height)
			panel.resolution.SetSelected(selected)
			panel.screen.Aspect = float32(output.CurrentMode.Width) / float32(output.CurrentMode.Height)
		}
		panel.resolution.OnChanged = func(m string) {
			_, err := randr.SetCrtcConfig(conn, output.ctrl, 0, state.configTimestamp,
				0, 0, modes[m].id, randr.RotationRotate0, []randr.Output{output.id}).Reply()
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to set resolution: %w", err), w)
				return
			}

			panel.screen.Aspect = float32(output.CurrentMode.Width) / float32(output.CurrentMode.Height)
			panel.screen.Refresh()
		}

		panel.label.Alignment = fyne.TextAlignCenter
		panel.label.SetText(output.Name)

		g.connected.Add(ui)
	}
}

func (g *gui) activate(out Output) {
	x := int16(0) // just assume RightOf TODO configure
	var ctrl *Controller
	for _, c := range state.controllers {
		if c.Mode == nil {
			ctrl = &c
		} else {
			x += int16(c.Mode.Width)
		}
	}
	if ctrl == nil {
		fyne.LogError("Cannot find an available controller!", nil)
		return
	}

	// Use config timestamp and check for errors
	_, err := randr.SetCrtcConfig(conn, (*ctrl).id, 0, state.configTimestamp,
		x, 0, out.Modes[0].id, randr.RotationRotate0, []randr.Output{out.id}).Reply()
	if err != nil {
		fyne.LogError("Failed to activate output", err)
	}
}

func (g *gui) deactivate(out Output) {
	var ctrl *Controller
	for _, c := range state.controllers {
		if c.Mode == out.CurrentMode {
			ctrl = &c
			break
		}
	}
	if ctrl == nil {
		fyne.LogError("Cannot find matching controller!", nil)
		return
	}

	// ignore response as we will reload
	_ = randr.SetCrtcConfig(conn, (*ctrl).id, 0, 0, 0, 0, randr.Mode(0), randr.RotationRotate0, []randr.Output{})
}
