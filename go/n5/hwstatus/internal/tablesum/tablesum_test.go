package tablesum

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

const table = "| a | b |\n|---|---|\n| 1 | 2 |\n"

func TestSumCoversOnlyTheTable(t *testing.T) {
	ref := sha256.Sum256([]byte(table))
	want := hex.EncodeToString(ref[:])
	for name, src := range map[string]string{
		"plain":      "intro\n" + Begin + "\n" + table + End + "\nmore\n",
		"other text": "other intro\n\n" + Begin + "\n" + table + End + "\nchanged prose\n",
		"crlf":       strings.ReplaceAll("intro\n"+Begin+"\n"+table+End+"\n", "\n", "\r\n"),
		"end at eof": Begin + "\n" + table + End,
	} {
		got, err := Sum([]byte(src))
		if err != nil || got != want {
			t.Errorf("%s: %s, %v; want %s", name, got, err, want)
		}
	}
	changed, err := Sum([]byte(Begin + "\n" + strings.Replace(table, "| 1 |", "| 3 |", 1) + End + "\n"))
	if err != nil || changed == want {
		t.Fatalf("table change not detected: %s, %v", changed, err)
	}
	empty := sha256.Sum256(nil)
	if got, err := Sum([]byte(Begin + "\n" + End + "\n")); err != nil || got != hex.EncodeToString(empty[:]) {
		t.Fatalf("empty table: %s, %v", got, err)
	}
	for name, src := range map[string]string{
		"no markers": table,
		"no end":     Begin + "\n" + table,
		"end only":   table + End + "\n",
	} {
		if _, err := Sum([]byte(src)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
