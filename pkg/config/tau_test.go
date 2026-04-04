package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSource_Cluster_YAMLUnmarshal(t *testing.T) {
	t.Run("with cluster set", func(t *testing.T) {
		data := []byte("cluster: build\nservices: [patrick, monkey]")
		var src Source
		if err := yaml.Unmarshal(data, &src); err != nil {
			t.Fatal(err)
		}
		if src.Cluster != "build" {
			t.Errorf("Source.Cluster = %q, want %q", src.Cluster, "build")
		}
	})

	t.Run("without cluster", func(t *testing.T) {
		data := []byte("services: [patrick]")
		var src Source
		if err := yaml.Unmarshal(data, &src); err != nil {
			t.Fatal(err)
		}
		if src.Cluster != "" {
			t.Errorf("Source.Cluster = %q, want empty", src.Cluster)
		}
	})
}

func TestNode_Validate_DefaultsClusterToMain(t *testing.T) {
	cfg, err := New(
		WithRoot("/tb"),
		WithP2PListen([]string{"/ip4/0.0.0.0/tcp/4001"}),
		WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/4001"}),
		WithPrivateKey(make([]byte, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster() != "main" {
		t.Errorf("Cluster() after New = %q, want main", cfg.Cluster())
	}
}

func TestNode_Validate_KeepsNonEmptyCluster(t *testing.T) {
	cfg, err := New(
		WithRoot("/tb"),
		WithP2PListen([]string{"/ip4/0.0.0.0/tcp/4001"}),
		WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/4001"}),
		WithPrivateKey(make([]byte, 32)),
		WithCluster("build"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster() != "build" {
		t.Errorf("Cluster() after New = %q, want build", cfg.Cluster())
	}
}
