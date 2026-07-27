package speedtest

import (
	"testing"
	"time"
)

func TestCalculateMbps(t *testing.T) {
	cases := []struct {
		name          string
		amountOfBytes int64
		elapsed       time.Duration
		want          float64
	}{
		{"10MB in 1s is 80Mbps", 10_000_000, time.Second, 80},
		{"10MB in 2s is 40Mbps", 10_000_000, 2 * time.Second, 40},
		{"zero elapsed is 0", 10_000_000, 0, 0},
		{"zero bytes is 0", 0, time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calculateMbps(tc.amountOfBytes, tc.elapsed)
			if got != tc.want {
				t.Fatalf("calculateMbps(%d, %v) = %v, want %v", tc.amountOfBytes, tc.elapsed, got, tc.want)
			}
		})
	}
}
