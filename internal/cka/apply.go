package cka

import (
	"strconv"

	"kuzelkystream/internal/match"
)

// lane returns the result of segment j (0-based) of a player, nil when nothing is entered yet.
func lane(p *PlayerResult, j int) *LaneResult {
	if p == nil {
		return nil
	}
	for i := range p.LaneResults {
		l := &p.LaneResults[i]
		if l.LaneNumber != j+1 {
			continue
		}
		for _, v := range []*int{l.Full, l.Spare, l.Errors, l.Total} {
			if v != nil && *v > 0 {
				return l
			}
		}
		return nil // an empty row (all zero) is a segment not played yet
	}
	return nil
}

func (l *LaneResult) total() *int {
	if l == nil {
		return nil
	}
	if l.Total != nil {
		v := *l.Total
		return &v
	}
	v := 0
	for _, x := range []*int{l.Full, l.Spare} {
		if x != nil {
			v += *x
		}
	}
	return &v
}

func (l *LaneResult) detail() *match.Detail {
	if l == nil {
		return nil
	}
	cp := func(p *int) *int {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	return &match.Detail{Full: cp(l.Full), Clear: cp(l.Spare), Errors: cp(l.Errors)}
}

func (t *TeamResult) at(pos int) *PlayerResult {
	if t == nil {
		return nil
	}
	for i := range t.PlayerResults {
		if t.PlayerResults[i].Position == pos {
			return &t.PlayerResults[i]
		}
	}
	return nil
}

// subFor finds the substitution of the starting player p.
func (t *TeamResult) subFor(p *PlayerResult) *match.Sub {
	if t == nil || p == nil {
		return nil
	}
	for _, s := range t.Substitutions {
		if s.PlayerOut.ID == p.Player.ID && s.PlayerIn.Name() != "" {
			return &match.Sub{Name: s.PlayerIn.Name(), FromThrow: s.ThrowNumber}
		}
	}
	return nil
}

// lastSegment is the last segment (0-based) with a result in duel position pos, -1 if none.
func (m *Match) lastSegment(pos, lanes int) int {
	last := -1
	for _, home := range []bool{true, false} {
		p := m.Team(home).at(pos)
		for j := 0; j < lanes; j++ {
			if lane(p, j) != nil && j > last {
				last = j
			}
		}
	}
	return last
}

// Apply copies the published line-up and results into dst, which keeps everything the web does
// not know (short team names, graphics mode, message, interruption). The results service has no
// "segment finished" flag, so the last segment with data counts as being played (it goes to the
// live column, without set points) until the players start the next segment, a later part of the
// match starts, or the match is finished. Returns the part being played (0 when unknown/finished).
func (m *Match) Apply(dst *match.Match, r match.Rules) int {
	if m.HomeTeam.Name != "" {
		dst.Home.Name = m.HomeTeam.Name
	}
	if m.AwayTeam.Name != "" {
		dst.Away.Name = m.AwayTeam.Name
	}
	if m.Date != "" {
		dst.Date = m.Date
	}
	if m.Competition.Name != "" {
		dst.Competition = m.Competition.Name
	}
	if m.Round > 0 {
		dst.Round = strconv.Itoa(m.Round)
	}
	dst.Reshape(r)
	finished := m.Status == StatusFinished
	home, away := m.Team(true), m.Team(false)

	lastSeg := make([]int, len(dst.Duels))
	for i := range dst.Duels {
		lastSeg[i] = m.lastSegment(i+1, r.Lanes)
	}
	per := max(r.DuelsPerPart, 1)
	laterStarted := func(i int) bool { // has any duel of a later part got results?
		for k := (i/per + 1) * per; k < len(dst.Duels); k++ {
			if lastSeg[k] >= 0 {
				return true
			}
		}
		return false
	}

	livePart := 0
	for i := range dst.Duels {
		d := &dst.Duels[i]
		hp, ap := home.at(i+1), away.at(i+1)
		if hp != nil {
			d.HomePlayer = hp.Player.Name()
		}
		if ap != nil {
			d.AwayPlayer = ap.Player.Name()
		}
		// A published line-up without this position: the place is empty (SŘ čl. 15).
		d.HomeAbsent = home != nil && len(home.PlayerResults) > 0 && hp == nil
		d.AwayAbsent = away != nil && len(away.PlayerResults) > 0 && ap == nil
		d.HomeSub, d.AwaySub = home.subFor(hp), away.subFor(ap)

		last := lastSeg[i]
		done := finished || laterStarted(i)
		d.Live = match.Pair{}
		for j := range d.Lanes {
			hl, al := lane(hp, j), lane(ap, j)
			switch {
			case j > last:
				d.Lanes[j] = match.Pair{}
			case j < last || done:
				d.Lanes[j] = match.Pair{Home: hl.total(), Away: al.total(), HomeDet: hl.detail(), AwayDet: al.detail()}
			default: // the segment being played
				d.Lanes[j] = match.Pair{}
				d.Live = match.Pair{Home: hl.total(), Away: al.total()}
			}
		}
		if last >= 0 && !finished {
			livePart = max(livePart, i/per+1)
		}
	}
	return livePart
}
