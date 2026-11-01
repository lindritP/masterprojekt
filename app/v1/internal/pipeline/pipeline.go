// Package pipeline implements the document validation pipeline run by
// validation-worker (SRS §4.5.1).
//
// PDFs are parsed and rendered by PDFium compiled to WebAssembly and executed
// by wazero, so no cgo is involved and the same instructions run on amd64 and
// arm64. The content digest is reduced from the rendered page with integer
// arithmetic only. Together these make steps 1–5 deterministic across targets
// and CPU architectures (FR-410, ADR 0010).
package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"strings"

	"github.com/klippa-app/go-pdfium"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
)

// Failure codes recorded in a ValidationResult (SRS §4.5.1).
const (
	FileTooLarge         = "FILE_TOO_LARGE"
	UnsupportedMediaType = "UNSUPPORTED_MEDIA_TYPE"
	MimeMismatch         = "MIME_MISMATCH"
	CorruptDocument      = "CORRUPT_DOCUMENT"
	EncryptedDocument    = "ENCRYPTED_DOCUMENT"
	PageCountOutOfRange  = "PAGE_COUNT_OUT_OF_RANGE"
	RenderFailed         = "RENDER_FAILED"
)

const (
	mimePDF = "application/pdf"

	// renderDPI fixes the bitmap that the content digest is reduced from.
	renderDPI = 72

	// digestSide is the edge length of the grayscale matrix behind contentDigest.
	digestSide = 64

	// thumbnailSide bounds the longer edge of the step 6 thumbnail.
	thumbnailSide = 256
)

// Limits are the configured bounds a document must stay within.
type Limits struct {
	MaxBytes int64 // CCSRA_MAX_OBJECT_BYTES
	MaxPages int   // CCSRA_MAX_PAGES
}

// Checks are the observations recorded alongside the outcome (SRS §5.6,
// document.validated.v1 `checks`).
type Checks struct {
	DetectedMime  string
	SizeBytes     int64
	SHA256        string
	PageCount     int
	ContentDigest string
}

// Result is the outcome of steps 1–5.
type Result struct {
	FailureCodes []string // in the order the checks failed; empty means PASS
	Checks       Checks
}

// Passed reports whether every check passed.
func (r Result) Passed() bool { return len(r.FailureCodes) == 0 }

// Validator runs the pipeline. It is safe for concurrent use by up to the
// concurrency given to New.
type Validator struct {
	limits Limits
	pool   pdfium.Pool
}

// New compiles the PDFium module and prepares a pool of up to concurrency
// instances (FR-408: one per in-flight message).
func New(limits Limits, concurrency int) (*Validator, error) {
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:  1,
		MaxIdle:  concurrency,
		MaxTotal: concurrency,
		// Documents are passed as bytes; the module needs no file system.
		FSConfig: wazero.NewFSConfig(),
	})
	if err != nil {
		return nil, fmt.Errorf("init pdfium: %w", err)
	}
	return &Validator{limits: limits, pool: pool}, nil
}

// Close releases the PDFium pool.
func (v *Validator) Close() error { return v.pool.Close() }

// Validate runs steps 1–5 on a document. Checks continue after a failure where
// that is still possible, so the citizen receives a complete list. The error
// is reserved for infrastructure failures, never for a bad document.
func (v *Validator) Validate(ctx context.Context, data []byte, declaredMime string) (Result, error) {
	var r Result
	fail := func(code string) { r.FailureCodes = append(r.FailureCodes, code) }

	// 1. Size.
	r.Checks.SizeBytes = int64(len(data))
	if r.Checks.SizeBytes > v.limits.MaxBytes {
		fail(FileTooLarge)
	}

	// 2. MIME sniff on the first 512 bytes.
	r.Checks.DetectedMime = sniff(data)
	if declaredMime != mimePDF {
		fail(UnsupportedMediaType)
	}
	if r.Checks.DetectedMime != declaredMime {
		fail(MimeMismatch)
	}

	// 3. SHA-256 of the full object (recorded, not a pass/fail check).
	sum := sha256.Sum256(data)
	r.Checks.SHA256 = hex.EncodeToString(sum[:])

	// 4–5 only make sense for content that actually is a PDF.
	if r.Checks.DetectedMime != mimePDF {
		return r, nil
	}
	err := v.withDocument(ctx, data, func(inst pdfium.Pdfium, doc *requests.Page) error {
		count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.ByIndex.Document})
		if err != nil {
			fail(CorruptDocument)
			return nil
		}
		r.Checks.PageCount = count.PageCount
		if count.PageCount < 1 || count.PageCount > v.limits.MaxPages {
			fail(PageCountOutOfRange)
		}
		if count.PageCount < 1 {
			return nil
		}

		// 5. Render page 1 and reduce it to the content digest.
		rendered, err := inst.RenderPageInDPI(&requests.RenderPageInDPI{Page: *doc, DPI: renderDPI})
		if err != nil {
			fail(RenderFailed)
			return nil
		}
		defer rendered.Cleanup()
		digest, err := contentDigest(rendered.Result.RenderedImage)
		if err != nil {
			fail(RenderFailed)
			return nil
		}
		r.Checks.ContentDigest = digest
		return nil
	})
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword):
		fail(EncryptedDocument)
	case errors.Is(err, errOpen):
		fail(CorruptDocument)
	case err != nil:
		return Result{}, err
	}
	return r, nil
}

// Thumbnail renders page 1 as a PNG whose longer edge is at most 256 px
// (step 6). Its bytes are not asserted to be identical across targets (FR-411).
func (v *Validator) Thumbnail(ctx context.Context, data []byte) ([]byte, error) {
	var out []byte
	err := v.withDocument(ctx, data, func(inst pdfium.Pdfium, page *requests.Page) error {
		rendered, err := inst.RenderPageInPixels(&requests.RenderPageInPixels{Page: *page, Width: thumbnailSide, Height: thumbnailSide})
		if err != nil {
			return err
		}
		defer rendered.Cleanup()
		var b bytes.Buffer
		if err := png.Encode(&b, rendered.Result.RenderedImage); err != nil {
			return err
		}
		out = b.Bytes()
		return nil
	})
	return out, err
}

// errOpen marks a document PDFium cannot open for a reason other than a password.
var errOpen = errors.New("pdf cannot be opened")

// withDocument opens data in a pooled PDFium instance and calls fn with a
// reference to its first page.
func (v *Validator) withDocument(ctx context.Context, data []byte, fn func(pdfium.Pdfium, *requests.Page) error) error {
	inst, err := v.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return fmt.Errorf("get pdfium instance: %w", err)
	}
	defer inst.Close()

	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
	if errors.Is(err, pdfiumerrors.ErrPassword) {
		return err
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errOpen, err)
	}
	defer inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	return fn(inst, &requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}})
}

// sniff returns the media type of data without parameters.
func sniff(data []byte) string {
	t, _, _ := strings.Cut(http.DetectContentType(data), ";")
	return t
}

// contentDigest reduces img to a 64×64 grayscale matrix by box averaging and
// returns the hex SHA-256 of its 4096 bytes. Integer arithmetic only, so the
// result cannot depend on the CPU's floating-point behaviour.
func contentDigest(img image.Image) (string, error) {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return "", fmt.Errorf("unexpected image type %T", img)
	}
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < digestSide || h < digestSide {
		return "", fmt.Errorf("rendered page too small: %dx%d", w, h)
	}

	matrix := make([]byte, 0, digestSide*digestSide)
	for cy := range digestSide {
		y0, y1 := cy*h/digestSide, (cy+1)*h/digestSide
		for cx := range digestSide {
			x0, x1 := cx*w/digestSide, (cx+1)*w/digestSide
			var sum, n int
			for y := y0; y < y1; y++ {
				row := rgba.Pix[y*rgba.Stride:]
				for x := x0; x < x1; x++ {
					p := row[x*4:]
					// ITU-R BT.601 luma in integer form.
					sum += (299*int(p[0]) + 587*int(p[1]) + 114*int(p[2]) + 500) / 1000
					n++
				}
			}
			matrix = append(matrix, byte((sum+n/2)/n))
		}
	}
	d := sha256.Sum256(matrix)
	return hex.EncodeToString(d[:]), nil
}
