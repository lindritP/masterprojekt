// Command corpusgen writes the synthetic PDF test corpus (SRS §10.3, FR-048).
//
// The output is byte-for-byte deterministic: no timestamps, a fixed seed and
// integer-only arithmetic, so `make corpus` yields identical files on every
// platform. test/corpus/corpus.sha256 records the expected hashes.
//
// The catalog and page tree are written last in every file, so truncating a
// document (corrupt.pdf) removes its root and cannot be silently repaired.
package main

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4" //nolint:staticcheck // PDF Standard Security Handler revision 2 is defined in terms of RC4.
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	out := flag.String("out", "test/corpus", "output directory")
	maxBytes := flag.Int("max-bytes", 20971520, "object size limit straddled by valid-large.pdf and oversized.pdf (CCSRA_MAX_OBJECT_BYTES)")
	maxPages := flag.Int("max-pages", 50, "page limit exceeded by too-many-pages.pdf (CCSRA_MAX_PAGES)")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	valid1 := document("valid-1page", 1, 1700, false, nil)
	fixtures := map[string][]byte{
		"valid-1page.pdf":   valid1,
		"valid-10page.pdf":  document("valid-10page", 10, 40, true, nil),
		"valid-large.pdf":   sized("valid-large", *maxBytes-1024),
		"oversized.pdf":     sized("oversized", *maxBytes+1024),
		"mime-mismatch.pdf": pngImage(),
		"corrupt.pdf":       valid1[:len(valid1)*4/10],
		"encrypted.pdf":     document("encrypted", 1, 200, false, newSecurity("encrypted", "corpus-owner", "corpus-user")),
		// Opens without a password, but is still encrypted (owner password only).
		"encrypted-owner-only.pdf": document("encrypted-owner-only", 1, 200, false, newSecurity("encrypted-owner-only", "corpus-owner", "")),
		"too-many-pages.pdf":       document("too-many-pages", *maxPages+1, 20, false, nil),
	}
	for name, data := range fixtures {
		if err := os.WriteFile(filepath.Join(*out, name), data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("wrote %d fixtures to %s", len(fixtures), *out)
}

// document builds a valid PDF with the given number of A4 pages. Each page
// carries a title, a coloured band and `lines` pseudo-random strokes; with
// withImage, each page also draws a 400×500 grayscale noise image.
func document(name string, pages, lines int, withImage bool, sec *security) []byte {
	d := newDoc(name, sec)
	font := d.add([]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"))
	pagesNum := d.reserve(true)
	rnd := fnv(name)

	var kids []string
	for p := 1; p <= pages; p++ {
		var c strings.Builder
		fmt.Fprintf(&c, "q 0.20 0.30 0.60 rg 72 700 451 60 re f Q\n")
		fmt.Fprintf(&c, "BT /F1 24 Tf 1 1 1 rg 90 722 Td (CCSRA synthetic document) Tj ET\n")
		fmt.Fprintf(&c, "BT /F1 12 Tf 0 0 0 rg 72 670 Td (%s - page %d of %d) Tj ET\n", name, p, pages)
		resources := fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", font)
		if withImage {
			img := d.addStream("/Type /XObject /Subtype /Image /Width 400 /Height 500 /ColorSpace /DeviceGray /BitsPerComponent 8", noise(&rnd, 400*500))
			resources = fmt.Sprintf("<< /Font << /F1 %d 0 R >> /XObject << /Im1 %d 0 R >> >>", font, img)
			c.WriteString("q 400 0 0 500 97 120 cm /Im1 Do Q\n")
		}
		for range lines {
			x1, y1 := 72+rnd.intn(451), 120+rnd.intn(520)
			x2, y2 := 72+rnd.intn(451), 120+rnd.intn(520)
			fmt.Fprintf(&c, "0.%02d G %d %d m %d %d l S\n", rnd.intn(100), x1, y1, x2, y2)
		}
		content := d.addStream("", []byte(c.String()))
		page := d.add(fmt.Appendf(nil, "<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Resources %s /Contents %d 0 R >>", pagesNum, resources, content))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	d.set(pagesNum, fmt.Appendf(nil, "<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages))
	d.root = d.reserve(true)
	d.set(d.root, fmt.Appendf(nil, "<< /Type /Catalog /Pages %d 0 R >>", pagesNum))
	return d.bytes()
}

// sized builds a one-page PDF of exactly target bytes, padded by an
// unreferenced filler stream.
func sized(name string, target int) []byte {
	fill := 0
	for {
		d := newDoc(name, nil)
		font := d.add([]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))
		d.addStream("", make([]byte, fill))
		content := d.addStream("", fmt.Appendf(nil, "BT /F1 24 Tf 72 720 Td (%s) Tj ET", name))
		pagesNum := d.reserve(true)
		page := d.add(fmt.Appendf(nil, "<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", pagesNum, font, content))
		d.set(pagesNum, fmt.Appendf(nil, "<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page))
		d.root = d.reserve(true)
		d.set(d.root, fmt.Appendf(nil, "<< /Type /Catalog /Pages %d 0 R >>", pagesNum))
		b := d.bytes()
		if len(b) == target {
			return b
		}
		fill += target - len(b)
	}
}

// pngImage is a PNG, to be submitted under a .pdf name (MIME_MISMATCH).
func pngImage() []byte {
	rnd := fnv("mime-mismatch")
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	img.Pix = noise(&rnd, len(img.Pix))
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		log.Fatal(err)
	}
	return b.Bytes()
}

// ---------------------------------------------------------------- PDF writer

type object struct {
	body []byte
	late bool // written after all other objects
}

type doc struct {
	objects []object // object n is objects[n-1]
	root    int
	id      []byte
	sec     *security
}

func newDoc(name string, sec *security) *doc {
	sum := md5.Sum([]byte(name))
	return &doc{id: sum[:], sec: sec}
}

func (d *doc) add(body []byte) int {
	d.objects = append(d.objects, object{body: body})
	return len(d.objects)
}

func (d *doc) reserve(late bool) int {
	d.objects = append(d.objects, object{late: late})
	return len(d.objects)
}

func (d *doc) set(n int, body []byte) { d.objects[n-1].body = body }

func (d *doc) addStream(dict string, data []byte) int {
	n := len(d.objects) + 1
	if d.sec != nil {
		data = d.sec.encrypt(n, data)
	}
	return d.add(fmt.Appendf(nil, "<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data))
}

func (d *doc) bytes() []byte {
	var encryptRef string
	if d.sec != nil {
		n := d.add(d.sec.dict())
		encryptRef = fmt.Sprintf(" /Encrypt %d 0 R", n)
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(d.objects)+1)
	for _, late := range []bool{false, true} {
		for i, o := range d.objects {
			if o.late == late {
				offsets[i+1] = b.Len()
				fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o.body)
			}
		}
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(d.objects)+1)
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	id := hex.EncodeToString(d.id)
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root %d 0 R%s /ID [<%s> <%s>] >>\nstartxref\n%d\n%%%%EOF\n",
		len(d.objects)+1, d.root, encryptRef, id, id, xref)
	return b.Bytes()
}

// ---------------------------------------------------------------- encryption

// security implements the PDF Standard Security Handler, revision 2 (40-bit
// RC4), ISO 32000-1 §7.6.3, algorithms 2, 3 and 4.
type security struct {
	key  []byte
	o, u []byte
}

const permissions = -44

var passwordPad = []byte{
	0x28, 0xbf, 0x4e, 0x5e, 0x4e, 0x75, 0x8a, 0x41, 0x64, 0x00, 0x4e, 0x56, 0xff, 0xfa, 0x01, 0x08,
	0x2e, 0x2e, 0x00, 0xb6, 0xd0, 0x68, 0x3e, 0x80, 0x2f, 0x0c, 0xa9, 0xfe, 0x64, 0x53, 0x69, 0x7a,
}

func newSecurity(name, owner, user string) *security {
	id := md5.Sum([]byte(name))
	ownerKey := md5.Sum(pad(owner))
	s := &security{o: rc4Apply(ownerKey[:5], pad(user))}

	h := md5.New()
	h.Write(pad(user))
	h.Write(s.o)
	var p int32 = permissions
	h.Write(binary.LittleEndian.AppendUint32(nil, uint32(p)))
	h.Write(id[:])
	s.key = h.Sum(nil)[:5]
	s.u = rc4Apply(s.key, passwordPad)
	return s
}

func (s *security) dict() []byte {
	return fmt.Appendf(nil, "<< /Filter /Standard /V 1 /R 2 /O <%x> /U <%x> /P %d >>", s.o, s.u, permissions)
}

func (s *security) encrypt(objNum int, data []byte) []byte {
	k := md5.Sum(append(append([]byte{}, s.key...), byte(objNum), byte(objNum>>8), byte(objNum>>16), 0, 0))
	return rc4Apply(k[:10], data)
}

func pad(pw string) []byte { return append([]byte(pw), passwordPad...)[:32] }

func rc4Apply(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		log.Fatal(err)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// ---------------------------------------------------------------- randomness

// splitmix is SplitMix64: tiny, fully specified, identical on every platform.
type splitmix uint64

func (s *splitmix) next() uint64 {
	*s += 0x9e3779b97f4a7c15
	z := uint64(*s)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (s *splitmix) intn(n int) int { return int(s.next() % uint64(n)) }

func noise(s *splitmix, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = uint8(s.next())
	}
	return b
}

func fnv(s string) splitmix {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return splitmix(h)
}
