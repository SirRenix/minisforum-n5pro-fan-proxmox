// Package bundle writes and reads one result directory (TESTSPEC §8):
// meta.json, samples.csv, events.log, verdict.json and further files of the
// command, with SHA256SUMS over all of them. events.log and samples.csv are
// written line by line while the run goes on, so a killed run leaves its
// data. Every text written through a Writer is redacted (host name, MAC/IP
// addresses, serials, home paths; design §8).
package bundle

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

// File names of a bundle.
const (
	Meta    = "meta.json"
	Samples = "samples.csv"
	Events  = "events.log"
	Verdict = "verdict.json"
	Sums    = "SHA256SUMS"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// DirName is "<YYYYMMDD-HHMM>-<board>-<bios>-<test>" (TESTSPEC §8), the
// time in UTC like every other time of a bundle.
func DirName(t time.Time, board, bios, test string) string {
	t = t.UTC()
	clean := func(s string) string {
		s = unsafeName.ReplaceAllString(strings.TrimSpace(s), "_")
		if s == "" {
			return "unknown"
		}
		return s
	}
	return fmt.Sprintf("%s-%s-%s-%s", t.Format("20060102-1504"), clean(board), clean(bios), clean(test))
}

// Writer is an open bundle directory.
type Writer struct {
	Dir     string
	red     *identity.Redactor
	mu      sync.Mutex
	events  *os.File
	samples *os.File
	csvw    *csv.Writer
	closed  bool
}

// Create makes dir (it must not exist) and opens events.log and
// samples.csv (with the header). red may be nil only in tests.
func Create(dir string, red *identity.Redactor) (*Writer, error) {
	if err := os.Mkdir(dir, 0o750); err != nil {
		return nil, err
	}
	w := &Writer{Dir: dir, red: red}
	var err error
	if w.events, err = os.OpenFile(filepath.Join(dir, Events), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640); err != nil {
		return nil, err
	}
	if w.samples, err = os.OpenFile(filepath.Join(dir, Samples), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640); err != nil {
		w.events.Close()
		return nil, err
	}
	w.csvw = csv.NewWriter(w.samples)
	_ = w.csvw.Write(spec.CSVHeader)
	w.csvw.Flush()
	return w, w.csvw.Error()
}

func (w *Writer) redact(s string) string {
	if w.red == nil {
		return s
	}
	return w.red.String(s)
}

// Event appends one line to events.log (and syncs it).
func (w *Writer) Event(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	_, _ = w.events.WriteString(w.redact(strings.TrimRight(line, "\n")) + "\n")
	_ = w.events.Sync()
}

// Sample appends one row to samples.csv.
func (w *Writer) Sample(s spec.Sample) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	_ = w.csvw.Write(s.Row())
	w.csvw.Flush()
	_ = w.samples.Sync()
}

// WriteJSON writes v as indented JSON (redacted) to name.
func (w *Writer) WriteJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.WriteFile(name, append(b, '\n'))
}

// WriteFile writes a further file of the bundle (redacted) atomically.
func (w *Writer) WriteFile(name string, data []byte) error {
	if strings.ContainsAny(name, `/\`) || name == Sums || name == Events || name == Samples {
		return fmt.Errorf("bundle: invalid file name %q", name)
	}
	return WriteAtomic(filepath.Join(w.Dir, name), []byte(w.redact(string(data))), 0o640)
}

// Finish closes the streamed files and writes SHA256SUMS.
func (w *Writer) Finish() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.csvw.Flush()
		w.samples.Close()
		w.events.Close()
	}
	w.mu.Unlock()
	return WriteSums(w.Dir)
}

// WriteAtomic writes data to p through a temporary file, fsync and rename.
func WriteAtomic(p string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, p); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// listFiles lists every regular file below dir (also in subdirectories,
// as slash-separated relative paths), except SHA256SUMS at the top and
// hidden files and directories (temporary files of WriteAtomic).
func listFiles(dir string) ([]string, error) {
	if _, err := os.ReadDir(dir); err != nil {
		return nil, err
	}
	return listFS(os.DirFS(dir))
}

// listFS is listFiles on a file system rooted at the bundle directory.
// Directory entries carry the Lstat type: symlinks are never listed.
func listFS(fsys fs.FS) ([]string, error) {
	var names []string
	err := fs.WalkDir(fsys, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		if p != Sums {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	return names, err
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// OpenDir opens a bundle directory as an *os.Root: every later access is
// relative to that one directory handle and cannot leave it through a
// symlink or "..", even if the path or a parent is swapped afterwards
// (BACKLOG #15). dir itself must be a real directory, not a symlink.
func OpenDir(dir string) (*os.Root, error) {
	lfi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !lfi.IsDir() {
		return nil, fmt.Errorf("bundle: %s is not a directory (symlinks are refused)", dir)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	if fi, err := r.Stat("."); err != nil || !os.SameFile(fi, lfi) {
		r.Close()
		return nil, fmt.Errorf("bundle: %s changed while opened", dir)
	}
	return r, nil
}

// WriteSums (re)writes SHA256SUMS over every regular file below dir
// (TESTSPEC §8: over all files). All file access goes through one
// directory handle (OpenDir), so a writable parent cannot redirect the
// reads or the write of SHA256SUMS through symlinks.
func WriteSums(dir string) error {
	r, err := OpenDir(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	return WriteSumsIn(r)
}

// WriteSumsIn is WriteSums on an open bundle directory.
func WriteSumsIn(r *os.Root) error {
	names, err := listFS(r.FS())
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, n := range names {
		s, err := rootFileSum(r, n)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", s, n)
	}
	return writeAtomicIn(r, Sums, []byte(b.String()), 0o640)
}

// rootFileSum hashes name below r; the opened file must be the regular
// file Lstat saw (no symlink swapped in after the listing).
func rootFileSum(r *os.Root, name string) (string, error) {
	lfi, err := r.Lstat(name)
	if err != nil {
		return "", err
	}
	if !lfi.Mode().IsRegular() {
		return "", fmt.Errorf("bundle: %s is not a regular file", name)
	}
	f, err := r.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !os.SameFile(fi, lfi) {
		return "", fmt.Errorf("bundle: %s changed while opened", name)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeAtomicIn is WriteAtomic relative to r: a new temporary file
// (O_EXCL), fsync, rename over name (rename replaces a symlink, it does not
// follow it), directory fsync.
func writeAtomicIn(r *os.Root, name string, data []byte, mode os.FileMode) error {
	var tmp string
	var f *os.File
	var err error
	for i := 0; i < 100; i++ {
		tmp = fmt.Sprintf(".%s.tmp%d-%d", name, os.Getpid(), time.Now().UnixNano()+int64(i))
		f, err = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			_ = r.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Rename(tmp, name); err != nil {
		return err
	}
	ok = true
	if d, err := r.Open("."); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// VerifySums checks SHA256SUMS; it returns one line per problem (missing
// list, changed file, file not listed).
func VerifySums(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, Sums))
	if err != nil {
		return []string{"SHA256SUMS missing: " + err.Error()}
	}
	listed := map[string]bool{}
	var probs []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		sum, name, ok := strings.Cut(l, "  ")
		if !ok {
			continue
		}
		listed[name] = true
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			probs = append(probs, name+": path outside the bundle")
			continue
		}
		got, err := fileSum(filepath.Join(dir, filepath.FromSlash(name)))
		switch {
		case err != nil:
			probs = append(probs, name+": "+err.Error())
		case got != sum:
			probs = append(probs, name+": checksum differs")
		}
	}
	names, _ := listFiles(dir)
	for _, n := range names {
		if !listed[n] {
			probs = append(probs, n+": not in SHA256SUMS")
		}
	}
	return probs
}

// Loaded is a bundle read back for the report.
type Loaded struct {
	Dir         string
	MetaRaw     []byte
	Meta        map[string]any
	Samples     []spec.Sample
	Events      []string
	VerdictRaw  []byte
	SumProblems []string
}

// Load reads a bundle. Missing files are not an error (a killed run has no
// meta.json or verdict.json); a malformed samples.csv is.
func Load(dir string) (*Loaded, error) {
	l := &Loaded{Dir: dir}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("bundle: %s is not a directory", dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, Meta)); err == nil {
		l.MetaRaw = b
		if err := json.Unmarshal(b, &l.Meta); err != nil {
			return nil, fmt.Errorf("bundle: meta.json: %w", err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, Verdict)); err == nil {
		l.VerdictRaw = b
	}
	if b, err := os.ReadFile(filepath.Join(dir, Events)); err == nil {
		for _, s := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if s != "" {
				l.Events = append(l.Events, s)
			}
		}
	}
	f, err := os.Open(filepath.Join(dir, Samples))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		defer f.Close()
		smp, err := ReadSamples(bufio.NewReader(f))
		if err != nil {
			return nil, fmt.Errorf("bundle: samples.csv: %w", err)
		}
		l.Samples = smp
	}
	l.SumProblems = VerifySums(dir)
	return l, nil
}

// ScanTimes calls fn with the t_iso of every row of samples.csv, one row
// at a time: the memory does not grow with the length of a log run.
func ScanTimes(r io.Reader, fn func(time.Time)) error {
	cr := csv.NewReader(bufio.NewReader(r))
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	header, err := cr.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	col := -1
	for i, h := range header {
		if strings.TrimSpace(h) == "t_iso" {
			col = i
		}
	}
	if col < 0 {
		return errors.New("samples.csv without a t_iso column")
	}
	for n := 2; ; n++ {
		row, err := cr.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if col >= len(row) || strings.TrimSpace(row[col]) == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(row[col]))
		if err != nil {
			return fmt.Errorf("line %d: t_iso %q: %w", n, row[col], err)
		}
		fn(t)
	}
}

// ReadSamples parses samples.csv (header first; columns by name).
func ReadSamples(r io.Reader) ([]spec.Sample, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []spec.Sample
	for n := 2; ; n++ {
		row, err := cr.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		s, err := spec.ParseRow(header, row)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, s)
	}
}
