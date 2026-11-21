package match

import "testing"

func ip(v int) *int { return &v }

// set fills duel d with the given home/away lane pins.
func set(m *Match, d int, home, away [4]int) {
	for i := 0; i < 4; i++ {
		m.Duels[d].Lanes[i] = Pair{Home: ip(home[i]), Away: ip(away[i])}
	}
}

func TestDuelBySets(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	set(&m, 0, [4]int{150, 140, 160, 130}, [4]int{140, 150, 150, 120}) // sets 3:1
	res := Compute(m, r)
	d := res.Duels[0]
	if d.HomeSets != 3 || d.AwaySets != 1 || !d.Finished || d.HomePts != 1 || d.AwayPts != 0 {
		t.Fatalf("duel = %+v", d)
	}
	if d.HomeSum != 580 || d.AwaySum != 560 {
		t.Fatalf("sums = %d:%d", d.HomeSum, d.AwaySum)
	}
}

func TestDuelSetDrawDecidedByPins(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	// 2:2 on sets, home has more pins in total → home wins the duel.
	set(&m, 0, [4]int{150, 150, 100, 100}, [4]int{140, 140, 101, 101})
	d := Compute(m, r).Duels[0]
	if d.HomeSets != 2 || d.AwaySets != 2 || d.HomePts != 1 || d.AwayPts != 0 {
		t.Fatalf("duel = %+v", d)
	}
	// Same pins as well → half a point each.
	set(&m, 0, [4]int{150, 150, 100, 100}, [4]int{150, 150, 100, 100})
	d = Compute(m, r).Duels[0]
	if d.HomeSets != 2 || d.HomePts != 0.5 || d.AwayPts != 0.5 {
		t.Fatalf("draw duel = %+v", d)
	}
	// Rule off: a set draw is a draw regardless of pins.
	r.DuelTieByPins = false
	set(&m, 0, [4]int{150, 150, 100, 100}, [4]int{140, 140, 101, 101})
	if d = Compute(m, r).Duels[0]; d.HomePts != 0.5 {
		t.Fatalf("without pin tie-break = %+v", d)
	}
}

func TestLaneDrawIsHalfSet(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	set(&m, 0, [4]int{150, 150, 150, 150}, [4]int{150, 140, 160, 150})
	d := Compute(m, r).Duels[0]
	if d.HomeSets != 2 || d.AwaySets != 2 {
		t.Fatalf("sets = %v:%v", d.HomeSets, d.AwaySets)
	}
}

func TestUnfinishedDuelAndLive(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	m.Duels[0].Lanes[0] = Pair{Home: ip(150), Away: ip(140)}
	m.Duels[0].Lanes[1] = Pair{Home: ip(145)} // away not entered yet
	m.Duels[0].Live = Pair{Home: ip(70), Away: ip(66)}
	res := Compute(m, r)
	d := res.Duels[0]
	if d.Finished || d.HomePts != 0 || d.LanesDone != 1 || d.HomeSets != 1 {
		t.Fatalf("duel = %+v", d)
	}
	if d.HomeSum != 150+145+70 || d.AwaySum != 140+66 {
		t.Fatalf("sums with live = %d:%d", d.HomeSum, d.AwaySum)
	}
	if res.Finished || res.HomePts != 0 {
		t.Fatalf("match = %+v", res)
	}
}

func TestWholeMatch(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	// Home wins 4 duels, away 2; home has more pins overall → 4+2 : 2 = 6:2.
	for i := 0; i < 6; i++ {
		if i < 4 {
			set(&m, i, [4]int{150, 150, 150, 150}, [4]int{140, 140, 140, 140})
		} else {
			set(&m, i, [4]int{140, 140, 140, 145}, [4]int{150, 150, 150, 150})
		}
	}
	res := Compute(m, r)
	if !res.Finished || res.HomePts != 6 || res.AwayPts != 2 || res.TotalPts != 8 || res.PartsDone != 3 {
		t.Fatalf("result = %+v", res)
	}
	if res.HomePins != 4*600+2*565 || res.AwayPins != 4*560+2*600 {
		t.Fatalf("pins = %d:%d", res.HomePins, res.AwayPins)
	}
}

func TestTotalPinsDraw(t *testing.T) {
	r := DefaultRules()
	m := New(r)
	for i := 0; i < 6; i++ {
		set(&m, i, [4]int{150, 150, 150, 150}, [4]int{150, 150, 150, 150})
	}
	res := Compute(m, r)
	if res.HomePts != 4 || res.AwayPts != 4 {
		t.Fatalf("all draws = %v:%v, want 4:4", res.HomePts, res.AwayPts)
	}
}

func TestReshapeAndParts(t *testing.T) {
	r := DefaultRules()
	if r.Parts() != 3 {
		t.Fatalf("parts = %d", r.Parts())
	}
	if got := r.PartDuels(2); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("part 2 = %v", got)
	}
	m := New(r)
	m.Duels[0].HomePlayer = "Novák"
	r.PlayersPerTeam, r.Lanes = 4, 2
	m.Reshape(r)
	if len(m.Duels) != 4 || len(m.Duels[0].Lanes) != 2 || m.Duels[0].HomePlayer != "Novák" {
		t.Fatalf("reshape = %+v", m)
	}
	if err := m.Validate(r); err != nil {
		t.Fatal(err)
	}
	m.Duels[1].Lanes[0].Home = ip(300)
	if m.Validate(r) == nil {
		t.Fatal("300 pins per lane must be rejected")
	}
}
