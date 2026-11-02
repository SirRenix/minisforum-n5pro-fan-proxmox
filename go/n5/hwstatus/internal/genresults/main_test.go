package main

import (
	"strings"
	"testing"
)

const good = "x\n<!-- hwstatus:begin -->\n" +
	"| Produkt | Board | BIOS | EC-FW | Status | Herkunft | Stand | Bemerkung |\n" +
	"|---|---|---|---|---|---|---|---|\n" +
	"| N5 PRO | F8NAA | 1.05 | 0.14 | experimental | eigen | 2026-10-09 | a `b` c |\n" +
	"| N5 | F8NAA | * | * | experimental | upstream | 2026-10-08 | n |\n" +
	"<!-- hwstatus:end -->\n"

func TestGenerate(t *testing.T) {
	out, err := Generate([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# generated: 2026-10-09\n", "N5 PRO\tF8NAA\t1.05\t0.14\texperimental\teigen\t2026-10-09\ta b c\n", "# source-table-sha256: "} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	again, _ := Generate([]byte(good))
	if again != out {
		t.Fatal("not deterministic")
	}
	// #45: prose outside the markers does not change the output, a table
	// change does
	if prose, _ := Generate([]byte("other prose\n" + good + "trailer\n")); prose != out {
		t.Fatalf("prose outside the table changed the output:\n%s", prose)
	}
	if tbl, _ := Generate([]byte(strings.Replace(good, "| n |", "| m |", 1))); tbl == out || !strings.Contains(tbl, "# source-table-sha256: ") {
		t.Fatal("table change kept the checksum")
	}
	for name, src := range map[string]string{
		"no end":     strings.Replace(good, "<!-- hwstatus:end -->\n", "", 1),
		"no markers": "| N5 | F8NAA | * | * | experimental | upstream | 2026-10-08 | n |\n",
		"columns":    strings.Replace(good, "| n |", "|", 1),
		"status":     strings.Replace(good, "| experimental | upstream", "| verified | upstream", 1),
		"origin":     strings.Replace(good, "| experimental | upstream", "| tested | upstream", 1),
		"empty":      "<!-- hwstatus:begin -->\n<!-- hwstatus:end -->\n",
	} {
		if _, err := Generate([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
