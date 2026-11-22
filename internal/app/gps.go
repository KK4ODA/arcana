package app

import (
	"context"

	"github.com/KK4ODA/arcana/internal/gps"
	"github.com/KK4ODA/arcana/internal/library"
)

func gpsSettings(s library.GPSSettings) gps.Settings {
	return gps.Settings{
		Source: s.Source, SerialPort: s.SerialPort, BaudRate: s.BaudRate, UDPPort: s.UDPPort, Address: s.Address,
		SimulatorTrack: s.SimulatorTrack, SimulatorSpeed: s.SimulatorSpeed, ManualLat: s.ManualLat, ManualLon: s.ManualLon,
		RecordTrack: s.RecordTrack,
	}
}

func (a *App) openGPS() {
	a.GPS = gps.NewManager(a.Lib.User(), a.Lib.ReadOnly, a.Log)
	// The simulator's default loop is placed on the most detailed
	// installed map, so it is visible straight away.
	a.GPS.SimCenter = func() (float64, float64, bool) {
		for _, s := range a.maps.service.Sets() {
			if s.Error == "" && s.Bounds[2] > s.Bounds[0] {
				return (s.Bounds[1] + s.Bounds[3]) / 2, (s.Bounds[0] + s.Bounds[2]) / 2, true
			}
		}
		return 0, 0, false
	}
}

// ApplyGPSSettings restarts the position source from the current settings.
func (a *App) ApplyGPSSettings() {
	ctx := a.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a.GPS.Apply(ctx, gpsSettings(a.Lib.Settings.Get().GPS))
}
