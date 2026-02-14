package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/toon-format/toon-go"
)

// Format is the output format: "table", "json", or "toon".
var Format = "table"

// SetFormat sets the output format from flags. Json takes precedence over Toon.
func SetFormat(json, toon bool) {
	if json {
		Format = "json"
	} else if toon {
		Format = "toon"
	} else {
		Format = "table"
	}
}

// RenderKeyValue outputs [][]string key-value data in the current format.
// Returns true if output was handled (json/toon), false if caller should render table.
func RenderKeyValue(data [][]string) bool {
	switch Format {
	case "json":
		m := make(map[string]string)
		for _, item := range data {
			if len(item) >= 2 {
				key := strings.Replace(item[0], "\t", " -  ", 1)
				m[key] = item[1]
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(m); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return true
	case "toon":
		m := make(map[string]string)
		for _, item := range data {
			if len(item) >= 2 {
				key := strings.Replace(item[0], "\t", " -  ", 1)
				m[key] = item[1]
			}
		}
		b, err := toon.Marshal(m, toon.WithLengthMarkers(true))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return true
		}
		fmt.Print(string(b))
		return true
	}
	return false
}

// Render outputs arbitrary data as JSON or TOON. For table format, returns false.
func Render(data any) bool {
	switch Format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(data); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return true
	case "toon":
		b, err := toon.Marshal(data, toon.WithLengthMarkers(true))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return true
		}
		fmt.Print(string(b))
		return true
	}
	return false
}
