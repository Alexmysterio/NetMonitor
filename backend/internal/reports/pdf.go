package reports

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

const (
	a4LandscapeW = 842.0
	a4LandscapeH = 595.0
	pdfMargin    = 36.0
)

type pdfDoc struct {
	pages    []*pdfPage
	title    string
	subtitle string
	created  time.Time
}

type pdfPage struct {
	content bytes.Buffer
	y       float64
}

func newPDF(title, subtitle string) *pdfDoc {
	d := &pdfDoc{title: title, subtitle: subtitle, created: time.Now().UTC()}
	d.addPage()
	return d
}

func (d *pdfDoc) addPage() {
	p := &pdfPage{}
	d.pages = append(d.pages, p)
	first := len(d.pages) == 1
	if first {
		d.textAt(p, pdfMargin, a4LandscapeH-46, 19, true, d.title)
		if d.subtitle != "" {
			d.textAt(p, pdfMargin, a4LandscapeH-63, 9, false, d.subtitle)
		}
		d.line(p, pdfMargin, a4LandscapeH-76, a4LandscapeW-pdfMargin, a4LandscapeH-76, 0.8)
		p.y = a4LandscapeH - 94
	} else {
		d.textAt(p, pdfMargin, a4LandscapeH-30, 9, false, d.title)
		d.line(p, pdfMargin, a4LandscapeH-38, a4LandscapeW-pdfMargin, a4LandscapeH-38, 0.85)
		p.y = a4LandscapeH - 56
	}
}

func (d *pdfDoc) ensure(space float64) *pdfPage {
	p := d.pages[len(d.pages)-1]
	if p.y-space < pdfMargin+22 {
		d.addPage()
		p = d.pages[len(d.pages)-1]
	}
	return p
}

func (d *pdfDoc) Heading(text string) {
	p := d.ensure(34)
	p.y -= 12
	d.textAt(p, pdfMargin, p.y, 12, true, text)
	d.line(p, pdfMargin, p.y-5, a4LandscapeW-pdfMargin, p.y-5, 0.9)
	p.y -= 16
}

func (d *pdfDoc) Paragraph(text string) {
	p := d.ensure(22)
	lines := wrapText(text, 86)
	for _, line := range lines {
		p = d.ensure(14)
		d.textAt(p, pdfMargin, p.y, 9, false, line)
		p.y -= 13
	}
	p.y -= 3
}

func (d *pdfDoc) KeyValues(rows [][2]string) {
	p := d.ensure(float64(len(rows)*14 + 8))
	for _, row := range rows {
		p = d.ensure(14)
		d.textAt(p, pdfMargin, p.y, 9, true, row[0])
		d.textAt(p, pdfMargin+150, p.y, 9, false, row[1])
		p.y -= 14
	}
	p.y -= 4
}

func (d *pdfDoc) Table(headers []string, widths []float64, rows [][]string) {
	total := 0.0
	for _, w := range widths {
		total += w
	}
	if total <= 0 {
		total = float64(len(headers))
	}
	contentWidth := a4LandscapeW - 2*pdfMargin
	colWidths := make([]float64, len(widths))
	for i, w := range widths {
		colWidths[i] = contentWidth * w / total
	}
	p := d.ensure(28)
	p.y = d.tableHeader(p, headers, colWidths)
	for i, row := range rows {
		p = d.ensure(17)
		if i%2 == 1 {
			d.rect(p, pdfMargin, p.y-4, contentWidth, 15, 0.965)
		}
		x := pdfMargin
		for c := range headers {
			value := ""
			if c < len(row) {
				value = row[c]
			}
			maxChars := int(colWidths[c] / 4.6)
			value = truncate(value, maxChars)
			d.textAt(p, x+3, p.y, 8.2, false, value)
			x += colWidths[c]
		}
		p.y -= 15
		d.line(p, pdfMargin, p.y+1, a4LandscapeW-pdfMargin, p.y+1, 0.93)
	}
	p.y -= 10
}

func (d *pdfDoc) tableHeader(p *pdfPage, headers []string, widths []float64) float64 {
	d.rect(p, pdfMargin, p.y-5, a4LandscapeW-2*pdfMargin, 17, 0.91)
	x := pdfMargin
	for i, h := range headers {
		d.textAt(p, x+3, p.y, 8.5, true, h)
		x += widths[i]
	}
	p.y -= 17
	return p.y
}

func (d *pdfDoc) Bars(title string, labels []string, values []float64, max float64, unit string) {
	d.Heading(title)
	if max <= 0 {
		max = 100
	}
	barWidth := 420.0
	for i := range labels {
		if i >= len(values) {
			break
		}
		p := d.ensure(20)
		label := labels[i]
		value := values[i]
		pct := value / max
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		d.textAt(p, pdfMargin, p.y, 8.2, false, truncate(label, 32))
		d.rect(p, pdfMargin+220, p.y-2, barWidth, 9, 0.9)
		color := [3]float64{0.15, 0.55, 0.42}
		if value < max*0.95 {
			color = [3]float64{0.85, 0.55, 0.15}
		}
		if value < max*0.9 {
			color = [3]float64{0.82, 0.22, 0.22}
		}
		d.rectColor(p, pdfMargin+220, p.y-2, barWidth*pct, 9, color)
		d.textAt(p, pdfMargin+220+barWidth+8, p.y, 8.2, false, fmt.Sprintf("%.2f%s", value, unit))
		p.y -= 18
	}
	p := d.pages[len(d.pages)-1]
	p.y -= 4
}

func (d *pdfDoc) Note(text string) {
	p := d.ensure(16)
	d.textAt(p, pdfMargin, p.y, 8, false, text)
	p.y -= 12
}

func (d *pdfDoc) Finalize() []byte {
	for i, p := range d.pages {
		footer := fmt.Sprintf("NetMonitor · %s · página %d de %d", d.created.Format("2006-01-02 15:04 UTC"), i+1, len(d.pages))
		d.textAt(p, pdfMargin, 22, 7.5, false, footer)
	}
	// Object layout: 1 catalog, 2 pages, 3/4 fonts, then page/content pairs.
	objects := make([]string, 0, 8+len(d.pages)*2)
	objects = append(objects,
		"<< /Type /Catalog /Pages 2 0 R >>",
		"",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>",
	)
	kids := make([]string, 0, len(d.pages))
	for i, p := range d.pages {
		pageObj := 5 + i*2
		contentObj := pageObj + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
				a4LandscapeW, a4LandscapeH, contentObj),
			streamObject(p.content.String()),
		)
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(d.pages))
	infoObj := len(objects) + 1
	objects = append(objects, fmt.Sprintf("<< /Title (%s) /Author (NetMonitor) /Creator (NetMonitor) /CreationDate (D:%s) >>",
		escapePDF(d.title), d.created.Format("20060102150405")))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects)+1)
	for i, obj := range objects {
		offsets[i+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, infoObj, xref)
	return out.Bytes()
}

func streamObject(content string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content)
}

func (d *pdfDoc) textAt(p *pdfPage, x, y, size float64, bold bool, text string) {
	if text == "" {
		return
	}
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(&p.content, "BT /%s %.1f Tf 0.10 0.13 0.18 rg 1 0 0 1 %.2f %.2f Tm (%s) Tj ET\n",
		font, size, x, y, escapePDF(text))
}

func (d *pdfDoc) line(p *pdfPage, x1, y1, x2, y2, gray float64) {
	fmt.Fprintf(&p.content, "%.3f G %.2f w %.2f %.2f m %.2f %.2f l S\n", gray, 0.6, x1, y1, x2, y2)
}

func (d *pdfDoc) rect(p *pdfPage, x, y, w, h, gray float64) {
	fmt.Fprintf(&p.content, "%.3f g %.2f %.2f %.2f %.2f re f\n", gray, x, y, w, h)
}

func (d *pdfDoc) rectColor(p *pdfPage, x, y, w, h float64, color [3]float64) {
	fmt.Fprintf(&p.content, "%.3f %.3f %.3f rg %.2f %.2f %.2f %.2f re f\n", color[0], color[1], color[2], x, y, w, h)
}

func escapePDF(text string) string {
	encoded := encodeWinAnsi(text)
	encoded = strings.ReplaceAll(encoded, `\`, `\\`)
	encoded = strings.ReplaceAll(encoded, "(", `\(`)
	encoded = strings.ReplaceAll(encoded, ")", `\)`)
	encoded = strings.ReplaceAll(encoded, "\r", " ")
	encoded = strings.ReplaceAll(encoded, "\n", " ")
	return encoded
}

func encodeWinAnsi(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r < 0x80:
			b.WriteByte(byte(r))
		case r >= 0xA0 && r <= 0xFF:
			b.WriteByte(byte(r))
		default:
			if mapped, ok := winAnsiMap[r]; ok {
				b.WriteString(mapped)
			} else {
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

var winAnsiMap = map[rune]string{
	'€': "\x80", '‚': "\x82", 'ƒ': "\x83", '„': "\x84", '…': "\x85",
	'†': "\x86", '‡': "\x87", 'ˆ': "\x88", '‰': "\x89", 'Š': "\x8A",
	'‹': "\x8B", 'Œ': "\x8C", 'Ž': "\x8E", '‘': "\x91", '’': "\x92",
	'“': "\x93", '”': "\x94", '•': "\x95", '–': "\x96", '—': "\x97",
	'˜': "\x98", '™': "\x99", 'š': "\x9A", '›': "\x9B", 'œ': "\x9C",
	'ž': "\x9E", 'Ÿ': "\x9F", '≥': ">=", '≤': "<=", '→': "->", '←': "<-",
	'Δ': "Delta", 'δ': "delta", '·': "\xB7", '°': "\xB0", '±': "\xB1", '×': "\xD7", '÷': "\xF7",
	'α': "a", 'β': "b", 'µ': "\xB5",
}

func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := ""
	for _, word := range words {
		if line == "" {
			line = word
		} else if len([]rune(line))+1+len([]rune(word)) <= width {
			line += " " + word
		} else {
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func truncate(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}
