package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A gate's waits are each shown to be waiting by what it prints before it
// blocks, so that a run has not started yet is a fact here, not a timing.

func TestASmallRunWaitsForAGate(t *testing.T) {
	c := newCapped(t)
	gate := c.launch(t, "gate", small, nil, "true").await(t)

	waiting := c.launch(t, "", small, nil, "true")
	waiting.says(t, "scripts/cap: waiting for "+c.slice+", held by a gate: sh -c ")
	waiting.says(t, gate.unit)
	assert.False(t, waiting.started(), "a small run does not share the slice with a gate")

	gate.kill(t)
	waiting.await(t)
	waiting.says(t, "scripts/cap: waited ")
}

func TestAGateWaitsForTheSmallRunsInItsSlice(t *testing.T) {
	c := newCapped(t)
	first := c.start(t, small, nil, "true")
	second := c.start(t, small, nil, "true")

	gate := c.launch(t, "gate", small, nil, "true")
	gate.says(t, "scripts/cap: this gate waits for the runs in "+c.slice+" to end:")
	gate.says(t, first.unit)
	gate.says(t, second.unit)

	first.kill(t)
	assert.False(t, gate.started(), "one small run is still in the slice")
	second.kill(t)
	gate.await(t)
}

func TestAWaitingGateIsNotStarvedBySmallRuns(t *testing.T) {
	c := newCapped(t)
	in := c.start(t, small, nil, "true")
	gate := c.launch(t, "gate", small, nil, "true")
	gate.says(t, "this gate waits for the runs in "+c.slice)

	late := c.launch(t, "", small, nil, "true")
	late.says(t, "held by a gate: ")
	assert.False(t, late.started(), "a small run that comes after a waiting gate does not get in ahead of it")

	in.kill(t)
	gate.await(t)
	assert.False(t, late.started())

	gate.kill(t)
	late.await(t)
}

// TestAWaiterNamesARunWhoseCommandHasNewlines starts a run whose script is
// two lines. systemd shows a description holding a newline as [unprintable],
// so the scope is described with the command on one line.
func TestAWaiterNamesARunWhoseCommandHasNewlines(t *testing.T) {
	c := newCapped(t)
	in := c.start(t, small, nil, "true\ntrue")

	gate := c.launch(t, "gate", small, nil, "true")
	gate.says(t, in.unit+": sh -c true true; ")
	assert.NotContains(t, gate.said(), "[unprintable]")
}

func TestGatesRunOneAtATime(t *testing.T) {
	c := newCapped(t)
	first := c.launch(t, "gate", small, nil, "true").await(t)

	second := c.launch(t, "gate", small, nil, "true")
	second.says(t, "held by a gate: ")
	assert.False(t, second.started())

	first.kill(t)
	second.await(t)
}

// TestAMakeRunIsAGateUnlessToldOtherwise runs a stand-in make, which is all a
// run's class is read from.
func TestAMakeRunIsAGateUnlessToldOtherwise(t *testing.T) {
	c := newCapped(t)
	makeBin := filepath.Join(t.TempDir(), "make")
	require.NoError(t, os.WriteFile(makeBin, []byte("#!/bin/sh\nexec \"$@\"\n"), 0o755))
	c.start(t, small, nil, "true")

	c.launch(t, "small", small, []string{makeBin}, "true").await(t)

	gate := c.launch(t, "", small, []string{makeBin}, "true")
	gate.says(t, "this gate waits for the runs in "+c.slice)
	assert.False(t, gate.started())
}

func TestAnUnknownClassIsRefused(t *testing.T) {
	c := newCapped(t)

	out, err := c.cmdAs(t.Context(), "heavy", small, "true").CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", out)
	assert.Equal(t, 64, exit.ExitCode())
	assert.Contains(t, string(out), `TAU_CAP_CLASS is gate or small, not "heavy"`)
}

// TestAGateHoldsTheSliceUntilItsLastProcessIsGone ends the gate's own process
// and leaves one it started, which is how a run whose make was killed goes on.
func TestAGateHoldsTheSliceUntilItsLastProcessIsGone(t *testing.T) {
	c := newCapped(t)
	gate := c.launch(t, "gate", small, nil, "(sleep 300 </dev/null >/dev/null 2>&1 &)").await(t)
	gate.end(t)

	waiting := c.launch(t, "", small, nil, "true")
	waiting.says(t, "held by a gate: ")
	assert.False(t, waiting.started(), "the gate's last process still holds the slice")

	gate.kill(t)
	waiting.await(t)
}

// TestAGateWaitsForWhatIsLeftInTheSlice leaves a process in the slice that
// holds no lock, as a run an older cap started does.
func TestAGateWaitsForWhatIsLeftInTheSlice(t *testing.T) {
	c := newCapped(t)
	left := c.start(t, small, nil, "(exec 7>&- 8>&- sleep 300 </dev/null >/dev/null 2>&1 &)")
	left.end(t)

	gate := c.launch(t, "gate", small, nil, "true")
	gate.says(t, "scripts/cap: this gate waits for what is still in "+c.slice+", holding no lock:")
	gate.says(t, left.unit)
	assert.False(t, gate.started())

	left.kill(t)
	gate.await(t)
}

// TestARunInsideARunTakesNoLock caps runs from inside runs in the same slice.
// Each inner call is part of the run it is in, and one that took the slice's
// lock would wait for that run, which waits for it.
func TestARunInsideARunTakesNoLock(t *testing.T) {
	c := newCapped(t)

	out := c.runAs(t, "gate", small, "sh", "-c", `"$0" -- echo gate; TAU_CAP_CLASS=small "$0" -- echo small`, c.script)
	assert.Equal(t, "gate\nsmall\n", out)

	out = c.run(t, small, "sh", "-c", `TAU_CAP_CLASS=gate "$0" -- echo gate`, c.script)
	assert.Equal(t, "gate\n", out)
}
