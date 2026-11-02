package dsl

import "testing"

func TestArithmeticAndOID(t *testing.T) {
	ctx := &Context{
		OIDs: map[string]any{
			"1.3.6.1.4.1.14988.1.1.3.10.0": 485.0,
			"1.3.6.1.2.1.1.3.0":            1234567.0,
		},
		Columns: map[string][]float64{"1.3.6.1.2.1.25.3.3.1.2": {0.1, 0.2, 0.3}},
		Rates:   map[string]float64{"counter": 1500},
		Vars:    map[string]string{"Device.Name": "core-01"},
	}
	cases := []struct {
		expr string
		want string
	}{
		{`round(oid("1.3.6.1.4.1.14988.1.1.3.10.0") / 10, 1)`, "48.5"},
		{`if(oid("1.3.6.1.4.1.14988.1.1.3.10.0") > 400, "hot", "ok")`, "hot"},
		{`concatenate("CPU ", 12, "%")`, "CPU 12%"},
		{`avg(oid_column("1.3.6.1.2.1.25.3.3.1.2"))`, "0.2"},
		{`bitrate(rate("counter") * 8)`, "12.00 Kbps"},
		{`string_size("abc")`, "3"},
		{`if(string_size("") == 0, "empty", "full")`, "empty"},
	}
	for _, tc := range cases {
		if got := EvalToString(tc.expr, ctx); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestRenderLabel(t *testing.T) {
	ctx := &Context{
		OIDs:    map[string]any{"1.2.3": 72.0},
		Columns: map[string][]float64{},
		Rates:   map[string]float64{},
		Vars: map[string]string{
			"Device.Name":         "Switch-01",
			"Device.FirstAddress": "10.0.0.1",
			"Device.Custom.site":  "CPD",
		},
	}
	funcs := map[string]string{"cpu": `oid("1.2.3")`}
	got := RenderLabel("[Device.Name]\\n[Device.FirstAddress] · [Device.Custom.site]\\nCPU [cpu()]%", ctx, funcs)
	want := "Switch-01\n10.0.0.1 · CPD\nCPU 72%"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnknownFunctionsDoNotPanic(t *testing.T) {
	ctx := NewContext()
	if got := EvalToString(`oid("1.2.3.4")`, &ctx); got != "" {
		t.Fatalf("missing oid should render blank, got %q", got)
	}
	if got := EvalToString(`not_a_function(1)`, &ctx); got != "" {
		t.Fatalf("unknown function should render blank, got %q", got)
	}
}
