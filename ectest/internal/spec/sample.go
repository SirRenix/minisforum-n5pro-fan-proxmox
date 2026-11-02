package spec

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Sample is one row of samples.csv (TESTSPEC §8). Unknown values are -1
// (integers) or NaN (temperatures, load); both are written as empty cells.
type Sample struct {
	Time    time.Time
	TS      float64 // seconds since the run started
	Phase   string
	Ch      int // channel under test, 0 = none
	Written int // duty written in this phase, -1 = none (EC automatic)
	DCR     [5]int
	Fan     [3]int
	ECCPU   float64
	ECSys   float64
	ECBoard float64
	ECAmb   float64
	HDDMax  float64
	NVMeMax float64
	Tctl    float64
	Load1   float64
	RoomT   float64 // reference rig only; NaN here
	RoomRH  float64
}

// NewSample returns a sample with every value unknown.
func NewSample(t time.Time, ts float64, phase string, ch, written int) Sample {
	n := math.NaN()
	return Sample{Time: t, TS: ts, Phase: phase, Ch: ch, Written: written,
		DCR: [5]int{-1, -1, -1, -1, -1}, Fan: [3]int{-1, -1, -1},
		ECCPU: n, ECSys: n, ECBoard: n, ECAmb: n, HDDMax: n, NVMeMax: n, Tctl: n, Load1: n, RoomT: n, RoomRH: n}
}

// CSVHeader is the column list of samples.csv, in the order of TESTSPEC §8.
var CSVHeader = []string{"t_iso", "t_s", "phase", "ch", "written", "dcr1", "dcr2", "dcr3", "dcr4", "dcr5", "fan1", "fan2", "fan3", "ec_cpu", "ec_sys", "ec_board", "ec_amb", "hdd_max", "nvme_max", "tctl", "load1", "room_t", "room_rh"}

func cellInt(v int) string {
	if v < 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func cellF(v float64, prec int) string {
	if math.IsNaN(v) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', prec, 64)
}

// RoundMS is t as samples.csv and events.log store it (3 decimals).
func RoundMS(t float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(t, 'f', 3, 64), 64)
	return v
}

// SinceMS is the time from b to a computed on their stored ms values, so
// runner and report get the same number at the 60/900/1020 s limits.
func SinceMS(a, b float64) float64 { return RoundMS(RoundMS(a) - RoundMS(b)) }

// Row renders the sample as a samples.csv row.
func (s Sample) Row() []string {
	// t_s with ms: the report rebuilds "seconds after the auto command" from
	// it and the event times
	r := []string{s.Time.UTC().Format(time.RFC3339), strconv.FormatFloat(s.TS, 'f', 3, 64), s.Phase, cellInt(s.Ch), cellInt(s.Written)}
	for _, v := range s.DCR {
		r = append(r, cellInt(v))
	}
	for _, v := range s.Fan {
		r = append(r, cellInt(v))
	}
	for _, v := range []float64{s.ECCPU, s.ECSys, s.ECBoard, s.ECAmb, s.HDDMax, s.NVMeMax, s.Tctl} {
		r = append(r, cellF(v, 1))
	}
	r = append(r, cellF(s.Load1, 2), cellF(s.RoomT, 1), cellF(s.RoomRH, 1))
	return r
}

// ParseRow is the inverse of Row; header is the CSV header of the file
// (columns are looked up by name).
func ParseRow(header, row []string) (Sample, error) {
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	get := func(name string) string {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	s := NewSample(time.Time{}, 0, get("phase"), -1, -1)
	var err error
	if v := get("t_iso"); v != "" {
		if s.Time, err = time.Parse(time.RFC3339, v); err != nil {
			return s, fmt.Errorf("t_iso %q: %w", v, err)
		}
	}
	if v := get("t_s"); v != "" {
		if s.TS, err = strconv.ParseFloat(v, 64); err != nil {
			return s, fmt.Errorf("t_s %q: %w", v, err)
		}
	}
	pi := func(name string) (int, error) {
		v := get(name)
		if v == "" {
			return -1, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return -1, fmt.Errorf("%s %q: %w", name, v, err)
		}
		return n, nil
	}
	pf := func(name string) (float64, error) {
		v := get(name)
		if v == "" {
			return math.NaN(), nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return math.NaN(), fmt.Errorf("%s %q: %w", name, v, err)
		}
		return f, nil
	}
	if s.Ch, err = pi("ch"); err != nil {
		return s, err
	}
	if s.Ch < 0 {
		s.Ch = 0
	}
	if s.Written, err = pi("written"); err != nil {
		return s, err
	}
	for i := 0; i < 5; i++ {
		if s.DCR[i], err = pi(fmt.Sprintf("dcr%d", i+1)); err != nil {
			return s, err
		}
	}
	for i := 0; i < 3; i++ {
		if s.Fan[i], err = pi(fmt.Sprintf("fan%d", i+1)); err != nil {
			return s, err
		}
	}
	for name, dst := range map[string]*float64{"ec_cpu": &s.ECCPU, "ec_sys": &s.ECSys, "ec_board": &s.ECBoard, "ec_amb": &s.ECAmb,
		"hdd_max": &s.HDDMax, "nvme_max": &s.NVMeMax, "tctl": &s.Tctl, "load1": &s.Load1, "room_t": &s.RoomT, "room_rh": &s.RoomRH} {
		if *dst, err = pf(name); err != nil {
			return s, err
		}
	}
	return s, nil
}

// Gate is one precondition of TESTSPEC §3 (plus the tool's own board and
// pwm gates, design §4).
type Gate struct {
	ID        string `json:"id"`
	Condition string `json:"condition"`
	Result    string `json:"result"` // PASS, FAIL, PARTIAL, SKIPPED
	Detail    string `json:"detail"`
}

// Phase names of the writing procedures; the report rebuilds the tests
// from them.
const (
	PhaseBoot   = "boot-state"
	PhaseGate   = "gate"
	PhaseT1Rest = "t1-rest"
	PhaseT2Pre  = "t2-pre"
)

// PhaseT1Step is the phase of the T1 step with duty d.
func PhaseT1Step(d int) string { return fmt.Sprintf("t1-%d", d) }

// PhaseT2Hold and PhaseT2Auto are the phases of sub-run label of run rep.
func PhaseT2Hold(rep int, label string) string { return fmt.Sprintf("t2-r%d-%s-hold", rep, label) }
func PhaseT2Auto(rep int, label string) string { return fmt.Sprintf("t2-r%d-%s-auto", rep, label) }
