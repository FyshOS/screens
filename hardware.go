package main

import "github.com/BurntSushi/xgb/randr"

type State struct {
	controllers []Controller
	outputs     []Output
	modes       []Mode
}

type Controller struct {
	id randr.Crtc

	Mode *Mode
	X, Y int16
}

type Mode struct {
	id randr.Mode

	Width, Height uint16
}

type Output struct {
	id randr.Output

	CurrentMode *Mode
	Modes       []Mode
	Name        string
}
