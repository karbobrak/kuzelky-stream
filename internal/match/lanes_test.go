package match

import "testing"

func TestLanePlan(t *testing.T) {
	r := DefaultRules()
	if r.LaneOf(0, "d", 0) != 0 {
		t.Fatal("no plan must give 0")
	}
	r.LanePlan = []LaneSlot{{Home: []int{1, 2, 1, 2}, Away: []int{2, 1, 2, 1}}, {Home: []int{3, 4, 3, 4}, Away: []int{4, 3, 4, 3}}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	// Duel 4 (index 3) is the 2nd duel of part 2 → slot 1.
	if got := r.LaneOf(3, "h", 1); got != 3 {
		t.Fatalf("LaneOf(3,h,1) = %d, want 3", got)
	}
	if got := r.LaneOf(2, "d", 2); got != 1 {
		t.Fatalf("LaneOf(2,d,2) = %d, want 1", got)
	}
	if r.LaneOf(0, "d", 9) != 0 {
		t.Fatal("segment out of plan must give 0")
	}
	if r.Equal(DefaultRules()) || !r.Equal(r) {
		t.Fatal("Equal ignores the lane plan")
	}
	empty := DefaultRules()
	empty.LanePlan = []LaneSlot{}
	if !empty.Equal(DefaultRules()) {
		t.Fatal("empty plan must equal no plan")
	}
	r.LanePlan[0].Home[0] = 0
	if r.Validate() == nil {
		t.Fatal("lane 0 accepted")
	}
}
