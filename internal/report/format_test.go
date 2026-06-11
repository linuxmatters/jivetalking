package report

import (
	"math"
	"testing"
)

func TestFormatFloatPlaceholder(t *testing.T) {
	cases := []struct {
		name string
		in   float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatFloat(c.in, 1); got != placeholder {
				t.Errorf("formatFloat(%v) = %q, want placeholder %q", c.in, got, placeholder)
			}
		})
	}
}

func TestFormatFloatFinite(t *testing.T) {
	cases := []struct {
		in       float64
		decimals int
		want     string
	}{
		{-16.0, 1, "-16.0"},
		{-1.23456, 2, "-1.23"},
		{0.0, 1, "0.0"},
		{48000.0, 0, "48000"},
	}
	for _, c := range cases {
		if got := formatFloat(c.in, c.decimals); got != c.want {
			t.Errorf("formatFloat(%v, %d) = %q, want %q", c.in, c.decimals, got, c.want)
		}
	}
}
