package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the contentDigest values in test/corpus/expected.json")

// corpusDir holds the generated fixtures (make corpus) and expected.json.
const corpusDir = "../../../../test/corpus"

type expectations struct {
	Limits struct {
		MaxBytes int64 `json:"maxBytes"`
		MaxPages int   `json:"maxPages"`
	} `json:"limits"`
	Fixtures []fixture `json:"fixtures"`
}

type fixture struct {
	File          string   `json:"file"`
	FailureCodes  []string `json:"failureCodes"`
	PageCount     int      `json:"pageCount"`
	ContentDigest string   `json:"contentDigest"`
}

// TestCorpus is the determinism check of FR-410 and FR-043: on every
// architecture the pipeline must reproduce the committed outcomes and content
// digests exactly. Run with -update only when the corpus or the pipeline
// changes on purpose, and record why.
func TestCorpus(t *testing.T) {
	path := filepath.Join(corpusDir, "expected.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exp expectations
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatal(err)
	}

	v := newValidator(t, Limits{MaxBytes: exp.Limits.MaxBytes, MaxPages: exp.Limits.MaxPages})
	for i, f := range exp.Fixtures {
		t.Run(f.File, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(corpusDir, f.File))
			if errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s is missing: run `make corpus` first", f.File)
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := v.Validate(context.Background(), data, "application/pdf")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.FailureCodes, f.FailureCodes) {
				t.Errorf("failure codes = %v, want %v", r.FailureCodes, f.FailureCodes)
			}
			if r.Checks.PageCount != f.PageCount {
				t.Errorf("page count = %d, want %d", r.Checks.PageCount, f.PageCount)
			}
			if *update {
				exp.Fixtures[i].ContentDigest = r.Checks.ContentDigest
				return
			}
			if r.Checks.ContentDigest != f.ContentDigest {
				t.Errorf("content digest = %q, want %q", r.Checks.ContentDigest, f.ContentDigest)
			}
		})
	}

	if *update {
		out, err := json.MarshalIndent(exp, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidate_UnsupportedDeclaredType(t *testing.T) {
	v := newValidator(t, Limits{MaxBytes: 1 << 20, MaxPages: 5})
	data := readFixture(t, "valid-1page.pdf")

	r, err := v.Validate(context.Background(), data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{UnsupportedMediaType, MimeMismatch}
	if !slices.Equal(r.FailureCodes, want) {
		t.Errorf("failure codes = %v, want %v", r.FailureCodes, want)
	}
}

func TestThumbnail_IsBoundedPNG(t *testing.T) {
	v := newValidator(t, Limits{MaxBytes: 1 << 20, MaxPages: 5})
	out, err := v.Thumbnail(context.Background(), readFixture(t, "valid-1page.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if max(cfg.Width, cfg.Height) > thumbnailSide {
		t.Errorf("thumbnail is %dx%d, want longer edge <= %d", cfg.Width, cfg.Height, thumbnailSide)
	}
}

func TestContentDigest_BoxAverage(t *testing.T) {
	// A 128×128 image whose left half is black and right half white must
	// reduce to 32 zero bytes followed by 32 bytes of 255, in every row.
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := range 128 {
		for x := 64; x < 128; x++ {
			copy(img.Pix[y*img.Stride+x*4:], []byte{255, 255, 255, 255})
		}
	}
	got, err := contentDigest(img)
	if err != nil {
		t.Fatal(err)
	}
	row := append(make([]byte, 32), bytes.Repeat([]byte{255}, 32)...)
	sum := sha256.Sum256(bytes.Repeat(row, 64))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("digest = %s, want %s", got, want)
	}
}

func TestContentDigest_RejectsTinyImages(t *testing.T) {
	if _, err := contentDigest(image.NewRGBA(image.Rect(0, 0, 10, 10))); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Errorf("err = %v, want a 'too small' error", err)
	}
}

func newValidator(t *testing.T, l Limits) *Validator {
	t.Helper()
	v, err := New(l, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(corpusDir, name))
	if err != nil {
		t.Fatalf("%v (run `make corpus` first)", err)
	}
	return data
}
