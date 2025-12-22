package mains

import "testing"

func TestFrequencyForTimezone(t *testing.T) {
	tests := []struct {
		timezone string
		want     int
	}{
		// 50Hz countries
		{"Europe/London", 50},
		{"Europe/Paris", 50},
		{"Europe/Berlin", 50},
		{"Australia/Sydney", 50},
		{"Asia/Shanghai", 50},
		{"Asia/Tokyo", 50}, // Japan defaults to 50Hz

		// 60Hz countries
		{"America/New_York", 60},
		{"America/Los_Angeles", 60},
		{"America/Chicago", 60},
		{"America/Toronto", 60},
		{"America/Mexico_City", 60},
		{"America/Bogota", 60},    // Colombia
		{"America/Sao_Paulo", 60}, // Brazil
		{"Asia/Seoul", 60},        // South Korea
		{"Asia/Taipei", 60},       // Taiwan
		{"Asia/Manila", 60},       // Philippines

		// Edge cases
		{"UTC", 50},
		{"GMT", 50},
		{"Etc/UTC", 50},
	}

	for _, tt := range tests {
		t.Run(tt.timezone, func(t *testing.T) {
			got := FrequencyForTimezone(tt.timezone)
			if got != tt.want {
				t.Errorf("FrequencyForTimezone(%q) = %d, want %d", tt.timezone, got, tt.want)
			}
		})
	}
}

func TestFrequency(t *testing.T) {
	// Just verify it returns a valid value without panicking
	freq := Frequency()
	if freq != 50 && freq != 60 {
		t.Errorf("Frequency() = %d, want 50 or 60", freq)
	}
}
