package match

// DuelResult is the computed state of one duel. Points are only awarded for finished lane segments
// (both players entered); Live pins count toward the displayed sums only.
type DuelResult struct {
	HomeSum   int     `json:"dSoucet"` // finished segments + live
	AwaySum   int     `json:"hSoucet"`
	HomeSets  float64 `json:"dSB"`
	AwaySets  float64 `json:"hSB"`
	LanesDone int     `json:"useku"` // segments where both players are entered
	Finished  bool    `json:"dohrano"`
	HomePts   float64 `json:"dBody"` // duel point(s), only when Finished
	AwayPts   float64 `json:"hBody"`
	Started   bool    `json:"zacato"`
}

type Result struct {
	Duels     []DuelResult `json:"souboje"`
	HomePins  int          `json:"dKuzelky"`
	AwayPins  int          `json:"hKuzelky"`
	HomePts   float64      `json:"dBody"` // duel points so far (+ total-pins points when finished)
	AwayPts   float64      `json:"hBody"`
	Finished  bool         `json:"dohrano"`
	TotalPts  float64      `json:"bodyCelkem"` // points available in the match
	PartsDone int          `json:"castiDohrano"`
}

func val(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// split awards pts to the higher value, or half each on a draw.
func split(h, a, pts float64) (float64, float64) {
	switch {
	case h > a:
		return pts, 0
	case a > h:
		return 0, pts
	}
	return pts / 2, pts / 2
}

func Compute(m Match, r Rules) Result {
	res := Result{TotalPts: float64(r.PlayersPerTeam)*r.DuelPoint + r.TotalPinsPoints}
	allDone := len(m.Duels) > 0
	for _, d := range m.Duels {
		var dr DuelResult
		for _, p := range d.Lanes {
			dr.HomeSum += val(p.Home)
			dr.AwaySum += val(p.Away)
			if p.Home != nil || p.Away != nil {
				dr.Started = true
			}
			if p.Home == nil || p.Away == nil {
				continue
			}
			dr.LanesDone++
			switch {
			case *p.Home > *p.Away:
				dr.HomeSets += r.SetWin
			case *p.Away > *p.Home:
				dr.AwaySets += r.SetWin
			default:
				dr.HomeSets += r.SetDraw
				dr.AwaySets += r.SetDraw
			}
		}
		dr.HomeSum += val(d.Live.Home)
		dr.AwaySum += val(d.Live.Away)
		if d.Live.Home != nil || d.Live.Away != nil {
			dr.Started = true
		}
		dr.Finished = len(d.Lanes) > 0 && dr.LanesDone == len(d.Lanes)
		if dr.Finished {
			h, a := dr.HomeSets, dr.AwaySets
			if h == a && r.DuelTieByPins {
				h, a = float64(dr.HomeSum), float64(dr.AwaySum)
			}
			dr.HomePts, dr.AwayPts = split(h, a, r.DuelPoint)
			res.HomePts += dr.HomePts
			res.AwayPts += dr.AwayPts
		} else {
			allDone = false
		}
		res.HomePins += dr.HomeSum
		res.AwayPins += dr.AwaySum
		res.Duels = append(res.Duels, dr)
	}
	for p := 1; p <= r.Parts(); p++ {
		done := true
		for _, i := range r.PartDuels(p) {
			if i >= len(res.Duels) || !res.Duels[i].Finished {
				done = false
			}
		}
		if done {
			res.PartsDone++
		}
	}
	if allDone {
		res.Finished = true
		h, a := split(float64(res.HomePins), float64(res.AwayPins), r.TotalPinsPoints)
		res.HomePts += h
		res.AwayPts += a
	}
	return res
}
