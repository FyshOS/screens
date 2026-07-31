package screenmanager

import (
	"testing"
)

func mode(width, height uint16) *Mode {
	return &Mode{Width: width, Height: height}
}

func TestLayoutBounds(t *testing.T) {
	for name, tt := range map[string]struct {
		places        []placement
		width, height int32
	}{
		"mirrored": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{mode: mode(1920, 1080)},
			},
			width: 1920, height: 1080,
		},
		"side by side": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{mode: mode(1280, 1024), x: 1920},
			},
			width: 3200, height: 1080,
		},
		"stacked": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{mode: mode(1280, 1024), y: 1080},
			},
			width: 1920, height: 2104,
		},
		"switched off screens do not count": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{x: 1920},
			},
			width: 1920, height: 1080,
		},
	} {
		t.Run(name, func(t *testing.T) {
			width, height := layoutBounds(tt.places)
			if width != tt.width || height != tt.height {
				t.Errorf("expected %dx%d, got %dx%d", tt.width, tt.height, width, height)
			}
		})
	}
}

// TestNormaliseShiftsToOrigin covers positioning a screen left of or above the
// primary, where the position works out negative and the desktop has to move
// across instead.
func TestNormaliseShiftsToOrigin(t *testing.T) {
	for name, tt := range map[string]struct {
		places  []placement
		wantX   []int16
		wantY   []int16
		bWidth  int32
		bHeight int32
	}{
		"left of the primary": {
			places: []placement{
				{mode: mode(1920, 1080)},           // primary at the origin
				{mode: mode(1280, 1024), x: -1280}, // placed to its left
			},
			wantX: []int16{1280, 0}, wantY: []int16{0, 0},
			bWidth: 3200, bHeight: 1080,
		},
		"above the primary": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{mode: mode(1280, 1024), y: -1024},
			},
			wantX: []int16{0, 0}, wantY: []int16{1024, 0},
			bWidth: 1920, bHeight: 2104,
		},
		"already at the origin is left alone": {
			places: []placement{
				{mode: mode(1920, 1080)},
				{mode: mode(1280, 1024), x: 1920},
			},
			wantX: []int16{0, 1920}, wantY: []int16{0, 0},
			bWidth: 3200, bHeight: 1080,
		},
	} {
		t.Run(name, func(t *testing.T) {
			normalise(tt.places)

			for i, place := range tt.places {
				if place.x != tt.wantX[i] || place.y != tt.wantY[i] {
					t.Errorf("screen %d: expected (%d, %d), got (%d, %d)",
						i, tt.wantX[i], tt.wantY[i], place.x, place.y)
				}
			}

			// A normalised layout has to fit a desktop that starts at the origin.
			width, height := layoutBounds(tt.places)
			if width != tt.bWidth || height != tt.bHeight {
				t.Errorf("expected bounds of %dx%d, got %dx%d", tt.bWidth, tt.bHeight, width, height)
			}
		})
	}
}

// TestConfigSignature covers the check that stops the repeated events the server
// sends for one change from rebuilding the panels each time, which detaches
// whatever the pointer is on from the canvas.
func TestConfigSignature(t *testing.T) {
	sideBySide := State{
		outputs: []Output{
			{id: 1, Name: "eDP-1", ctrl: 10, CurrentMode: mode(1920, 1080), Modes: []Mode{{Width: 1920, Height: 1080}}},
			{id: 2, Name: "HDMI-1", ctrl: 11, CurrentMode: mode(1920, 1080), Modes: []Mode{{Width: 1920, Height: 1080}}},
		},
		controllers: []Controller{{id: 10}, {id: 11, X: 1920}},
	}

	same := sideBySide
	if configSignature(sideBySide) != configSignature(same) {
		t.Error("an unchanged configuration should produce the same signature")
	}

	// The second screen moves to the origin, which is what mirroring does.
	mirrored := sideBySide
	mirrored.controllers = []Controller{{id: 10}, {id: 11}}
	if configSignature(sideBySide) == configSignature(mirrored) {
		t.Error("moving a screen should produce a different signature")
	}

	unplugged := sideBySide
	unplugged.outputs = sideBySide.outputs[:1]
	if configSignature(sideBySide) == configSignature(unplugged) {
		t.Error("losing a screen should produce a different signature")
	}

	switchedOff := sideBySide
	switchedOff.outputs = []Output{sideBySide.outputs[0], {id: 2, Name: "HDMI-1",
		Modes: []Mode{{Width: 1920, Height: 1080}}}}
	if configSignature(sideBySide) == configSignature(switchedOff) {
		t.Error("switching a screen off should produce a different signature")
	}
}

func TestLargestMode(t *testing.T) {
	// Ordered as an output lists them, preferred first, so the largest is not
	// simply the first entry.
	modes := []Mode{
		{Width: 1280, Height: 1024},
		{Width: 1920, Height: 1080},
		{Width: 800, Height: 600},
	}

	if best := largestMode(modes); best.Width != 1920 || best.Height != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", best.Width, best.Height)
	}
}

func TestFindCommonModes(t *testing.T) {
	outputs := []Output{
		{Name: "eDP-1", Modes: []Mode{
			{Width: 1920, Height: 1200},
			{Width: 1920, Height: 1080},
			{Width: 1280, Height: 1024},
		}},
		{Name: "HDMI-1", Modes: []Mode{
			{Width: 3840, Height: 2160},
			{Width: 1920, Height: 1080},
			{Width: 1280, Height: 1024},
		}},
	}

	common := findCommonModes(outputs)
	if len(common) != 2 {
		t.Fatalf("expected 2 shared resolutions, got %d: %v", len(common), common)
	}

	if best := largestMode(common); best.Width != 1920 || best.Height != 1080 {
		t.Errorf("expected the best shared resolution to be 1920x1080, got %dx%d", best.Width, best.Height)
	}
}
