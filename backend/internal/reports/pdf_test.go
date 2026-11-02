package reports

import (
	"bytes"
	"strings"
	"testing"
)

func TestPDFGeneration(t *testing.T) {
	doc := newPDF("Informe de prueba", "Disponibilidad y SLA")
	doc.Heading("Resumen")
	doc.KeyValues([][2]string{{"Disponibilidad", "99.995 %"}, {"Incidencias", "2"}})
	doc.Table([]string{"Dispositivo", "Disponibilidad"}, []float64{3, 1}, [][]string{
		{"Router sede", "99.999 %"},
		{"CPE remota", "98.500 %"},
	})
	doc.Bars("Mensual", []string{"2026-10", "2026-11"}, []float64{99.9, 99.5}, 100, " %")
	out := doc.Finalize()
	if !bytes.HasPrefix(out, []byte("%PDF-1.4")) {
		t.Fatalf("missing PDF header")
	}
	if !bytes.Contains(out, []byte("startxref")) || !bytes.Contains(out, []byte("%%EOF")) {
		t.Fatalf("PDF is not complete")
	}
	text := string(out)
	if !strings.Contains(text, "Informe de prueba") {
		t.Fatalf("title missing from PDF")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[float64]string{0: "0 min", 90: "1m", 3660: "1h 1m", 90000: "1d 1h 0m"}
	for input, want := range cases {
		if got := formatDuration(input); got != want {
			t.Fatalf("formatDuration(%v) = %q, want %q", input, got, want)
		}
	}
}
