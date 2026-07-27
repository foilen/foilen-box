package realm

import (
	"reflect"
	"testing"

	"foilen-realm/model"
)

func TestRingMemberIDsIncludesSelfAndOnlyConfirmedGroupMembers(t *testing.T) {
	known := []model.PeerInfo{
		{ID: "c", GroupNames: []string{"family"}},
		{ID: "a", GroupNames: []string{"work"}}, // different group, must be excluded
		{ID: "b", GroupNames: []string{"family", "work"}},
	}

	got := ringMemberIDs(known, "family", "self")
	want := []string{"b", "c", "self"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ringMemberIDs() = %v, want %v", got, want)
	}
}

func TestRingCandidateOrderWrapsAroundBothDirections(t *testing.T) {
	// Alphabetically: a, b, c(self), d, e
	members := []string{"a", "b", "c", "d", "e"}
	selfIdx := 2

	prev := ringCandidateOrder(members, selfIdx, -1)
	if want := []string{"b", "a", "e", "d"}; !reflect.DeepEqual(prev, want) {
		t.Errorf("ringCandidateOrder(-1) = %v, want %v", prev, want)
	}

	next := ringCandidateOrder(members, selfIdx, 1)
	if want := []string{"d", "e", "a", "b"}; !reflect.DeepEqual(next, want) {
		t.Errorf("ringCandidateOrder(+1) = %v, want %v", next, want)
	}
}

func TestRingCandidateOrderSmallGroupCanRepeatAcrossDirections(t *testing.T) {
	// Only one other member: both directions land on it.
	members := []string{"other", "self"}
	selfIdx := 1

	prev := ringCandidateOrder(members, selfIdx, -1)
	next := ringCandidateOrder(members, selfIdx, 1)
	if want := []string{"other"}; !reflect.DeepEqual(prev, want) || !reflect.DeepEqual(next, want) {
		t.Errorf("ringCandidateOrder() = prev %v next %v, want both %v", prev, next, want)
	}
}

func TestIndexOfString(t *testing.T) {
	list := []string{"a", "b", "c"}
	if got := indexOfString(list, "b"); got != 1 {
		t.Errorf("indexOfString(b) = %d, want 1", got)
	}
	if got := indexOfString(list, "z"); got != -1 {
		t.Errorf("indexOfString(z) = %d, want -1", got)
	}
}
