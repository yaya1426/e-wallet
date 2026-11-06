// Package clock provides the current time through an injectable interface (TDR-013) and
// the Africa/Cairo business day used by daily limits and daily settlement (PRD-001 §5.6).
package clock

import (
	"sort"
	"time"
	_ "time/tzdata" // embeds the zone database so Africa/Cairo resolves in every binary and test
)

// Clock returns the current time. Inject it wherever time matters so tests control it.
type Clock interface {
	Now() time.Time
}

// System is the real clock. Now is always in UTC.
type System struct{}

// Now returns the current time in UTC.
func (System) Now() time.Time { return time.Now().UTC() }

// Cairo is the Africa/Cairo location. It is loaded once and only read afterwards.
var Cairo = mustLoad("Africa/Cairo")

// searchWindow bounds the search for the first instant of a day around its nominal midnight;
// it is wider than any UTC offset change.
const searchWindow = 3 * time.Hour

// CairoDay returns the Africa/Cairo calendar day that contains t as the half-open interval
// [start, end) of UTC instants. It is exact on DST days, when the day lasts 23 or 25 hours.
func CairoDay(t time.Time) (start, end time.Time) {
	y, m, d := t.In(Cairo).Date()
	return dayStart(y, m, d), dayStart(y, m, d+1)
}

// dayStart returns the first instant whose Cairo date is y-m-d. It does not rely on
// time.Date for a midnight that may not exist (DST start): it searches, to the second,
// around the nominal midnight for the first instant whose date is not before the target.
func dayStart(y int, m time.Month, d int) time.Time {
	target := time.Date(y, m, d, 0, 0, 0, 0, time.UTC) // calendar date only; normalizes d+1
	nominal := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, Cairo)
	from := nominal.Add(-searchWindow).Truncate(time.Second)
	seconds := int(2 * searchWindow / time.Second)
	i := sort.Search(seconds, func(i int) bool {
		y, m, d := from.Add(time.Duration(i) * time.Second).In(Cairo).Date()
		return !time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Before(target)
	})
	return from.Add(time.Duration(i) * time.Second).UTC()
}

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// tzdata is embedded above, so this is a build error, not a runtime condition.
		panic("clock: load location " + name + ": " + err.Error())
	}
	return loc
}
