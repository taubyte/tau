//go:build ee

package main

// Mounts the ee taucorder handlers. They register themselves from an init();
// this seam only pulls the package in. Structural only — no layout or logic here.
import _ "github.com/taubyte/tau/ee/taucorder"
