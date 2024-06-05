package new

import (
	"fmt"
	"reflect"
	"testing"
)

func TestBuildServiceConfig(t *testing.T) {
	enable := func(s ...string) []string { return s }
	disable := func(s ...string) []string { return s }
	bind := func(s ...string) []string { return s }

	// Should work with empty
	c, err := buildServiceConfig(
		enable(),  // Enable
		disable(), // Disable
		bind(),    // Bind
	)
	if c == nil {
		t.Error("c is nil")
		return
	}
	if err != nil {
		t.Error(err)
		return
	}

	// Should block setting enable and disable
	_, err = buildServiceConfig(
		enable("seer"), // Enable
		disable("tns"), // Disable
		bind(),         // Bind
	)
	if err == nil {
		t.Error("Expected error")
		return
	}

	// Should disable
	c, err = buildServiceConfig(
		enable(),       // Enable
		disable("tns"), // Disable
		bind(),         // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}
	if len(c) <= 1 {
		t.Error("Only found one service")
		return
	}
	for idx := range c {
		if idx == "tns" {
			t.Error("tns wasn't disabled")
			return
		}
	}

	// Should enable only listed
	c, err = buildServiceConfig(
		enable("tns"), // Enable
		disable(),     // Disable
		bind(),        // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}
	if len(c) != 1 {
		t.Error("Should have only enabled one service")
		return
	}
	found := false
	for idx := range c {
		if idx == "tns" {
			found = true

		}
	}
	if !found {
		t.Error("tns wasn't enabled")
		return
	}

	// Should fail to bind disabled
	_, err = buildServiceConfig(
		enable(),              // Enable
		disable("tns"),        // Disable
		bind("tns@4040/http"), // Bind
	)
	if err == nil {
		t.Error("Expected to error")
		return
	}

	// Should fail to bind not enabled
	_, err = buildServiceConfig(
		enable("seer"),        // Enable
		disable(),             // Disable
		bind("tns@4040/http"), // Bind
	)
	if err == nil {
		t.Error("Expected to error")
		return
	}

	// should bind http
	c, err = buildServiceConfig(
		enable("tns"), // Enable
		disable(),     // Disable
		bind("tns@4040/http", "tns@4041/p2p", "tns@4042"), // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}

	err = notEqual(c["tns"].Others, map[string]int{"http": 4040, "p2p": 4041, "main": 4042})
	if err != nil {
		t.Error(err)
		return
	}

	// Shouldn't be able to bind the same port on a single service
	_, err = buildServiceConfig(
		enable(),  // Enable
		disable(), // Disable
		bind("tns@4040/http", "seer@4040/p2p", "tns@4042"), // Bind
	)
	if err == nil { // Attempted duplicate port bindings [tns@4040/http] and [seer@4040/p2p]
		t.Error("Expected error")
		return
	}

	// complex equality test
	c, err = buildServiceConfig(
		enable(),  // Enable
		disable(), // Disable
		bind("tns@4040/http", "tns@4041/p2p", "tns@4042", "seer@4043/http", "seer@4044/p2p", "seer@4045", "patrick@4046/http", "patrick@4047/p2p", "patrick@4048"), // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}
	err = notEqual(c["tns"].Others, map[string]int{"http": 4040, "p2p": 4041, "main": 4042})
	if err != nil {
		t.Error(err)
		return
	}
	err = notEqual(c["seer"].Others, map[string]int{"http": 4043, "p2p": 4044, "main": 4045})
	if err != nil {
		t.Error(err)
		return
	}
	err = notEqual(c["patrick"].Others, map[string]int{"http": 4046, "p2p": 4047, "main": 4048})
	if err != nil {
		t.Error(err)
		return
	}

	// Testing dns configurable port
	c, err = buildServiceConfig(
		enable(),              // Enable
		disable(),             // Disable
		bind("seer@8099/dns"), // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}
	err = notEqual(c["seer"].Others, map[string]int{"dns": 8099})
	if err != nil {
		t.Error(err)
		return
	}

	// testing https
	c, err = buildServiceConfig(
		enable(),                // Enable
		disable(),               // Disable
		bind("seer@8099/https"), // Bind
	)
	if err != nil {
		t.Error(err)
		return
	}
	err = notEqual(c["seer"].Others, map[string]int{"http": 8099, "secure": 1})
	if err != nil {
		t.Error(err)
		return
	}
}

func notEqual(a, b interface{}) error {
	if reflect.DeepEqual(a, b) == false {
		return fmt.Errorf("%v != %v", a, b)
	}

	return nil
}
