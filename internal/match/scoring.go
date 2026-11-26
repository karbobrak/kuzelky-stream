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
	Home      Totals  `json:"dPodrobne"` // full / clearing / errors (detailed scoring only)
	Away      Totals  `json:"hPodrobne"`
}

// Totals of full throws, clearing and errors over the entered segments with details.
type Totals struct {
	Full   int `json:"p"`
	Clear  int `json:"d"`
	Errors int `json:"ch"`
}

func (t *Totals) add(d *Detail) {
	if d == nil {
		return
	}
	t.Full += val(d.Full)
	t.Clear += val(d.Clear)
	t.Errors += val(d.Errors)
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
	HomeSets  float64      `json:"dSety"` // set points of all duels together ("Sety 16 : 8")
	AwaySets  float64      `json:"hSety"`
	Home      Totals       `json:"dPodrobne"`
	Away      Totals       `json:"hPodrobne"`
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
		zero := 0
		for _, p := range d.Lanes {
			// An empty place in the line-up scores 0 on every segment.
			if d.HomeAbsent {
				p.Home = &zero
			}
			if d.AwayAbsent {
				p.Away = &zero
			}
			dr.Home.add(p.HomeDet)
			dr.Away.add(p.AwayDet)
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
			if r.DuelByPins || (h == a && r.DuelTieByPins) {
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
		res.HomeSets += dr.HomeSets
		res.AwaySets += dr.AwaySets
		res.Home.Full, res.Home.Clear, res.Home.Errors = res.Home.Full+dr.Home.Full, res.Home.Clear+dr.Home.Clear, res.Home.Errors+dr.Home.Errors
		res.Away.Full, res.Away.Clear, res.Away.Errors = res.Away.Full+dr.Away.Full, res.Away.Clear+dr.Away.Clear, res.Away.Errors+dr.Away.Errors
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
	// The points for the higher team total are shown during the match too (provisionally, as on
	// vysledky.kuzelky.cz), once any pins are entered; they are final when all duels are finished.
	res.Finished = allDone
	if allDone || res.HomePins+res.AwayPins > 0 {
		h, a := split(float64(res.HomePins), float64(res.AwayPins), r.TotalPinsPoints)
		res.HomePts += h
		res.AwayPts += a
	}
	return res
}
