package blocks

import (
	"reflect"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

func TestNewRecord(t *testing.T) {
	at := time.Date(2026, 11, 19, 2, 0, 0, 0, time.UTC)
	set := []Member{{Address: valG0Y, Power: 10}, {Address: val15T, Power: 10}, {Address: valHQH, Power: 10}, {Address: valH97, Power: 10}}
	c := node.Commit{
		Height: 42, Time: at, Proposer: valG0Y, NumTxs: 2, ValidatorsHash: "SET", BlockHash: "B42",
		Precommits: []*node.Precommit{
			{Address: valG0Y, Index: 0, Round: 1, BlockHash: "B42", Time: at.Add(1 * time.Second)},
			nil,
			{Address: valHQH, Index: 2, Round: 1, BlockHash: "", Time: at.Add(2 * time.Second)},
			{Address: valH97, Index: 3, Round: 1, BlockHash: "B41", Time: at.Add(5 * time.Second)},
		},
	}
	got, err := newRecord(c, set)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{
		Height: 42, Time: at, Proposer: valG0Y, Round: 1, Txs: 2, ValsetHash: "SET",
		Absences: []Absence{{val15T, Absent}, {valHQH, Nil}, {valH97, OtherBlock}},
		// The weighted median of 1 s, 2 s and 5 s, 10 power each, is 2 s.
		Delays: []Delay{{valG0Y, -1000}, {valHQH, 0}, {valH97, 3000}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("newRecord() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestNewRecordWeightsTheMedianByPower(t *testing.T) {
	at := time.Date(2026, 11, 19, 2, 0, 0, 0, time.UTC)
	set := []Member{{Address: valG0Y, Power: 70}, {Address: val15T, Power: 30}}
	c := node.Commit{
		Height: 42, BlockHash: "B42",
		Precommits: []*node.Precommit{
			{Address: valG0Y, BlockHash: "B42", Time: at.Add(4 * time.Second)},
			{Address: val15T, BlockHash: "B42", Time: at.Add(1 * time.Second)},
		},
	}
	got, err := newRecord(c, set)
	if err != nil {
		t.Fatal(err)
	}
	// Half the power, 50, is reached at valG0Y's 4 s.
	if want := []Delay{{valG0Y, 0}, {val15T, -3000}}; !reflect.DeepEqual(got.Delays, want) {
		t.Errorf("delays = %+v, want %+v", got.Delays, want)
	}
}

func TestNewRecordRejectsAForeignSet(t *testing.T) {
	set := []Member{{Address: valG0Y, Power: 1}}
	for _, c := range []node.Commit{
		{Height: 7, Precommits: []*node.Precommit{{Address: valG0Y}, nil}},
		{Height: 7, Precommits: []*node.Precommit{{Address: val15T}}},
	} {
		if _, err := newRecord(c, set); err == nil {
			t.Errorf("newRecord(%+v) accepted a commit that does not fit the set", c)
		}
	}
}

func TestNewRecordWithoutPrecommits(t *testing.T) {
	got, err := newRecord(node.Commit{Height: 7, Precommits: []*node.Precommit{nil}}, []Member{{Address: valG0Y, Power: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Delays) != 0 || !reflect.DeepEqual(got.Absences, []Absence{{valG0Y, Absent}}) {
		t.Errorf("newRecord() = %+v, want one absence and no delay", got)
	}
}
