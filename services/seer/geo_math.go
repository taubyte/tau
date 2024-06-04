package seer

import "math"

/****** from https://gist.github.com/cdipaolo/d3f8db3848278b49db68 ***/

// haversin(θ) function
func hsin(theta float64) float64 {
	return math.Pow(math.Sin(theta/2), 2)
}

// Distance function returns the distance (in meters) between two points of
//
//	a given longitude and latitude relatively accurately (using a spherical
//	approximation of the Earth) through the Haversin Distance Formula for
//	great arc distance on a sphere with accuracy for small distances
//
// point coordinates are supplied in degrees and converted into rad. in the func
//
// distance returned is METERS!!!!!!
// http://en.wikipedia.org/wiki/Haversine_formula
func computeDistance(lat1, lon1, lat2, lon2 float32) float32 {
	// convert to radians
	// must cast radius as float to multiply later
	var la1, lo1, la2, lo2, r float64
	la1 = float64(lat1) * math.Pi / 180
	lo1 = float64(lon1) * math.Pi / 180
	la2 = float64(lat2) * math.Pi / 180
	lo2 = float64(lon2) * math.Pi / 180

	r = 6378100 // Earth radius in METERS

	// calculate
	h := hsin(la2-la1) + math.Cos(la1)*math.Cos(la2)*hsin(lo2-lo1)

	return float32(2 * r * math.Asin(math.Sqrt(h)))
}

/*******************************/
