// Markdown -> DOCX converter tailored to the constructs used in SRS.md
const fs = require('fs');
const {
  Document, Packer, Paragraph, TextRun, HeadingLevel, AlignmentType,
  Table, TableRow, TableCell, WidthType, ShadingType, BorderStyle,
  TableOfContents, PageBreak, Footer, PageNumber, LevelFormat,
  PositionalTab, PositionalTabAlignment, PositionalTabLeader,
} = require('docx');

const SRC = process.argv[2];
const OUT = process.argv[3];

// ---------- page geometry (A4) ----------
const PAGE_W = 11906, PAGE_H = 16838;
const MARGIN_X = 1021;                    // 1.8 cm
const CONTENT_W = PAGE_W - 2 * MARGIN_X;  // 9864 twips

// ---------- type sizes (half-points) ----------
const BODY = 20, TBL = 17, CODE = 16, CODE_TBL = 15;

const GREY = 'D9D9D9', HDR_FILL = 'E8EDF5', CODE_FILL = 'F6F8FA', QUOTE_FILL = 'F7F9FC';

// ---------- inline markdown ----------
function plain(text, b) {
  if (!text) return [];
  return [new TextRun({
    text, bold: b.bold, italics: b.italics, color: b.color,
    underline: b.underline, size: b.size || BODY, font: b.font || 'Calibri',
  })];
}

function parseInline(text, b = {}) {
  const out = [];
  const re = /\[([^\]]+)\]\(([^)]+)\)|\*\*([^*]+)\*\*|`([^`]+)`|(?<![*\w])\*([^*\n]+)\*(?!\*)/g;
  let last = 0, m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(...plain(text.slice(last, m.index), b));
    if (m[1] !== undefined) {
      out.push(...parseInline(m[1], { ...b, color: '1155CC' }));
    } else if (m[3] !== undefined) {
      out.push(...parseInline(m[3], { ...b, bold: true }));
    } else if (m[4] !== undefined) {
      out.push(new TextRun({
        text: m[4], font: 'Consolas', size: (b.size || BODY) - 2,
        bold: b.bold, italics: b.italics, color: b.color || '9C2B2B',
      }));
    } else if (m[5] !== undefined) {
      out.push(...parseInline(m[5], { ...b, italics: true }));
    }
    last = re.lastIndex;
  }
  if (last < text.length) out.push(...plain(text.slice(last), b));
  return out.length ? out : plain(' ', b);
}

function stripMd(s) {
  return String(s || '').replace(/\[([^\]]+)\]\([^)]+\)/g, '$1').replace(/[*`]/g, '');
}

// ---------- table helpers ----------
function splitRow(line) {
  let t = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  const cells = [];
  let cur = '';
  for (let i = 0; i < t.length; i++) {
    if (t[i] === '\\' && t[i + 1] === '|') { cur += '|'; i++; continue; }
    if (t[i] === '|') { cells.push(cur); cur = ''; continue; }
    cur += t[i];
  }
  cells.push(cur);
  return cells.map(c => c.trim());
}

const isSep = l => /^\|[\s:|-]+\|$/.test(l.trim()) && l.includes('-');

function colWidths(rows, total) {
  const n = Math.max(...rows.map(r => r.length));
  const w = new Array(n).fill(1);       // desired share, from total content length
  const longest = new Array(n).fill(1); // longest unbreakable token, in chars
  for (const r of rows) {
    for (let i = 0; i < n; i++) {
      const txt = stripMd(r[i]);
      // cap a single long cell so it cannot swallow the whole table
      w[i] = Math.max(w[i], Math.min(txt.length, 55));
      // words break at whitespace and at '/', which is where Word wraps paths
      for (const tok of txt.split(/[\s/]+/)) {
        if (tok) longest[i] = Math.max(longest[i], Math.min(tok.length, 20));
      }
    }
  }
  // 105 twips/char is generous for 8.5pt, so a substituted (wider) font still fits
  let min = longest.map(l => Math.max(560, l * 105 + 200));
  const minSum = min.reduce((a, b) => a + b, 0);
  if (minSum > total) min = min.map(x => Math.floor(x * total / minSum)); // too many columns: scale down

  const sum = w.reduce((a, b) => a + b, 0);
  let out = w.map((x, i) => Math.max(min[i], Math.round(total * x / sum)));
  // rescale the above-minimum slack so the row sums exactly to `total`
  for (let pass = 0; pass < 8; pass++) {
    const s = out.reduce((a, b) => a + b, 0);
    if (s === total) break;
    const flex = out.map((x, i) => x > min[i] ? x - min[i] : 0);
    const fs = flex.reduce((a, b) => a + b, 0);
    if (fs <= 0) break;
    out = out.map((x, i) => x > min[i] ? Math.max(min[i], x + Math.round((total - s) * flex[i] / fs)) : x);
  }
  out[out.length - 1] += total - out.reduce((a, b) => a + b, 0);
  return out;
}

function cellPara(text, opts) {
  return new Paragraph({
    children: parseInline(text, { size: opts.size, bold: opts.bold }),
    spacing: { before: 20, after: 20, line: 240 },
    alignment: AlignmentType.LEFT,
  });
}

function buildTable(rows, hasHeader) {
  const n = Math.max(...rows.map(r => r.length));
  const norm = rows.map(r => { const c = r.slice(); while (c.length < n) c.push(''); return c.slice(0, n); });
  const widths = colWidths(norm, CONTENT_W);
  const border = { style: BorderStyle.SINGLE, size: 2, color: GREY };
  const trs = norm.map((r, ri) => {
    const head = hasHeader && ri === 0;
    return new TableRow({
      tableHeader: head,
      cantSplit: false,
      children: r.map((c, ci) => new TableCell({
        width: { size: widths[ci], type: WidthType.DXA },
        shading: head ? { type: ShadingType.CLEAR, fill: HDR_FILL } : undefined,
        margins: { top: 40, bottom: 40, left: 90, right: 90 },
        children: [cellPara(c, { size: TBL, bold: head })],
      })),
    });
  });
  return new Table({
    rows: trs,
    columnWidths: widths,
    width: { size: CONTENT_W, type: WidthType.DXA },
    borders: { top: border, bottom: border, left: border, right: border,
               insideHorizontal: border, insideVertical: border },
  });
}

// ---------- block parsing ----------
const src = fs.readFileSync(SRC, 'utf8').split('\n');
const body = [];

let docTitle = null, docSubtitle = null, metaTable = null, frontDone = false;
let i = 0;

function flushCode(lines, lang) {
  const out = [];
  lines.forEach((l, idx) => {
    out.push(new Paragraph({
      children: [new TextRun({ text: l.length ? l : ' ', font: 'Consolas', size: CODE, color: '24292F' })],
      spacing: { before: idx === 0 ? 100 : 0, after: idx === lines.length - 1 ? 140 : 0, line: 230 },
      shading: { type: ShadingType.CLEAR, fill: CODE_FILL },
      indent: { left: 170, right: 100 },
    }));
  });
  return out;
}

function quotePara(text, first, last) {
  return new Paragraph({
    children: parseInline(text, { size: BODY - 1 }),
    indent: { left: 300, right: 160 },
    spacing: { before: first ? 140 : 60, after: last ? 160 : 60, line: 264 },
    border: { left: { style: BorderStyle.SINGLE, size: 14, color: '4472C4', space: 10 } },
    shading: { type: ShadingType.CLEAR, fill: QUOTE_FILL },
  });
}

while (i < src.length) {
  const raw = src[i];
  const line = raw.replace(/\s+$/, '');
  const t = line.trim();

  // fenced code
  if (t.startsWith('```')) {
    const lang = t.slice(3);
    const buf = [];
    i++;
    while (i < src.length && !src[i].trim().startsWith('```')) { buf.push(src[i].replace(/\s+$/, '')); i++; }
    i++;
    body.push(...flushCode(buf, lang));
    continue;
  }

  // table
  if (t.startsWith('|') && t.endsWith('|')) {
    const rows = [];
    let hasHeader = false;
    const start = i;
    while (i < src.length) {
      const l = src[i].trim();
      if (!(l.startsWith('|') && l.endsWith('|'))) break;
      if (isSep(l)) { hasHeader = rows.length === 1; i++; continue; }
      rows.push(splitRow(l));
      i++;
    }
    if (rows.length) {
      // an all-empty header row (the metadata table) is not a real header
      if (hasHeader && rows[0].every(c => c === '')) { rows.shift(); hasHeader = false; }
      const tbl = buildTable(rows, hasHeader);
      if (!frontDone && metaTable === null && docTitle) { metaTable = tbl; }
      else { body.push(tbl); body.push(new Paragraph({ text: '', spacing: { after: 100 } })); }
    }
    continue;
  }

  // headings
  let m;
  if ((m = t.match(/^(#{1,4})\s+(.*)$/))) {
    const lvl = m[1].length, txt = m[2];
    if (lvl === 1 && !docTitle && !frontDone) { docTitle = txt; i++; continue; }
    const H = { 1: HeadingLevel.HEADING_1, 2: HeadingLevel.HEADING_2, 3: HeadingLevel.HEADING_3, 4: HeadingLevel.HEADING_4 }[lvl];
    if (lvl === 1) body.push(new Paragraph({ children: [new PageBreak()] }));
    body.push(new Paragraph({ heading: H, children: parseInline(txt, { size: lvl === 1 ? 32 : lvl === 2 ? 25 : 22, bold: true }) }));
    i++;
    continue;
  }

  // horizontal rule: skip when it only separates sections
  if (/^---+$/.test(t)) { i++; continue; }

  // blockquote
  if (t.startsWith('>')) {
    const chunk = [];
    while (i < src.length && src[i].trim().startsWith('>')) {
      chunk.push(src[i].trim().replace(/^>\s?/, ''));
      i++;
    }
    const paras = [];
    let cur = [];
    for (const c of chunk) {
      if (c.trim() === '') { if (cur.length) { paras.push(cur.join(' ')); cur = []; } }
      else cur.push(c);
    }
    if (cur.length) paras.push(cur.join(' '));
    paras.forEach((p, k) => body.push(quotePara(p, k === 0, k === paras.length - 1)));
    continue;
  }

  // bullet list
  if (/^\s*[-*]\s+/.test(line)) {
    while (i < src.length && /^\s*[-*]\s+/.test(src[i])) {
      body.push(new Paragraph({
        children: parseInline(src[i].replace(/^\s*[-*]\s+/, ''), { size: BODY }),
        numbering: { reference: 'bullets', level: 0 },
        spacing: { before: 40, after: 40, line: 264 },
      }));
      i++;
    }
    continue;
  }

  // numbered list -> keep the literal number (only two in the document)
  if (/^\s*\d+\.\s+/.test(line)) {
    body.push(new Paragraph({
      children: parseInline(line.trim(), { size: BODY }),
      indent: { left: 340 }, spacing: { before: 40, after: 40, line: 264 },
    }));
    i++;
    continue;
  }

  // blank
  if (t === '') { i++; continue; }

  // subtitle: the first bold-only line under the title
  if (!frontDone && docTitle && !docSubtitle && /^\*\*[^*]+\*\*$/.test(t)) {
    docSubtitle = t.replace(/^\*\*|\*\*$/g, '');
    i++;
    continue;
  }

  // paragraph
  body.push(new Paragraph({
    children: parseInline(t, { size: BODY }),
    spacing: { before: 60, after: 120, line: 264 },
  }));
  i++;
  if (metaTable && !frontDone) frontDone = true;
}

// footer label, version and date are read from the metadata table
function footerText() {
  const meta = key => {
    const row = src.find(l => l.startsWith(`| **${key}** |`));
    return row ? row.split('|')[2].trim() : '?';
  };
  const label = /Software Requirements/.test(docTitle || '') ? 'SRS — CCSRA' : (docTitle || 'Document');
  return `${label} · v${meta('Version')} · ${meta('Date')}`;
}

// ---------- front matter ----------
const front = [
  new Paragraph({ text: '', spacing: { after: 1400 } }),
  new Paragraph({
    children: [new TextRun({ text: docTitle || 'Document', bold: true, size: 48, font: 'Calibri', color: '1F3864' })],
    spacing: { after: 160 }, alignment: AlignmentType.LEFT,
  }),
  new Paragraph({
    children: [new TextRun({ text: docSubtitle || '', size: 30, font: 'Calibri', color: '2E5496' })],
    spacing: { after: 100 },
    border: { bottom: { style: BorderStyle.SINGLE, size: 8, color: '2E5496', space: 10 } },
  }),
  new Paragraph({ text: '', spacing: { after: 420 } }),
];
if (metaTable) front.push(metaTable);

const toc = [
  new Paragraph({ children: [new PageBreak()] }),
  new Paragraph({
    children: [new TextRun({ text: 'Table of Contents', bold: true, size: 32, font: 'Calibri', color: '1F3864' })],
    spacing: { after: 240 },
  }),
  new TableOfContents('Table of Contents', { hyperlink: true, headingStyleRange: '1-3' }),
  new Paragraph({
    children: [new TextRun({
      text: 'If this table is empty, select it and press F9 (Windows) or Fn+F9 (Mac) to build it.',
      italics: true, size: 16, color: '808080',
    })],
    spacing: { before: 200 },
  }),
  new Paragraph({ children: [new PageBreak()] }),
];

// ---------- document ----------
const doc = new Document({
  creator: 'Lindrit Prekaj',
  title: docTitle || 'Document',
  description: docSubtitle || '',
  features: { updateFields: true },
  numbering: {
    config: [{
      reference: 'bullets',
      levels: [{
        level: 0, format: LevelFormat.BULLET, text: '•', alignment: AlignmentType.LEFT,
        style: { paragraph: { indent: { left: 400, hanging: 200 } } },
      }],
    }],
  },
  styles: {
    default: {
      document: {
        run: { font: 'Calibri', size: BODY, color: '1A1A1A' },
        paragraph: { spacing: { after: 120, line: 264 } },
      },
      heading1: {
        run: { font: 'Calibri', size: 32, bold: true, color: '1F3864' },
        paragraph: { spacing: { before: 200, after: 180 }, keepNext: true, outlineLevel: 0 },
      },
      heading2: {
        run: { font: 'Calibri', size: 25, bold: true, color: '2E5496' },
        paragraph: { spacing: { before: 300, after: 120 }, keepNext: true, outlineLevel: 1 },
      },
      heading3: {
        run: { font: 'Calibri', size: 22, bold: true, color: '404040' },
        paragraph: { spacing: { before: 240, after: 100 }, keepNext: true, outlineLevel: 2 },
      },
      heading4: {
        run: { font: 'Calibri', size: 20, bold: true, italics: true, color: '404040' },
        paragraph: { spacing: { before: 200, after: 80 }, keepNext: true, outlineLevel: 3 },
      },
    },
  },
  sections: [{
    properties: {
      page: {
        size: { width: PAGE_W, height: PAGE_H },
        margin: { top: 1134, bottom: 1134, left: MARGIN_X, right: MARGIN_X, footer: 567 },
      },
    },
    footers: {
      default: new Footer({
        children: [new Paragraph({
          children: [
            new TextRun({ text: footerText(), size: 16, color: '808080' }),
            new TextRun({ children: [new PositionalTab({
              alignment: PositionalTabAlignment.RIGHT,
              relativeTo: 'margin',
              leader: PositionalTabLeader.NONE,
            })] }),
            new TextRun({ children: [PageNumber.CURRENT], size: 16, color: '808080' }),
          ],
          border: { top: { style: BorderStyle.SINGLE, size: 4, color: 'D9D9D9', space: 6 } },
        })],
      }),
    },
    children: [...front, ...toc, ...body],
  }],
});

Packer.toBuffer(doc).then(buf => {
  fs.writeFileSync(OUT, buf);
  console.log(`wrote ${OUT} (${(buf.length / 1024).toFixed(0)} KB)`);
});
