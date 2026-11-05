package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/n5driver"
)

// Settle time of a duty-register comparison after a write (TESTSPEC 0.4
// §2.1, #21; measured in maintenance window 1, 2026-10-17): the EC takes a
// pwm4 write over within about 0.2 s (Z1) and ramps DCR3, then DCR4 towards
// a written value (Z2: about 1.5 s and 4.6 s). A register read earlier than
// SettleTime(ch) after the write is recorded, never judged.
const SettlePoll = 200 * time.Millisecond

// SettleTime is the settle time of channel ch after a write: 10 s for pwm3,
// 1 s for the others (spec.SettleS).
func SettleTime(ch int) time.Duration {
	return time.Duration(spec.SettleS(ch) * float64(time.Second))
}

// Settled is the outcome of WaitDCR.
type Settled struct {
	DCR     n5driver.DCR
	After   time.Duration // time from the write to the last read
	Reached bool          // every register showed the target
	Err     error         // the first read failed: registers not available
}

// Text is a one-line summary for the event log ("DCR3=140 DCR4=140 after
// 4.6 s").
func (s Settled) Text(regs []int) string {
	if s.Err != nil {
		return fmt.Sprintf("not readable after %.1f s: %v", s.After.Seconds(), s.Err)
	}
	var parts []string
	for _, n := range regs {
		parts = append(parts, fmt.Sprintf("DCR%d=%d", n, s.DCR.Reg(n)))
	}
	return fmt.Sprintf("%s after %.1f s", strings.Join(parts, " "), s.After.Seconds())
}

// WaitDCR polls read every SettlePoll from the write at t0 until every
// register in regs equals want and at least min has passed, or until
// timeout. now and sleep are the clock of the caller (Env, or the real one);
// sleep returning false (cancelled) ends the wait with the last reading. A
// failing first read ends it at once (Err); a later failing read keeps the
// last good reading and polls on.
func WaitDCR(ctx context.Context, now func() time.Time, sleep func(context.Context, time.Duration) bool,
	read func() (n5driver.DCR, error), t0 time.Time, regs []int, want int, min, timeout time.Duration) Settled {
	var s Settled
	have := false
	for {
		el := now().Sub(t0)
		if el >= min {
			d, err := read()
			s.After = el
			if err == nil {
				s.DCR, s.Err, have = d, nil, true
				ok := len(regs) > 0
				for _, n := range regs {
					if d.Reg(n) != want {
						ok = false
					}
				}
				if ok {
					s.Reached = true
					return s
				}
			} else if !have {
				// not available (Env.DCR contract): nothing to wait for
				s.Err = err
				return s
			}
			if el >= timeout {
				return s
			}
		}
		wait := SettlePoll
		if el < min && min-el < wait {
			wait = min - el
		}
		if !sleep(ctx, wait) {
			return s
		}
	}
}
