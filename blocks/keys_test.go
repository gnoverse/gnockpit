package blocks

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func operatorKeys(t *testing.T, s *Store, operator, profileKey string) []KeySpan {
	t.Helper()
	got, err := s.OperatorKeys(context.Background(), operator, profileKey)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func putEvents(t *testing.T, s *Store, height int64, evs ...Event) {
	t.Helper()
	if err := s.PutEvents(context.Background(), height, height-2, evs); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorKeysRotation(t *testing.T) {
	s := newTestStore(t)
	putEvents(t, s, 100, Event{Type: "ValoperRotated", Operator: "g1op", OldAddress: "g1old", NewAddress: "g1new", Applied: true})
	want := []KeySpan{{"g1old", 0, 99}, {"g1new", 100, 0}}
	if got := operatorKeys(t, s, "g1op", "g1new"); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}

func TestOperatorKeysAddedThenRemoved(t *testing.T) {
	s := newTestStore(t)
	putEvents(t, s, 50, Event{Type: "ValidatorAdded", Operator: "g1op", Address: "g1k", Power: 1, Applied: true})
	putEvents(t, s, 80, Event{Type: "ValidatorRemoved", Operator: "g1op", Address: "g1k", Applied: true})
	want := []KeySpan{{"g1k", 50, 79}}
	if got := operatorKeys(t, s, "g1op", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}

func TestOperatorKeysIgnoreUnappliedRotations(t *testing.T) {
	s := newTestStore(t)
	putEvents(t, s, 100, Event{Type: "ValoperRotated", Operator: "g1op", OldAddress: "g1old", NewAddress: "g1new", Applied: false})
	if got := operatorKeys(t, s, "g1op", ""); len(got) != 0 {
		t.Errorf("keys = %+v, want none: the rotation never reached the set", got)
	}
	if got, want := operatorKeys(t, s, "g1op", "g1p"), []KeySpan{{"g1p", 0, 0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys with a profile key = %+v, want %+v", got, want)
	}
}

func TestOperatorKeysProfileAlone(t *testing.T) {
	s := newTestStore(t)
	if got, want := operatorKeys(t, s, "g1op", "g1p"), []KeySpan{{"g1p", 0, 0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
	if got := operatorKeys(t, s, "g1op", ""); len(got) != 0 {
		t.Errorf("keys without a profile key = %+v, want none", got)
	}
}

func TestOperatorOf(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putEvents(t, s, 50, Event{Type: "ValidatorAdded", Operator: "g1op", Address: "g1k", Applied: true})
	putEvents(t, s, 100, Event{Type: "ValoperRotated", Operator: "g1op", OldAddress: "g1k", NewAddress: "g1new", Applied: true})
	for addr, want := range map[string]string{"g1k": "g1op", "g1new": "g1op", "g1nobody": ""} {
		if got, err := s.OperatorOf(ctx, addr); err != nil || got != want {
			t.Errorf("OperatorOf(%s) = %q, %v; want %q", addr, got, err, want)
		}
	}
}

func TestOperatorOfTakesTheLatestEvent(t *testing.T) {
	s := newTestStore(t)
	putEvents(t, s, 50, Event{Type: "ValidatorRemoved", Operator: "g1first", Address: "g1k", Applied: true})
	putEvents(t, s, 100, Event{Type: "ValidatorAdded", Operator: "g1second", Address: "g1k", Applied: true})
	if got, err := s.OperatorOf(context.Background(), "g1k"); err != nil || got != "g1second" {
		t.Errorf("OperatorOf = %q, %v; want g1second, the latest event's", got, err)
	}
}

func TestKnown(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	for addr, want := range map[string]bool{valG0Y: true, valHQH: false} {
		if got, err := s.Known(ctx, addr); err != nil || got != want {
			t.Errorf("Known(%s) = %v, %v; want %v", addr, got, err, want)
		}
	}
}

// writeSetBlocks stores heights from to to on the test set, valG0Y and
// val15T both members.
func writeSetBlocks(t *testing.T, s *Store, from, to int64) {
	t.Helper()
	putTestSet(t, s)
	var recs []Record
	for h := from; h <= to; h++ {
		recs = append(recs, Record{Height: h, Time: time.Unix(h, 0).UTC(), Proposer: valG0Y, ValsetHash: "SET"})
	}
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorKeysPowerChangeKeepsTheSpan(t *testing.T) {
	s := newTestStore(t)
	writeSetBlocks(t, s, 40, 90)
	// valG0Y is in the set below both: each ValidatorAdded is a power change.
	putEvents(t, s, 50, Event{Type: "ValidatorAdded", Operator: "g1op", Address: valG0Y, Power: 500, Applied: true})
	putEvents(t, s, 80, Event{Type: "ValidatorAdded", Operator: "g1op", Address: valG0Y, Power: 60, Applied: true})
	want := []KeySpan{{valG0Y, 0, 0}}
	if got := operatorKeys(t, s, "g1op", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}

func TestOperatorKeysJoinThenPowerChange(t *testing.T) {
	s := newTestStore(t)
	writeSetBlocks(t, s, 50, 90)
	// Block 49 is not stored: the addition at 50 is a join.
	putEvents(t, s, 50, Event{Type: "ValidatorAdded", Operator: "g1op", Address: valG0Y, Power: 1, Applied: true})
	putEvents(t, s, 80, Event{Type: "ValidatorAdded", Operator: "g1op", Address: valG0Y, Power: 60, Applied: true})
	want := []KeySpan{{valG0Y, 50, 0}}
	if got := operatorKeys(t, s, "g1op", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}

func TestOperatorKeysAddTheProfileKeyNoEventNames(t *testing.T) {
	s := newTestStore(t)
	putEvents(t, s, 100, Event{Type: "ValoperRotated", Operator: "g1op", OldAddress: "g1old", NewAddress: "g1new", Applied: true})
	// The profile's key rotated again in a block whose events are not read.
	want := []KeySpan{{"g1old", 0, 99}, {"g1new", 100, 0}, {"g1newer", 0, 0}}
	if got := operatorKeys(t, s, "g1op", "g1newer"); !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %+v, want %+v", got, want)
	}
}
