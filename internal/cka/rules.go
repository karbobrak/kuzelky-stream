package cka

import (
	"fmt"
	"regexp"
	"strconv"

	"kuzelkystream/internal/match"
)

// Rules adapts the competition rules to the published match: number of players (matchType) and
// lane segments and throws (discipline T120 = 4 × 30, T100 = 2 × 50 …). The points system is
// not taken from the web (its live points cannot tell it reliably); it stays as configured.
// The second value describes the rules for the log.
func (m *Match) Rules(base match.Rules) (match.Rules, string) {
	r := base
	n := players(m.MatchType)
	if n > 0 {
		r.PlayersPerTeam = n
		if r.DuelsPerPart > n {
			r.DuelsPerPart = n
		}
		r.MaxSubs = 1
		if n >= 6 {
			r.MaxSubs = 2
		}
	} else {
		n = r.PlayersPerTeam
	}
	if total, ok := discipline(m.Discipline); ok {
		segs := 1
		switch {
		case total >= 120:
			segs = 4
		case total >= 60:
			segs = 2
		}
		r.Lanes, r.ThrowsPerLane = segs, total/segs
		r.MaxPinsPerLane = r.ThrowsPerLane * 9
	}
	sys := "8 bodů"
	if r.DuelByPins {
		sys = "16 bodů"
	}
	return r, fmt.Sprintf("%d hs, %d úseky po %d hodech, %d hráčů, %s",
		r.Lanes*r.Throws(), r.Lanes, r.Throws(), r.PlayersPerTeam, sys)
}

var reTeams = regexp.MustCompile(`^TEAMS_OF_(\d+)$`)
var reDiscipline = regexp.MustCompile(`^T(\d+)$`)

func players(matchType string) int {
	if m := reTeams.FindStringSubmatch(matchType); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n >= 1 && n <= 12 {
			return n
		}
	}
	return 0
}

func discipline(d string) (int, bool) {
	if m := reDiscipline.FindStringSubmatch(d); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n >= 10 && n <= 400 {
			return n, true
		}
	}
	return 0, false
}
