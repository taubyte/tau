//go:build !mock

package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"

	"github.com/taubyte/go-interfaces/services/seer"
)

// Structs to parse responses from the APIs
type ipAPIResponse struct {
	Lat float32 `json:"lat"`
	Lon float32 `json:"lon"`
}

type freeGeoIPResponse struct {
	Latitude  float32 `json:"latitude"`
	Longitude float32 `json:"longitude"`
}

// Function to estimate GPS location
func estimateGPSLocation() (*seer.Location, error) {
	var wg sync.WaitGroup
	wg.Add(2)

	var mu sync.Mutex
	var locations []seer.Location

	// ip-api.com
	go func() {
		defer wg.Done()
		resp, err := http.Get("http://ip-api.com/json/")
		if err != nil {
			fmt.Println("Error calling ip-api.com:", err)
			return
		}
		defer resp.Body.Close()

		var ipAPIResp ipAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&ipAPIResp); err == nil {
			mu.Lock()
			locations = append(locations, seer.Location{Latitude: ipAPIResp.Lat, Longitude: ipAPIResp.Lon})
			mu.Unlock()
		}
	}()

	// freegeoip.io
	go func() {
		defer wg.Done()
		resp, err := http.Get("https://freegeoip.app/json/")
		if err != nil {
			fmt.Println("Error calling freegeoip.app:", err)
			return
		}
		defer resp.Body.Close()

		var freeGeoIPResp freeGeoIPResponse
		if err := json.NewDecoder(resp.Body).Decode(&freeGeoIPResp); err == nil {
			mu.Lock()
			locations = append(locations, seer.Location{Latitude: freeGeoIPResp.Latitude, Longitude: freeGeoIPResp.Longitude})
			mu.Unlock()
		}
	}()

	wg.Wait()

	switch len(locations) {
	case 1:
		return &locations[0], nil
	case 2:
		avg := averageLocations(locations[0], locations[1])
		return &avg, nil
	default:
		return nil, fmt.Errorf("failed to estimate GPS location")
	}
}

// Converts geographic coordinates to Cartesian (x, y, z).
func toCartesian(lat, long float32) (x, y, z float64) {
	latRad := float64(lat) * math.Pi / 180
	longRad := float64(long) * math.Pi / 180

	x = math.Cos(latRad) * math.Cos(longRad)
	y = math.Cos(latRad) * math.Sin(longRad)
	z = math.Sin(latRad)
	return
}

// Converts Cartesian coordinates (x, y, z) back to geographic (latitude, longitude).
func toGeographic(x, y, z float64) (lat, long float32) {
	lat = float32(math.Atan2(z, math.Sqrt(x*x+y*y)) * 180 / math.Pi)
	long = float32(math.Atan2(y, x) * 180 / math.Pi)
	return
}

// Averages two locations more accurately by converting to Cartesian coordinates, averaging, and converting back.
func averageLocations(loc1, loc2 seer.Location) seer.Location {
	x1, y1, z1 := toCartesian(loc1.Latitude, loc1.Longitude)
	x2, y2, z2 := toCartesian(loc2.Latitude, loc2.Longitude)

	avgX := (x1 + x2) / 2
	avgY := (y1 + y2) / 2
	avgZ := (z1 + z2) / 2

	avgLat, avgLong := toGeographic(avgX, avgY, avgZ)
	return seer.Location{Latitude: avgLat, Longitude: avgLong}
}
