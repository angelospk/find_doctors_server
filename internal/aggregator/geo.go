package aggregator

import "math"

// distance calculates the Haversine distance between two points in km.
func distance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Earth radius in km
	dLat := (lat2 - lat1) * (math.Pi / 180)
	dLon := (lon2 - lon1) * (math.Pi / 180)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180))*math.Cos(lat2*(math.Pi/180))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// hasCoords reports whether a lat/lon pair carries usable geographic data.
// A 0/0 record (the upstream default for a missing location) and any NaN
// component are treated as "unknown" rather than a real point off West Africa.
func hasCoords(lat, lon float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lon) {
		return false
	}
	return lat != 0 || lon != 0
}
