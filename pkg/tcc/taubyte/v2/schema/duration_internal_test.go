package schema

import (
	"regexp"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// A DURATION IS GO'S GRAMMAR WITH DAYS, and the pattern a JSON Schema validator checks
// refuses exactly what the parser does.
func TestADurationReadsDaysAndGoAlike(t *testing.T) {
	pattern := regexp.MustCompile(durationPattern)
	type ok struct {
		in   string
		want time.Duration
	}
	for _, c := range []ok{
		{"30d", 30 * day}, {"1d12h", 36 * time.Hour}, {"1.5d", 36 * time.Hour},
		{"-2d", -2 * day}, {"+1d30m", day + 30*time.Minute}, {"720h", 720 * time.Hour},
		{"90s", 90 * time.Second}, {"0", 0}, {"106751d", 106751 * day},
	} {
		got, err := parseDuration(c.in)
		assert.NilError(t, err, c.in)
		assert.Equal(t, got, c.want, c.in)
		assert.Assert(t, pattern.MatchString(c.in), "the pattern refuses %q, which parses", c.in)
	}
	for _, bad := range []string{"", "d", "30days", "1d-2h", "1h2d", "30x", "106752d", "106751d24h", "99999999999999999999d"} {
		_, err := parseDuration(bad)
		assert.Assert(t, err != nil, "%q parsed", bad)
	}
	for _, bad := range []string{"", "d", "30days", "1d-2h", "1h2d", "30x"} {
		assert.Assert(t, !pattern.MatchString(bad), "the pattern takes %q, which does not parse", bad)
	}
}

// A WHOLE NUMBER OF DAYS READS BACK AS DAYS; anything else in Go's grammar.
func TestADurationWritesWholeDaysAsDays(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * day: "30d", -day: "-1d", 36 * time.Hour: "36h0m0s", 0: "0s", time.Minute: "1m0s",
	} {
		assert.Equal(t, formatDuration(d), want)
		back, err := parseDuration(want)
		assert.NilError(t, err)
		assert.Equal(t, back, d)
	}
}
