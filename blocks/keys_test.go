package blocks

import (
	"context"
	"reflect"
	"testing"
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
