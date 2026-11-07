package doctor

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func fakes() deps {
	return deps{
		github: func(string) (http.Header, int, error) {
			h := http.Header{}
			h.Set("X-RateLimit-Remaining", "4321")
			h.Set("Date", now.Format(http.TimeFormat))
			return h, 200, nil
		},
		universes:    func() ([]string, error) { return []string{"blackhole"}, nil },
		docker:       func() error { return nil },
		lookup:       func(string) error { return nil },
		dreamVersion: func() (string, error) { return "dream 1.0", nil },
		now:          func() time.Time { return now },
	}
}

func status(results []result) map[string]result {
	m := map[string]result{}
	for _, r := range results {
		m[r.name] = r
	}
	return m
}

func TestDoctorAllPass(t *testing.T) {
	p := config.Profile{Token: "t", CloudType: common.DreamCloud, Cloud: "blackhole"}
	var buf bytes.Buffer
	if err := report(&buf, run(p, fakes())); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), "FAIL") || strings.Contains(buf.String(), "WARN") {
		t.Fatal(buf.String())
	}
}

func TestDoctorFailures(t *testing.T) {
	for _, tc := range []struct {
		name, check, status, want string
		profile                   config.Profile
		change                    func(*deps)
	}{
		{"bad token", "login", "FAIL", "tau login --new", config.Profile{Token: "t"}, func(d *deps) {
			d.github = func(string) (http.Header, int, error) { return http.Header{}, 401, nil }
		}},
		{"no token", "login", "FAIL", "tau login", config.Profile{}, nil},
		{"rate limit out", "rate limit", "FAIL", "resets at", config.Profile{Token: "t"}, func(d *deps) {
			d.github = func(string) (http.Header, int, error) {
				h := http.Header{}
				h.Set("X-RateLimit-Remaining", "0")
				h.Set("X-RateLimit-Reset", "1793000000")
				return h, 403, nil
			}
		}},
		{"rate limit low", "rate limit", "WARN", "12 GitHub calls", config.Profile{Token: "t"}, func(d *deps) {
			d.github = func(string) (http.Header, int, error) {
				h := http.Header{}
				h.Set("X-RateLimit-Remaining", "12")
				return h, 200, nil
			}
		}},
		{"clock skew", "clock", "WARN", "5m0s off", config.Profile{Token: "t"}, func(d *deps) {
			d.now = func() time.Time { return now.Add(5 * time.Minute) }
		}},
		{"wrong universe", "cloud", "FAIL", "--universe blackhole", config.Profile{CloudType: common.DreamCloud, Cloud: "blackhol"}, nil},
		{"dream down", "dream", "FAIL", "dream start", config.Profile{CloudType: common.DreamCloud, Cloud: "blackhole"}, func(d *deps) {
			d.universes = func() ([]string, error) { return nil, errors.New("connection refused") }
		}},
		{"docker down", "docker", "FAIL", "start Docker", config.Profile{CloudType: common.DreamCloud, Cloud: "blackhole"}, func(d *deps) {
			d.docker = func() error { return errors.New("no socket") }
		}},
		{"remote unresolved", "cloud", "FAIL", "--fqdn", config.Profile{CloudType: common.RemoteCloud, Cloud: "nope.example"}, func(d *deps) {
			d.lookup = func(string) error { return errors.New("no such host") }
		}},
		{"no dream cli", "dream version", "WARN", "@taubyte/dream", config.Profile{CloudType: common.DreamCloud, Cloud: "blackhole"}, func(d *deps) {
			d.dreamVersion = func() (string, error) { return "", errors.New("not found") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := fakes()
			if tc.change != nil {
				tc.change(&d)
			}
			r := status(run(tc.profile, d))[tc.check]
			if r.status != tc.status || !strings.Contains(r.msg+" "+r.fix, tc.want) {
				t.Fatalf("%s = %+v; want %s with %q", tc.check, r, tc.status, tc.want)
			}
		})
	}
}
