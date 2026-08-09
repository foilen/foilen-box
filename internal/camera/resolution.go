package camera

import "fmt"

// ParseResolution parses a "WIDTHxHEIGHT" string (as saved in Data.Resolution
// and offered by the web UI's resolution picker) into its two components.
func ParseResolution(s string) (width, height int, err error) {
	if _, err := fmt.Sscanf(s, "%dx%d", &width, &height); err != nil || width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("camera: invalid resolution %q", s)
	}
	return width, height, nil
}
