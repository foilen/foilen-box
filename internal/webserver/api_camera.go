package webserver

import (
	"encoding/json"
	"fmt"

	boxcamera "foilen-box/internal/camera"

	realmannounce "foilen-realm/features/announce"
	realmmodel "foilen-realm/model"
)

// cameraServiceName is the fixed Config.Services entry name the camera
// creates/removes for itself when "expose as Realm Service" is toggled.
const cameraServiceName = "camera"

func handleCameraGetStatus(a *api, _ json.RawMessage) (any, error) {
	return a.camera.GetStatus(), nil
}

func handleCameraListDevices(a *api, _ json.RawMessage) (any, error) {
	devices, err := a.camera.ListDevices()
	if err != nil {
		return nil, err
	}
	return map[string]any{"devices": devices}, nil
}

func handleCameraStartCapture(a *api, _ json.RawMessage) (any, error) {
	return a.camera.StartCapture()
}

func handleCameraStopCapture(a *api, _ json.RawMessage) (any, error) {
	return a.camera.StopCapture(), nil
}

func handleCameraSaveConfig(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Enabled           bool   `json:"enabled"`
		DeviceID          string `json:"deviceId"`
		DeviceLabel       string `json:"deviceLabel"`
		Port              int    `json:"port"`
		BindAllInterfaces bool   `json:"bindAllInterfaces"`
		ExposeAsService   bool   `json:"exposeAsService"`
		Resolution        string `json:"resolution"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Enabled && p.Port <= 0 {
		return nil, fmt.Errorf("please enter a valid port")
	}
	if p.Resolution != "" {
		if _, _, err := boxcamera.ParseResolution(p.Resolution); err != nil {
			return nil, fmt.Errorf("please select a valid resolution")
		}
	}

	status, err := a.camera.SaveConfig(boxcamera.SaveConfigParams{
		Enabled:           p.Enabled,
		DeviceID:          p.DeviceID,
		DeviceLabel:       p.DeviceLabel,
		Port:              p.Port,
		BindAllInterfaces: p.BindAllInterfaces,
		ExposeAsService:   p.ExposeAsService,
		Resolution:        p.Resolution,
	})
	if err != nil {
		return nil, err
	}

	if err := a.syncCameraService(status); err != nil {
		return nil, err
	}
	return status, nil
}

// syncCameraService adds/updates/removes the "camera" Config.Services entry
// to match status — presence in Config.Services is what makes the RTSP port
// reachable through the common/services realm tunnel, mirroring
// handleRealmAddService/handleRealmDeleteService (api_realm.go).
func (a *api) syncCameraService(status boxcamera.Status) error {
	want := status.Enabled && status.ExposeAsService

	cfg := a.realmConfig.Load()
	var existing *realmmodel.Service
	for i := range cfg.Services {
		if cfg.Services[i].Name == cameraServiceName {
			existing = &cfg.Services[i]
			break
		}
	}

	if !want {
		if existing == nil {
			return nil
		}
		cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
			filtered := c.Services[:0]
			for _, svc := range c.Services {
				if svc.Name != cameraServiceName {
					filtered = append(filtered, svc)
				}
			}
			c.Services = filtered
		})
		if err != nil {
			return err
		}
		realmannounce.RetractServiceNow(a.realmMapsFeature, cfg, cameraServiceName)
		return nil
	}

	description := "Camera RTSP stream"
	if status.DeviceLabel != "" {
		description += " - " + status.DeviceLabel
	}
	svc := realmmodel.Service{
		Name:        cameraServiceName,
		Description: description,
		Hostname:    "127.0.0.1",
		Type:        realmmodel.ServiceTypeRTSP,
		Port:        status.Port,
	}
	if existing != nil && *existing == svc {
		return nil
	}

	cfg, err := a.updateRealmConfig(func(c *realmmodel.Config) {
		for i := range c.Services {
			if c.Services[i].Name == cameraServiceName {
				c.Services[i] = svc
				return
			}
		}
		c.Services = append(c.Services, svc)
	})
	if err != nil {
		return err
	}
	realmannounce.AnnounceServiceNow(a.realmMapsFeature, cfg, svc)
	return nil
}
