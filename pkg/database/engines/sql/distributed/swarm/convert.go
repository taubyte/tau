package swarm

import (
	"github.com/samyfodil/musql/replication"

	sqlsyncv2 "github.com/taubyte/tau/pkg/proto/gen/tau/sqlsync/v2"
)

// Field-for-field mapping between musql's replication values and the wire messages.

func vvToPB(vv replication.VersionVector) []*sqlsyncv2.VVEntry {
	out := make([]*sqlsyncv2.VVEntry, 0, len(vv))
	for site, seq := range vv {
		out = append(out, &sqlsyncv2.VVEntry{Site: site, Seq: seq})
	}
	return out
}

func vvFromPB(in []*sqlsyncv2.VVEntry) replication.VersionVector {
	out := make(replication.VersionVector, len(in))
	for _, e := range in {
		out[e.GetSite()] = e.GetSeq()
	}
	return out
}

func opToPB(op replication.Op) *sqlsyncv2.Op {
	cells := make([]*sqlsyncv2.Cell, 0, len(op.Cells))
	for _, c := range op.Cells {
		cells = append(cells, &sqlsyncv2.Cell{Col: c.Col, Type: uint32(c.Type), Val: c.Val})
	}
	return &sqlsyncv2.Op{
		Site: op.Site, Seq: op.Seq, Hlc: uint64(op.HLC), Tbl: op.Tbl,
		Pk: op.PK, Kind: uint32(op.Kind), Cells: cells,
	}
}

func opFromPB(op *sqlsyncv2.Op) replication.Op {
	cells := make([]replication.Cell, 0, len(op.GetCells()))
	for _, c := range op.GetCells() {
		cells = append(cells, replication.Cell{Col: c.GetCol(), Type: byte(c.GetType()), Val: c.GetVal()})
	}
	return replication.Op{
		Site: op.GetSite(), Seq: op.GetSeq(), HLC: replication.HLC(op.GetHlc()), Tbl: op.GetTbl(),
		PK: op.GetPk(), Kind: replication.OpKind(op.GetKind()), Cells: cells,
	}
}

func opsToPB(ops []replication.Op) []*sqlsyncv2.Op {
	out := make([]*sqlsyncv2.Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, opToPB(op))
	}
	return out
}

func opsFromPB(ops []*sqlsyncv2.Op) []replication.Op {
	out := make([]replication.Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, opFromPB(op))
	}
	return out
}

func acksToPB(acks replication.Acks) []*sqlsyncv2.AckEntry {
	out := make([]*sqlsyncv2.AckEntry, 0, len(acks))
	for site, a := range acks {
		out = append(out, &sqlsyncv2.AckEntry{Site: site, Hlc: uint64(a.HLC), Vv: vvToPB(a.VV)})
	}
	return out
}

func acksFromPB(in []*sqlsyncv2.AckEntry) replication.Acks {
	out := make(replication.Acks, len(in))
	for _, e := range in {
		out[e.GetSite()] = replication.Ack{HLC: replication.HLC(e.GetHlc()), VV: vvFromPB(e.GetVv())}
	}
	return out
}

func pageToPB(p replication.SnapshotPage) *sqlsyncv2.GetSnapshotResponse {
	out := &sqlsyncv2.GetSnapshotResponse{Ops: opsToPB(p.Ops), Floors: vvToPB(p.Floors), Next: p.Next}
	for site, lo := range p.Ranges {
		out.Ranges = append(out.Ranges, &sqlsyncv2.RangeEntry{Site: site, Lo: lo})
	}
	for _, c := range p.Cells {
		out.Cells = append(out.Cells, &sqlsyncv2.ClockCell{
			Tbl: c.Tbl, Pk: c.PK, Col: c.Col, Hlc: uint64(c.HLC), Site: c.Site, Val: c.Val,
		})
	}
	return out
}

func pageFromPB(p *sqlsyncv2.GetSnapshotResponse) replication.SnapshotPage {
	out := replication.SnapshotPage{Ops: opsFromPB(p.GetOps()), Floors: vvFromPB(p.GetFloors()), Next: p.GetNext()}
	if len(p.GetRanges()) > 0 {
		out.Ranges = make(map[string]uint64, len(p.GetRanges()))
		for _, r := range p.GetRanges() {
			out.Ranges[r.GetSite()] = r.GetLo()
		}
	}
	for _, c := range p.GetCells() {
		out.Cells = append(out.Cells, replication.ClockCell{
			Tbl: c.GetTbl(), PK: c.GetPk(), Col: c.GetCol(), HLC: replication.HLC(c.GetHlc()), Site: c.GetSite(), Val: c.GetVal(),
		})
	}
	return out
}
