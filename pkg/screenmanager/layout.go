package screenmanager

import (
	"fmt"

	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/xproto"
)

// placement is the wanted configuration of a single controller: the output it
// drives, the mode it drives it at, and where that lands in the desktop
// coordinate space. A nil mode means the controller should be switched off.
type placement struct {
	crtc   randr.Crtc
	output randr.Output
	name   string

	mode *Mode
	x, y int16
}

// activeOutputs returns the connected outputs that a controller is currently
// driving.
func activeOutputs() []Output {
	var active []Output
	for _, out := range state.outputs {
		if out.ctrl == 0 || out.CurrentMode == nil {
			continue
		}

		active = append(active, out)
	}
	return active
}

// currentPlacements captures the layout as the server has it right now, one entry
// per active controller. Callers adjust the entries they want to change and pass
// the whole set to applyLayout, so the screens they are not touching keep their
// positions.
func currentPlacements() []placement {
	var places []placement
	for _, out := range activeOutputs() {
		ctrl := findController(out.ctrl)
		if ctrl == nil || ctrl.Mode == nil {
			continue
		}

		places = append(places, placement{crtc: out.ctrl, output: out.id, name: out.Name,
			mode: ctrl.Mode, x: ctrl.X, y: ctrl.Y})
	}
	return places
}

// applyLayout configures every controller in places and resizes the desktop to
// fit them.
func applyLayout(places []placement) error {
	if len(places) == 0 {
		return fmt.Errorf("no active screens to configure")
	}

	normalise(places)
	width, height, err := layoutSize(places)
	if err != nil {
		return err
	}

	currentWidth, currentHeight, err := currentScreenSize()
	if err != nil {
		return err
	}

	// Grow first, so that every new position is already inside the desktop by
	// the time the controllers move.
	if err := resizeScreen(max16(width, currentWidth), max16(height, currentHeight)); err != nil {
		return err
	}

	for _, place := range places {
		if err := configureCrtc(place); err != nil {
			return err
		}
	}

	// Every screen is now within the wanted bounds, so any slack can go.
	return resizeScreen(width, height)
}

// normalise shifts a layout so that its top left corner is the origin. The
// desktop always starts at (0, 0), so a screen positioned left of or above the
// primary has to push everything else across rather than take a negative
// coordinate, which the server will not accept.
func normalise(places []placement) {
	var minX, minY int16
	for _, place := range places {
		if place.x < minX {
			minX = place.x
		}
		if place.y < minY {
			minY = place.y
		}
	}

	for i := range places {
		places[i].x -= minX
		places[i].y -= minY
	}
}

// layoutBounds returns the size of the smallest desktop containing every
// placement.
func layoutBounds(places []placement) (int32, int32) {
	var width, height int32
	for _, place := range places {
		if place.mode == nil {
			continue
		}

		if right := int32(place.x) + int32(place.mode.Width); right > width {
			width = right
		}
		if bottom := int32(place.y) + int32(place.mode.Height); bottom > height {
			height = bottom
		}
	}

	return width, height
}

// layoutSize returns the bounds of a layout clamped to the sizes the server
// supports.
func layoutSize(places []placement) (uint16, uint16, error) {
	width, height := layoutBounds(places)
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("screen layout has no size")
	}

	limits, err := randr.GetScreenSizeRange(conn, root).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read the supported desktop sizes: %w", err)
	}
	if width > int32(limits.MaxWidth) || height > int32(limits.MaxHeight) {
		return 0, 0, fmt.Errorf("a %dx%d desktop is larger than the maximum of %dx%d supported by this graphics driver",
			width, height, limits.MaxWidth, limits.MaxHeight)
	}
	if width < int32(limits.MinWidth) {
		width = int32(limits.MinWidth)
	}
	if height < int32(limits.MinHeight) {
		height = int32(limits.MinHeight)
	}

	return uint16(width), uint16(height), nil
}

// currentScreenSize reads the desktop size from the root window, which follows
// the desktop as it is resized - unlike the connection setup, which is only a
// snapshot from when we connected.
func currentScreenSize() (uint16, uint16, error) {
	geometry, err := xproto.GetGeometry(conn, xproto.Drawable(root)).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read the desktop size: %w", err)
	}

	return geometry.Width, geometry.Height, nil
}

// resizeScreen sets the desktop size, doing nothing if it already matches.
func resizeScreen(width, height uint16) error {
	currentWidth, currentHeight, err := currentScreenSize()
	if err != nil {
		return err
	}
	if width == currentWidth && height == currentHeight {
		return nil
	}

	mmWidth, mmHeight := physicalSize(width, height)
	err = randr.SetScreenSizeChecked(conn, root, width, height, mmWidth, mmHeight).Check()
	if err != nil {
		return fmt.Errorf("failed to resize the desktop to %dx%d: %w", width, height, err)
	}
	return nil
}

// physicalSize scales a pixel size into the millimetres that a desktop resize
// asks for, holding the density the session started at so that anything sizing
// itself from the reported DPI does not jump when the layout changes.
func physicalSize(width, height uint16) (uint32, uint32) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	if screen != nil && screen.WidthInPixels > 0 && screen.HeightInPixels > 0 &&
		screen.WidthInMillimeters > 0 && screen.HeightInMillimeters > 0 {
		return uint32(int32(width) * int32(screen.WidthInMillimeters) / int32(screen.WidthInPixels)),
			uint32(int32(height) * int32(screen.HeightInMillimeters) / int32(screen.HeightInPixels))
	}

	// Fall back to the 96 DPI that X assumes when it has nothing better.
	return uint32(float32(width) * 25.4 / 96), uint32(float32(height) * 25.4 / 96)
}

// configureCrtc applies a single placement, switching the controller off if the
// placement has no mode.
func configureCrtc(place placement) error {
	mode := randr.Mode(0)
	var outputs []randr.Output
	if place.mode != nil {
		mode = place.mode.id
		outputs = []randr.Output{place.output}
	}

	status, err := trySetCrtcConfig(place, mode, outputs)
	if err != nil {
		return err
	}
	if status == randr.SetConfigInvalidConfigTime {
		// Our cached configuration timestamp has gone stale, which happens when
		// the hardware changed since the last load. Catch up and try once more.
		if err = refreshConfigTimestamp(); err != nil {
			return err
		}
		if status, err = trySetCrtcConfig(place, mode, outputs); err != nil {
			return err
		}
	}
	if status != randr.SetConfigSuccess {
		return fmt.Errorf("could not configure %s: %s", place.name, setConfigStatus(status))
	}

	return nil
}

// trySetCrtcConfig makes the request and hands back its status. The status has to
// be inspected by the caller because a configuration the server refuses comes
// back as a status rather than as a protocol error, so it otherwise reads as a
// success.
func trySetCrtcConfig(place placement, mode randr.Mode, outputs []randr.Output) (byte, error) {
	reply, err := randr.SetCrtcConfig(conn, place.crtc, 0, state.configTimestamp,
		place.x, place.y, mode, randr.RotationRotate0, outputs).Reply()
	if err != nil {
		return 0, err
	}

	return reply.Status, nil
}

// disableCrtc switches a controller off, leaving the desktop size alone.
func disableCrtc(crtc randr.Crtc, name string) error {
	return configureCrtc(placement{crtc: crtc, name: name})
}

// refreshConfigTimestamp re-reads the configuration timestamp that RandR uses to
// spot requests racing against a hardware change.
func refreshConfigTimestamp() error {
	resources, err := randr.GetScreenResourcesCurrent(conn, root).Reply()
	if err != nil {
		return fmt.Errorf("failed to refresh the screen resources: %w", err)
	}

	state.configTimestamp = resources.ConfigTimestamp
	return nil
}

func setConfigStatus(status byte) string {
	switch status {
	case randr.SetConfigSuccess:
		return "success"
	case randr.SetConfigInvalidConfigTime:
		return "the screen configuration changed while we were applying it"
	case randr.SetConfigInvalidTime:
		return "the request arrived out of order"
	case randr.SetConfigFailed:
		return "the graphics driver could not apply it"
	}

	return fmt.Sprintf("unknown status %d", status)
}

func max16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}
