package main

import (
	"github.com/BurntSushi/xgb/randr"
	"github.com/BurntSushi/xgb/xproto"
)

type State struct {
	controllers     []Controller
	outputs         []Output
	modes           []Mode
	configTimestamp xproto.Timestamp
}

type Controller struct {
	id randr.Crtc

	Mode    *Mode
	Outputs []randr.Output
	X, Y    int16
}

type Mode struct {
	id randr.Mode

	Width, Height uint16
}

type Output struct {
	id   randr.Output
	ctrl randr.Crtc

	CurrentMode *Mode
	Modes       []Mode
	Name        string
}
