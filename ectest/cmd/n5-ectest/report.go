package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/bundle"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/report"
)

func init() {
	register("report", command{usage: "report DIR [--out FILE]", help: "recompute every verdict of a bundle, check SHA256SUMS, write one offline HTML file", run: cmdReport})
}

// cmdReport recomputes the verdicts of a bundle and writes the HTML report
// beside it (<DIR>.report.html; the bundle itself stays as checksummed).
// Exit 0: verdicts and gates recomputed and equal to the stored ones,
// checksums fine; 1: a mismatch, a checksum problem, or a bundle of a run
// that did not finish (no meta.json or only the provisional one).
func cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "output file (default <DIR>.report.html)")
	if rc, ok := parseFlags(fs, args, map[string]bool{"out": true}); !ok {
		return rc
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: n5-ectest report DIR [--out FILE]")
		return exitUsage
	}
	dir := filepath.Clean(fs.Arg(0))
	l, err := bundle.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "report: %v\n", err)
		return exitFail
	}
	r, err := report.Recompute(l)
	if err != nil {
		fmt.Fprintf(stderr, "report: %v\n", err)
		return exitFail
	}
	page := report.HTML(l, r)
	if *out == "" {
		*out = dir + ".report.html"
	}
	if err := bundle.WriteAtomic(*out, []byte(page), 0o644); err != nil {
		fmt.Fprintf(stderr, "report: %v\n", err)
		return exitFail
	}
	for _, t := range r.Tests {
		reason := ""
		if len(t.Reasons) > 0 {
			reason = t.Reasons[0]
		}
		fmt.Fprintf(stdout, "%-3s %-12s %s\n", t.ID, t.Verdict, reason)
	}
	rc := exitOK
	for _, m := range r.Mismatches {
		fmt.Fprintf(stdout, "MISMATCH %s: %s %s, recomputed %s\n", m.ID, m.Source(), m.Stored, m.Recomputed)
		rc = exitFail
	}
	for _, n := range r.Notes {
		fmt.Fprintf(stdout, "note: %s\n", n)
	}
	if len(l.SumProblems) > 0 {
		fmt.Fprintf(stdout, "SHA256SUMS: %s\n", strings.Join(l.SumProblems, "; "))
		rc = exitFail
	}
	if len(l.MetaRaw) == 0 || r.Meta.Provisional {
		rc = exitFail
	}
	fmt.Fprintf(stdout, "report: %s\n", *out)
	return rc
}
