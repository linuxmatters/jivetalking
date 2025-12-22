// Package mains detects local electrical mains frequency from system timezone.
package mains

import (
	"strings"

	tz "github.com/medama-io/go-timezone-country"
	"github.com/thlib/go-timezone-local/tzlocal"
)

// Frequency returns the local mains frequency in Hz (50 or 60).
// Returns 50Hz if detection fails or timezone is ambiguous.
func Frequency() int {
	timezone, err := tzlocal.RuntimeTZ()
	if err != nil {
		return 50 // Default fallback
	}
	return FrequencyForTimezone(timezone)
}

// FrequencyForTimezone returns the mains frequency for a given IANA timezone.
// Exported for testing with specific timezones.
func FrequencyForTimezone(timezone string) int {
	// Handle UTC/GMT—no country association, default to 50Hz
	if timezone == "UTC" || timezone == "GMT" || strings.HasPrefix(timezone, "Etc/") {
		return 50
	}

	tzMap, err := tz.NewTimezoneCountryMap()
	if err != nil {
		return 50
	}

	country, err := tzMap.GetCountry(timezone)
	if err != nil {
		return 50
	}

	return frequencyForCountry(country)
}

// frequencyForCountry returns the mains frequency for a country name.
// Returns 50Hz for unknown countries (more common globally).
func frequencyForCountry(country string) int {
	// Japan special case: split 50/60Hz by region
	// Default to 50Hz (Tokyo region is most populous)
	if country == "Japan" {
		return 50
	}

	if hz60Countries[country] {
		return 60
	}
	return 50
}

// hz60Countries lists countries using 60Hz mains power.
// All other countries use 50Hz.
// Source: https://en.wikipedia.org/wiki/Mains_electricity_by_country
var hz60Countries = map[string]bool{
	// North America
	"United States": true,
	"Canada":        true,
	"Mexico":        true,

	// Central America
	"Belize":      true,
	"Costa Rica":  true,
	"El Salvador": true,
	"Guatemala":   true,
	"Honduras":    true,
	"Nicaragua":   true,
	"Panama":      true,

	// Caribbean
	"Bahamas":             true,
	"Barbados":            true,
	"Cayman Islands":      true,
	"Cuba":                true,
	"Dominican Republic":  true,
	"Haiti":               true,
	"Jamaica":             true,
	"Puerto Rico":         true,
	"Trinidad and Tobago": true,
	"U.S. Virgin Islands": true,

	// South America (partial—most use 50Hz)
	"Brazil":    true, // Note: Brazil has both 50Hz and 60Hz regions; 60Hz predominant
	"Colombia":  true,
	"Ecuador":   true,
	"Guyana":    true,
	"Peru":      true,
	"Suriname":  true,
	"Venezuela": true,

	// Asia (partial)
	"South Korea":  true,
	"Taiwan":       true,
	"Philippines":  true,
	"Saudi Arabia": true,

	// Pacific
	"Guam":             true,
	"American Samoa":   true,
	"Marshall Islands": true,
	"Micronesia":       true,
	"Palau":            true,
}
