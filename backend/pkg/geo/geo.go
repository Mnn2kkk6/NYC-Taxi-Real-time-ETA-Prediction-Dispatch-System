// Package geo provides distance calculations used by driver dispatch.
// HaversineKm mirrors the formula in ml-service/app/features.py exactly
// (same earth radius constant) so "distance" means the same thing
// whether it's computed here for dispatch or in Python for prediction.
package geo

import "math"

const earthRadiusKm = 6371.0

// HaversineKm returns the great-circle distance between two lat/lon
// points in kilometers.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	lat1R := lat1 * math.Pi / 180
	lon1R := lon1 * math.Pi / 180
	lat2R := lat2 * math.Pi / 180
	lon2R := lon2 * math.Pi / 180

	dLat := lat2R - lat1R
	dLon := lon2R - lon1R

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1R)*math.Cos(lat2R)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Asin(math.Sqrt(a))
	return earthRadiusKm * c
}
