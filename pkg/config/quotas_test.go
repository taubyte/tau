package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestQuotasFor(t *testing.T) {
	var src Source
	y := "quotas:\n  default: {storage-bytes: 100, resources: 5}\n  projects:\n    p1: {storage-bytes: 10}\n"
	if err := yaml.Unmarshal([]byte(y), &src); err != nil {
		t.Fatal(err)
	}
	if got := src.Quotas.For("p1"); got.StorageBytes != 10 || got.Resources != 5 {
		t.Fatalf("override: %+v", got)
	}
	if got := src.Quotas.For("other"); got.StorageBytes != 100 || got.Resources != 5 {
		t.Fatalf("default: %+v", got)
	}
	if got := (Quotas{}).For("p1"); got != (Caps{}) {
		t.Fatalf("zero must be unlimited: %+v", got)
	}
	if yaml.Unmarshal([]byte("quotas: {default: {resources: x}}"), &src) == nil {
		t.Fatal("bad yaml accepted")
	}
}
