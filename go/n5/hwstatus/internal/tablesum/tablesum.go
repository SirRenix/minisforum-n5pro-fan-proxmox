// Package tablesum computes the provenance checksum of the hardware status
// table in befunde/RESULTS.md. Only the table counts, so prose elsewhere in
// RESULTS.md can change without making the embedded copy stale.
//
// Definition (shared with tools/gen-hwstatus.sh of n5-fangov): sha256 over
// the lines after the line containing "<!-- hwstatus:begin -->" up to the
// line before the line containing "<!-- hwstatus:end -->", marker lines
// excluded, each line with a trailing CR removed and terminated by one LF.
// Shell equivalent:
//
//	awk '/<!-- hwstatus:end -->/{exit} on{sub(/\r$/,"");print} /<!-- hwstatus:begin -->/{on=1}' RESULTS.md | sha256sum
package tablesum

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const (
	Begin = "<!-- hwstatus:begin -->"
	End   = "<!-- hwstatus:end -->"
)

// Sum returns the hex sha256 of the table between the markers of src.
func Sum(src []byte) (string, error) {
	h := sha256.New()
	on := false
	for len(src) > 0 {
		line := src
		if i := bytes.IndexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			src = nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case !on && bytes.Contains(line, []byte(Begin)):
			on = true
		case on && bytes.Contains(line, []byte(End)):
			return hex.EncodeToString(h.Sum(nil)), nil
		case on:
			h.Write(line)
			h.Write([]byte{'\n'})
		}
	}
	return "", errors.New("RESULTS.md: hwstatus markers not found")
}
