package joblog

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
)

func status(st plmv2.JobStatus) *plmv2.Job_Event {
	return &plmv2.Job_Event{At: 7, Kind: &plmv2.Job_Event_Status{Status: st}}
}

func message(text string) *plmv2.Job_Event {
	return &plmv2.Job_Event{At: 8, Kind: &plmv2.Job_Event_Message{Message: text}}
}

func TestALogIsItsEventsInOrderEachWithTheOffsetAfterIt(t *testing.T) {
	var log bytes.Buffer
	written := []*plmv2.Job_Event{status(plmv2.JobStatus_JobStatusClaimed), message("cloning"), message(""), status(plmv2.JobStatus_JobStatusSuccess)}
	var ends []int64
	for _, ev := range written {
		require.NoError(t, Write(&log, ev))
		ends = append(ends, int64(log.Len()))
	}

	events := Read(bytes.NewReader(log.Bytes()), 0)
	for i, want := range written {
		got, err := events.Next()
		require.NoError(t, err)
		require.Equal(t, want.GetKind(), got.GetKind())
		require.Equal(t, want.GetAt(), got.GetAt())
		require.Equal(t, ends[i], got.GetOffset())
		require.Equal(t, ends[i], events.Offset())
	}
	_, err := events.Next()
	require.ErrorIs(t, err, io.EOF)

	// A READ FROM A RECORD'S START is the rest of the log, offsets and all.
	from := ends[1]
	events = Read(bytes.NewReader(log.Bytes()[from:]), from)
	got, err := events.Next()
	require.NoError(t, err)
	require.Equal(t, "", got.GetMessage())
	require.Equal(t, ends[2], got.GetOffset())
}

func TestALogCutInARecordSaysSo(t *testing.T) {
	var log bytes.Buffer
	require.NoError(t, Write(&log, message("cloning")))
	for cut := 1; cut < log.Len(); cut++ {
		_, err := Read(bytes.NewReader(log.Bytes()[:cut]), 0).Next()
		require.ErrorIs(t, err, io.ErrUnexpectedEOF, "cut at %d", cut)
	}
	_, err := Read(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}), 0).Next()
	require.Error(t, err)
	require.False(t, errors.Is(err, io.EOF))
}

type refusing struct{}

func (refusing) Write([]byte) (int, error) { return 0, errors.New("the disk is full") }

func TestAnEventTheLogWillNotTakeIsAnError(t *testing.T) {
	require.ErrorContains(t, Write(refusing{}, message("x")), "the disk is full")
}

func TestTerminalIsTheThreeEnds(t *testing.T) {
	for st, want := range map[plmv2.JobStatus]bool{
		plmv2.JobStatus_JobStatusOpen: false, plmv2.JobStatus_JobStatusClaimed: false,
		plmv2.JobStatus_JobStatusSuccess: true, plmv2.JobStatus_JobStatusFailed: true, plmv2.JobStatus_JobStatusCancelled: true,
	} {
		require.Equal(t, want, Terminal(st), st.String())
	}
}
