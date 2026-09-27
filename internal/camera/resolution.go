package camera

import "fmt"

func ParseResolution(s string) (width, height int, err error) {
	if _, err := fmt.Sscanf(s, "%dx%d", &width, &height); err != nil || width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("camera: invalid resolution %q", s)
	}
	return width, height, nil
}
