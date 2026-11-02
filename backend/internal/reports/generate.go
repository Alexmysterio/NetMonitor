package reports

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type GenerateOptions struct {
	Type              string
	Format            string
	From              time.Time
	To                time.Time
	MapID             string
	ClientID          string
	IncludeInterfaces bool
	GeneratedBy       string
	ScheduleID        string
	ExportExternal    bool
	Storage           *StorageConfig
}

type Run struct {
	ID          string     `json:"id"`
	ScheduleID  string     `json:"schedule_id"`
	ReportType  string     `json:"report_type"`
	Format      string     `json:"format"`
	Status      string     `json:"status"`
	PeriodFrom  time.Time  `json:"period_from"`
	PeriodTo    time.Time  `json:"period_to"`
	Filename    string     `json:"filename"`
	Path        string     `json:"path"`
	SizeBytes   int64      `json:"size_bytes"`
	ExternalURL string     `json:"external_url"`
	Error       string     `json:"error"`
	GeneratedBy string     `json:"generated_by"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type Generator struct {
	Service Service
	Dir     string
}

func (g Generator) Generate(ctx context.Context, opts GenerateOptions) (Run, error) {
	if opts.Type == "" {
		opts.Type = "sla"
	}
	if opts.Format == "" {
		opts.Format = "pdf"
	}
	if opts.Format != "pdf" && opts.Format != "csv" {
		return Run{}, fmt.Errorf("formato no soportado: %s", opts.Format)
	}
	switch opts.Type {
	case "sla", "interfaces", "executive", "clients":
	default:
		return Run{}, fmt.Errorf("tipo de informe no soportado: %s", opts.Type)
	}
	from, to, err := normalizeRange(opts.From, opts.To)
	if err != nil {
		return Run{}, err
	}
	run := Run{ScheduleID: opts.ScheduleID, ReportType: opts.Type, Format: opts.Format,
		Status: "running", PeriodFrom: from, PeriodTo: to, GeneratedBy: opts.GeneratedBy, CreatedAt: time.Now().UTC()}
	var scheduleArg any
	if opts.ScheduleID != "" {
		scheduleArg = opts.ScheduleID
	}
	err = g.Service.Pool.QueryRow(ctx, `
		INSERT INTO report_runs (schedule_id, report_type, format, status, period_from, period_to, generated_by)
		VALUES ($1,$2,$3,'running',$4,$5,$6) RETURNING id, created_at
	`, scheduleArg, opts.Type, opts.Format, from, to, opts.GeneratedBy).Scan(&run.ID, &run.CreatedAt)
	if err != nil {
		return run, err
	}
	fail := func(err error) (Run, error) {
		run.Status = "failed"
		run.Error = err.Error()
		_, _ = g.Service.Pool.Exec(context.Background(),
			`UPDATE report_runs SET status='failed', error=$2, finished_at=NOW() WHERE id=$1`, run.ID, run.Error)
		return run, err
	}
	data, err := g.build(ctx, opts, from, to)
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return fail(err)
	}
	filename := fmt.Sprintf("netmonitor-%s-%s-%s.%s", opts.Type, time.Now().UTC().Format("20060102-150405"), run.ID[:8], opts.Format)
	path := filepath.Join(g.Dir, filename)
	if opts.Format == "csv" {
		err = os.WriteFile(path, data, 0o644)
	} else {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		return fail(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fail(err)
	}
	run.Filename = filename
	run.Path = path
	run.SizeBytes = info.Size()
	run.Status = "completed"
	run.ExternalURL = ""
	if opts.ExportExternal && opts.Storage != nil && opts.Storage.Type != "" {
		url, err := opts.Storage.Upload(ctx, path, filename)
		if err != nil {
			return fail(fmt.Errorf("informe generado, pero falló el envío externo: %w", err))
		}
		run.ExternalURL = url
	}
	now := time.Now().UTC()
	run.FinishedAt = &now
	_, err = g.Service.Pool.Exec(ctx, `
		UPDATE report_runs SET status='completed', filename=$2, path=$3, size_bytes=$4,
		       external_url=$5, finished_at=NOW()
		WHERE id=$1
	`, run.ID, run.Filename, run.Path, run.SizeBytes, run.ExternalURL)
	if err != nil {
		return run, err
	}
	return run, nil
}

func (g Generator) build(ctx context.Context, opts GenerateOptions, from, to time.Time) ([]byte, error) {
	switch opts.Type {
	case "interfaces":
		rows, err := g.Service.Interfaces(ctx, from, to, opts.MapID)
		if err != nil {
			return nil, err
		}
		if opts.Format == "csv" {
			return interfacesCSV(from, to, rows), nil
		}
		return interfacesPDF(from, to, rows), nil
	case "clients":
		clients, err := g.Service.Clients(ctx, from, to, opts.ClientID)
		if err != nil {
			return nil, err
		}
		if opts.Format == "csv" {
			return clientsCSV(from, to, clients), nil
		}
		return clientsPDF(from, to, clients), nil
	case "executive":
		summary, err := g.Service.Summary(ctx, from, to, opts.MapID)
		if err != nil {
			return nil, err
		}
		devices, _ := g.Service.Devices(ctx, from, to, opts.MapID)
		monthly, _ := g.Service.Monthly(ctx, 6, opts.MapID)
		var interfaces []InterfaceRow
		if opts.IncludeInterfaces {
			interfaces, _ = g.Service.Interfaces(ctx, from, to, opts.MapID)
		}
		if opts.Format == "csv" {
			return executiveCSV(from, to, summary, monthly, devices), nil
		}
		return executivePDF(from, to, summary, monthly, summary.TopDown, interfaces), nil
	default:
		summary, err := g.Service.Summary(ctx, from, to, opts.MapID)
		if err != nil {
			return nil, err
		}
		devices, err := g.Service.Devices(ctx, from, to, opts.MapID)
		if err != nil {
			return nil, err
		}
		if opts.Format == "csv" {
			return slaCSV(from, to, summary, devices), nil
		}
		return slaPDF(from, to, summary, devices), nil
	}
}

func slaPDF(from, to time.Time, summary Summary, devices []DeviceRow) []byte {
	doc := newPDF("Informe SLA y disponibilidad", fmt.Sprintf("Periodo: %s → %s", from.Format("02/01/2006 15:04"), to.Format("02/01/2006 15:04")))
	doc.Heading("Resumen ejecutivo")
	doc.KeyValues([][2]string{
		{"Dispositivos monitorizados", strconv.Itoa(summary.Devices)},
		{"Disponibilidad global", fmt.Sprintf("%.3f %%", summary.AvailabilityPct)},
		{"Incidencias", strconv.Itoa(summary.Incidents)},
		{"Tiempo de caída", formatDuration(summary.DowntimeSec)},
		{"MTTR medio", formatDuration(summary.MTTRSec)},
		{"Incidencias activas", strconv.Itoa(summary.ActiveOutages)},
	})
	doc.Heading("Disponibilidad por dispositivo")
	rows := make([][]string, 0, len(devices))
	for _, d := range devices {
		root := "—"
		if d.RootCause != "" {
			root = d.RootCause
		}
		rows = append(rows, []string{d.DeviceName, d.PrimaryIP, d.Status, strconv.Itoa(d.Incidents),
			formatDuration(d.DowntimeSec), fmt.Sprintf("%.3f %%", d.AvailabilityPct), root})
	}
	doc.Table([]string{"Dispositivo", "Dirección", "Estado", "Incidencias", "Caída", "Disponibilidad", "Causa raíz"},
		[]float64{22, 13, 9, 9, 12, 13, 22}, rows)
	if len(devices) == 0 {
		doc.Note("No hay dispositivos en el ámbito seleccionado.")
	}
	return doc.Finalize()
}

func interfacesPDF(from, to time.Time, rows []InterfaceRow) []byte {
	doc := newPDF("Disponibilidad por interfaz / enlace", fmt.Sprintf("Periodo: %s → %s", from.Format("02/01/2006 15:04"), to.Format("02/01/2006 15:04")))
	doc.Heading("Resumen de interfaces")
	withData, down := 0, 0
	for _, row := range rows {
		if row.Samples > 0 {
			withData++
			if row.AvailabilityPct < 99 {
				down++
			}
		}
	}
	doc.KeyValues([][2]string{
		{"Enlaces / interfaces", strconv.Itoa(len(rows))},
		{"Con muestras de tráfico", strconv.Itoa(withData)},
		{"Disponibilidad < 99 %", strconv.Itoa(down)},
	})
	doc.Heading("Detalle")
	table := make([][]string, 0, len(rows))
	for _, row := range rows {
		availability := "sin datos"
		if row.AvailabilityPct >= 0 {
			availability = fmt.Sprintf("%.2f %%", row.AvailabilityPct)
		}
		table = append(table, []string{row.MapName, row.DeviceName, row.InterfaceName, row.LinkType,
			availability, formatBps(row.AvgRxBps), formatBps(row.AvgTxBps), strconv.FormatInt(row.MaxErrors, 10)})
	}
	doc.Table([]string{"Mapa", "Dispositivo maestro", "Interfaz", "Tipo", "Disponibilidad", "RX medio", "TX medio", "Errores"},
		[]float64{16, 18, 20, 9, 11, 10, 10, 6}, table)
	return doc.Finalize()
}

func clientsPDF(from, to time.Time, clients []ClientRow) []byte {
	doc := newPDF("SLA por cliente", fmt.Sprintf("Periodo: %s → %s", from.Format("02/01/2006 15:04"), to.Format("02/01/2006 15:04")))
	doc.Heading("Cumplimiento")
	table := make([][]string, 0, len(clients))
	for _, c := range clients {
		state := "No cumple"
		if c.Compliant {
			state = "Cumple"
		}
		table = append(table, []string{c.Name, strconv.Itoa(c.Devices), strconv.Itoa(c.Incidents),
			formatDuration(c.DowntimeSec), fmt.Sprintf("%.3f %%", c.AvailabilityPct),
			fmt.Sprintf("%.2f %%", c.TargetPct), state})
	}
	doc.Table([]string{"Cliente", "Equipos", "Incidencias", "Caída", "Disponibilidad", "Objetivo", "Estado"},
		[]float64{26, 8, 9, 12, 13, 10, 10}, table)
	if len(clients) == 0 {
		doc.Note("Crea clientes SLA en la pestaña Clientes para generar este informe.")
	}
	return doc.Finalize()
}

func executivePDF(from, to time.Time, summary Summary, monthly []MonthlyRow, devices []DeviceRow, interfaces []InterfaceRow) []byte {
	doc := newPDF("Informe ejecutivo de red", fmt.Sprintf("Periodo: %s → %s", from.Format("02/01/2006 15:04"), to.Format("02/01/2006 15:04")))
	doc.Heading("Indicadores del periodo")
	doc.KeyValues([][2]string{
		{"Disponibilidad global", fmt.Sprintf("%.3f %%", summary.AvailabilityPct)},
		{"Dispositivos", strconv.Itoa(summary.Devices)},
		{"Incidencias", strconv.Itoa(summary.Incidents)},
		{"Tiempo de caída", formatDuration(summary.DowntimeSec)},
		{"MTTR", formatDuration(summary.MTTRSec)},
		{"Incidencias activas", strconv.Itoa(summary.ActiveOutages)},
	})
	if len(monthly) > 0 {
		doc.Heading("Comparativa mensual")
		table := make([][]string, 0, len(monthly))
		for _, m := range monthly {
			table = append(table, []string{m.Month, strconv.Itoa(m.Incidents), formatDuration(m.DowntimeSec),
				fmt.Sprintf("%.3f %%", m.AvailabilityPct), fmt.Sprintf("%+.3f %%", m.DeltaPct),
				fmt.Sprintf("%+d", m.DeltaIncidents)})
		}
		doc.Table([]string{"Mes", "Incidencias", "Caída", "Disponibilidad", "Δ disponibilidad", "Δ incidencias"},
			[]float64{14, 10, 14, 16, 16, 14}, table)
		labels := make([]string, 0, len(monthly))
		values := make([]float64, 0, len(monthly))
		for _, m := range monthly {
			labels = append(labels, m.Month)
			values = append(values, m.AvailabilityPct)
		}
		doc.Bars("Disponibilidad mensual", labels, values, 100, " %")
	}
	doc.Heading("Top incidencias por dispositivo")
	table := make([][]string, 0, len(devices))
	for i, d := range devices {
		if i >= 15 {
			break
		}
		table = append(table, []string{d.DeviceName, strconv.Itoa(d.Incidents), formatDuration(d.DowntimeSec),
			fmt.Sprintf("%.3f %%", d.AvailabilityPct), d.RootCause})
	}
	doc.Table([]string{"Dispositivo", "Incidencias", "Caída", "Disponibilidad", "Causa raíz"},
		[]float64{32, 10, 14, 14, 22}, table)
	if len(interfaces) > 0 {
		doc.Heading("Interfaces con peor disponibilidad")
		rows := make([][]string, 0, len(interfaces))
		for i, row := range interfaces {
			if i >= 20 || row.AvailabilityPct < 0 {
				continue
			}
			rows = append(rows, []string{row.MapName, row.InterfaceName,
				fmt.Sprintf("%.2f %%", row.AvailabilityPct), formatBps(row.AvgRxBps), formatBps(row.AvgTxBps)})
		}
		doc.Table([]string{"Mapa", "Interfaz", "Disponibilidad", "RX medio", "TX medio"},
			[]float64{24, 28, 15, 14, 14}, rows)
	}
	return doc.Finalize()
}

func slaCSV(from, to time.Time, summary Summary, devices []DeviceRow) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"netmonitor_report", "sla"})
	_ = w.Write([]string{"from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339)})
	_ = w.Write([]string{"availability_pct", fmt.Sprintf("%.4f", summary.AvailabilityPct),
		"incidents", strconv.Itoa(summary.Incidents), "downtime_sec", fmt.Sprintf("%.0f", summary.DowntimeSec),
		"mttr_sec", fmt.Sprintf("%.0f", summary.MTTRSec)})
	_ = w.Write([]string{"device", "ip", "status", "incidents", "downtime_sec", "availability_pct", "root_cause"})
	for _, d := range devices {
		_ = w.Write([]string{d.DeviceName, d.PrimaryIP, d.Status, strconv.Itoa(d.Incidents),
			fmt.Sprintf("%.0f", d.DowntimeSec), fmt.Sprintf("%.4f", d.AvailabilityPct), d.RootCause})
	}
	w.Flush()
	return []byte(b.String())
}

func interfacesCSV(from, to time.Time, rows []InterfaceRow) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"netmonitor_report", "interfaces"})
	_ = w.Write([]string{"from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339)})
	_ = w.Write([]string{"map", "device", "interface", "link_type", "samples", "up_samples", "availability_pct", "avg_rx_bps", "avg_tx_bps", "max_errors"})
	for _, row := range rows {
		avail := ""
		if row.AvailabilityPct >= 0 {
			avail = fmt.Sprintf("%.4f", row.AvailabilityPct)
		}
		_ = w.Write([]string{row.MapName, row.DeviceName, row.InterfaceName, row.LinkType,
			strconv.FormatInt(row.Samples, 10), strconv.FormatInt(row.UpSamples, 10), avail,
			fmt.Sprintf("%.0f", row.AvgRxBps), fmt.Sprintf("%.0f", row.AvgTxBps), strconv.FormatInt(row.MaxErrors, 10)})
	}
	w.Flush()
	return []byte(b.String())
}

func clientsCSV(from, to time.Time, clients []ClientRow) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"netmonitor_report", "clients"})
	_ = w.Write([]string{"from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339)})
	_ = w.Write([]string{"client", "devices", "incidents", "downtime_sec", "availability_pct", "target_pct", "compliant"})
	for _, c := range clients {
		_ = w.Write([]string{c.Name, strconv.Itoa(c.Devices), strconv.Itoa(c.Incidents),
			fmt.Sprintf("%.0f", c.DowntimeSec), fmt.Sprintf("%.4f", c.AvailabilityPct),
			fmt.Sprintf("%.2f", c.TargetPct), strconv.FormatBool(c.Compliant)})
	}
	w.Flush()
	return []byte(b.String())
}

func executiveCSV(from, to time.Time, summary Summary, monthly []MonthlyRow, devices []DeviceRow) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"netmonitor_report", "executive"})
	_ = w.Write([]string{"from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339)})
	_ = w.Write([]string{"availability_pct", fmt.Sprintf("%.4f", summary.AvailabilityPct),
		"devices", strconv.Itoa(summary.Devices), "incidents", strconv.Itoa(summary.Incidents),
		"downtime_sec", fmt.Sprintf("%.0f", summary.DowntimeSec), "mttr_sec", fmt.Sprintf("%.0f", summary.MTTRSec)})
	_ = w.Write([]string{"month", "incidents", "downtime_sec", "availability_pct", "delta_pct"})
	for _, m := range monthly {
		_ = w.Write([]string{m.Month, strconv.Itoa(m.Incidents), fmt.Sprintf("%.0f", m.DowntimeSec),
			fmt.Sprintf("%.4f", m.AvailabilityPct), fmt.Sprintf("%+.4f", m.DeltaPct)})
	}
	_ = w.Write([]string{"device", "incidents", "downtime_sec", "availability_pct", "root_cause"})
	for _, d := range devices {
		_ = w.Write([]string{d.DeviceName, strconv.Itoa(d.Incidents), fmt.Sprintf("%.0f", d.DowntimeSec),
			fmt.Sprintf("%.4f", d.AvailabilityPct), d.RootCause})
	}
	w.Flush()
	return []byte(b.String())
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "0 min"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatBps(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.2f Gbps", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.2f Mbps", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.2f Kbps", v/1e3)
	default:
		return fmt.Sprintf("%.0f bps", v)
	}
}
