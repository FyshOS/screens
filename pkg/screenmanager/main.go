package screenmanager

import (
	"fmt"

	"fyne.io/fyne/v2"
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

func (g *screensGui) setup(w fyne.Window, s *Screens) {
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

			// ScreenChangeNotifyEvent fires when the screen layout changes;
			// NotifyEvent fires for CRTC/output changes, including a monitor
			// being physically plugged in or removed. Refresh the UI for any
			// of them so the display list stays in sync with the hardware.
			switch ev.(type) {
			case randr.ScreenChangeNotifyEvent, randr.NotifyEvent:
				fyne.Do(func() {
					g.loadScreens(w)
					if s.OnConfigurationChanged != nil {
						s.OnConfigurationChanged()
					}
				})
			}
		}
	}()
}

func (g *screensGui) loadScreens(w fyne.Window) {
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
			index := i
			other := widget.NewCheck(output.Name, func(on bool) {
				if !on {
					return
				}

				g.activate(state.outputs[index])
			})

			g.offline.Add(other)
			continue
		}

		panel := &screenGui{}
		ui := panel.makeUI()
		panel.name.SetText(output.Name)

		panel.active.SetChecked(true)
		isPrimary := first
		if first {
			panel.active.Disable()
			panel.primary.SetChecked(true)
			panel.location.Disable()
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
			if active := activeOutputs(); !isPrimary && len(active) > 0 {
				primaryCtrl := findController(active[0].ctrl)
				panel.location.SetSelected(detectLocation(output, primaryCtrl))
			}
			panel.screen.Aspect = float32(output.CurrentMode.Width) / float32(output.CurrentMode.Height)
		}
		panel.resolution.OnChanged = func(m string) {
			mode, ok := modes[m]
			if !ok {
				return
			}

			// Change only the mode: the screen keeps its place in the layout, and
			// the desktop resizes around the new size.
			places := currentPlacements()
			found := false
			for i := range places {
				if places[i].output != output.id {
					continue
				}

				places[i].mode = &mode
				found = true
			}
			if !found {
				places = append(places, placement{crtc: output.ctrl, output: output.id,
					name: output.Name, mode: &mode})
			}

			if err := applyLayout(places); err != nil {
				dialog.ShowError(fmt.Errorf("failed to set resolution: %w", err), w)
				return
			}

			panel.screen.Aspect = float32(mode.Width) / float32(mode.Height)
			panel.screen.Refresh()
		}
		if !isPrimary {
			panel.location.OnChanged = func(loc string) {
				g.applyLocation(w, output, loc)
			}
		}

		panel.label.Alignment = fyne.TextAlignCenter
		panel.label.SetText(output.Name)

		g.connected.Add(ui)
	}
}

func (g *screensGui) activate(out Output) {
	if len(out.Modes) == 0 {
		fyne.LogError("Output has no modes to activate!", nil)
		return
	}

	var ctrl *Controller
	for i := range state.controllers {
		if state.controllers[i].Mode == nil {
			ctrl = &state.controllers[i]
			break
		}
	}
	if ctrl == nil {
		fyne.LogError("Cannot find an available controller!", nil)
		return
	}

	places := currentPlacements()

	x := int16(0) // just assume RightOf TODO configure
	for _, place := range places {
		if place.mode == nil {
			continue
		}

		if right := place.x + int16(place.mode.Width); right > x {
			x = right
		}
	}

	places = append(places, placement{crtc: ctrl.id, output: out.id, name: out.Name,
		mode: &out.Modes[0], x: x})
	if err := applyLayout(places); err != nil {
		fyne.LogError("Failed to activate output", err)
	}
}

func (g *screensGui) deactivate(out Output) {
	if out.ctrl == 0 {
		fyne.LogError("Cannot find matching controller!", nil)
		return
	}

	// Switch the screen off first, then shrink the desktop around what is left
	// of the layout.
	if err := disableCrtc(out.ctrl, out.Name); err != nil {
		fyne.LogError("Failed to deactivate output", err)
		return
	}

	var places []placement
	for _, place := range currentPlacements() {
		if place.crtc == out.ctrl {
			continue
		}

		places = append(places, place)
	}
	if len(places) == 0 {
		return
	}

	if err := applyLayout(places); err != nil {
		fyne.LogError("Failed to resize the desktop after deactivating output", err)
	}
}

func findController(id randr.Crtc) *Controller {
	for i := range state.controllers {
		if state.controllers[i].id == id {
			return &state.controllers[i]
		}
	}
	return nil
}

// findCommonModes returns modes (by resolution) supported by all outputs,
// ordered by the primary output's preference.
func findCommonModes(outputs []Output) []Mode {
	if len(outputs) == 0 {
		return nil
	}
	var common []Mode
	seen := map[string]bool{}
	for _, m := range outputs[0].Modes {
		key := fmt.Sprintf("%dx%d", m.Width, m.Height)
		if seen[key] {
			continue
		}
		allSupport := true
		for _, o := range outputs[1:] {
			found := false
			for _, om := range o.Modes {
				if om.Width == m.Width && om.Height == m.Height {
					found = true
					break
				}
			}
			if !found {
				allSupport = false
				break
			}
		}
		if allSupport {
			seen[key] = true
			common = append(common, m)
		}
	}
	return common
}

// largestMode returns the mode covering the most pixels, so that mirroring picks
// the best resolution the screens share rather than whichever one the primary
// happens to list first.
func largestMode(modes []Mode) Mode {
	best := modes[0]
	for _, m := range modes[1:] {
		if int32(m.Width)*int32(m.Height) > int32(best.Width)*int32(best.Height) {
			best = m
		}
	}
	return best
}

// findModeForOutput returns the first mode in out.Modes matching the given resolution.
func findModeForOutput(out Output, width, height uint16) *Mode {
	for i := range out.Modes {
		if out.Modes[i].Width == width && out.Modes[i].Height == height {
			return &out.Modes[i]
		}
	}
	return nil
}

// detectLocation infers the location of out relative to primaryCtrl.
func detectLocation(out Output, primaryCtrl *Controller) string {
	if primaryCtrl == nil {
		return "RightOf"
	}
	ctrl := findController(out.ctrl)
	if ctrl == nil {
		return "RightOf"
	}
	if ctrl.X == primaryCtrl.X && ctrl.Y == primaryCtrl.Y {
		return "Mirror"
	}
	if ctrl.X < primaryCtrl.X {
		return "LeftOf"
	}
	if ctrl.X > primaryCtrl.X {
		return "RightOf"
	}
	if ctrl.Y < primaryCtrl.Y {
		return "Above"
	}
	return "Below"
}

func (g *screensGui) applyLocation(w fyne.Window, out Output, location string) {
	active := activeOutputs()
	if len(active) == 0 {
		return
	}

	var places []placement
	switch location {
	case "Mirror":
		commonModes := findCommonModes(active)
		if len(commonModes) == 0 {
			dialog.ShowError(fmt.Errorf("no common resolution found for mirroring"), w)
			return
		}

		// Every screen shows the same area from the origin, so the desktop
		// shrinks to that one resolution.
		best := largestMode(commonModes)
		for _, o := range active {
			m := findModeForOutput(o, best.Width, best.Height)
			if m == nil {
				dialog.ShowError(fmt.Errorf("%s cannot display %dx%d", o.Name, best.Width, best.Height), w)
				return
			}

			places = append(places, placement{crtc: o.ctrl, output: o.id, name: o.Name, mode: m})
		}
	default:
		if out.ctrl == 0 {
			return
		}
		primaryCtrl := findController(active[0].ctrl)
		if primaryCtrl == nil || primaryCtrl.Mode == nil || len(out.Modes) == 0 {
			return
		}

		preferred := out.Modes[0]
		var x, y int16
		switch location {
		case "LeftOf":
			x = primaryCtrl.X - int16(preferred.Width)
			y = primaryCtrl.Y
		case "RightOf":
			x = primaryCtrl.X + int16(primaryCtrl.Mode.Width)
			y = primaryCtrl.Y
		case "Above":
			x = primaryCtrl.X
			y = primaryCtrl.Y - int16(preferred.Height)
		case "Below":
			x = primaryCtrl.X
			y = primaryCtrl.Y + int16(primaryCtrl.Mode.Height)
		default:
			return
		}

		for _, place := range currentPlacements() {
			if place.output == out.id {
				continue
			}

			places = append(places, place)
		}
		places = append(places, placement{crtc: out.ctrl, output: out.id, name: out.Name,
			mode: &preferred, x: x, y: y})
	}

	if err := applyLayout(places); err != nil {
		dialog.ShowError(fmt.Errorf("failed to arrange displays: %w", err), w)
	}
}
