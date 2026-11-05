package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/redis/go-redis/v9"

	"github.com/netmonitor/netmonitor/internal/auth"
	"github.com/netmonitor/netmonitor/internal/secure"
)

const winboxTicketTTL = 90 * time.Second

// createWinboxTicket issues a short-lived, one-time ticket. The browser only
// receives the ticket, never the RouterOS password. The local bridge redeems
// the ticket and launches Winbox with the stored credential.
func (s *Server) createWinboxTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
		writeError(w, http.StatusBadRequest, "device_id es obligatorio")
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, req.DeviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}

	var name, address, credentialID string
	err := s.pool.QueryRow(r.Context(), `
		SELECT d.name, COALESCE(host(d.primary_ip),''), COALESCE(d.routeros_credential_id::text,'')
		FROM devices d WHERE d.id=$1
	`, req.DeviceID).Scan(&name, &address, &credentialID)
	if err != nil {
		writeError(w, http.StatusNotFound, "dispositivo no encontrado")
		return
	}
	if address == "" {
		writeError(w, http.StatusBadRequest, "el dispositivo no tiene una IP principal")
		return
	}
	if credentialID == "" {
		writeError(w, http.StatusBadRequest, "el dispositivo no tiene una credencial RouterOS asignada")
		return
	}
	var username string
	if err := s.pool.QueryRow(r.Context(), `SELECT username FROM routeros_credentials WHERE id=$1`, credentialID).Scan(&username); err != nil || strings.TrimSpace(username) == "" {
		writeError(w, http.StatusBadRequest, "la credencial RouterOS no tiene usuario")
		return
	}
	if s.store == nil || s.store.RDB == nil {
		writeError(w, http.StatusServiceUnavailable, "el almacén temporal no está disponible")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo generar el ticket")
		return
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	key := "winbox:ticket:" + hex.EncodeToString(sum[:])
	if err := s.store.RDB.Set(r.Context(), key, req.DeviceID, winboxTicketTTL).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "no se pudo guardar el ticket")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"url":         "netmonitor-winbox://" + token,
		"expires_in":  int(winboxTicketTTL.Seconds()),
		"device_name": name,
	}})
}

// redeemWinboxTicket is intentionally outside the authenticated API group: the
// local bridge is not a browser session. The unguessable, one-time ticket is
// the credential for this single request.
func (s *Server) redeemWinboxTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Ticket) == "" {
		writeError(w, http.StatusBadRequest, "ticket es obligatorio")
		return
	}
	ticket := strings.TrimSpace(req.Ticket)
	if len(ticket) != 64 {
		writeError(w, http.StatusNotFound, "enlace de Winbox caducado o ya utilizado")
		return
	}
	if s.store == nil || s.store.RDB == nil {
		writeError(w, http.StatusServiceUnavailable, "el almacén temporal no está disponible")
		return
	}
	sum := sha256.Sum256([]byte(ticket))
	key := "winbox:ticket:" + hex.EncodeToString(sum[:])
	deviceID, err := s.store.RDB.GetDel(r.Context(), key).Result()
	if err == redis.Nil {
		writeError(w, http.StatusNotFound, "enlace de Winbox caducado o ya utilizado")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no se pudo validar el ticket")
		return
	}

	target, err := s.loadRouterOSTarget(r.Context(), deviceID)
	if err != nil || strings.TrimSpace(target.Username) == "" || target.IP == "" {
		writeError(w, http.StatusBadRequest, "la credencial RouterOS ya no está disponible")
		return
	}
	password, err := secure.Decrypt(target.PasswordEnc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo descifrar la credencial RouterOS")
		return
	}

	_, _ = s.pool.Exec(r.Context(), `
		INSERT INTO audit_logs (username, ip, method, path, status, resource_type, resource_id, details)
		VALUES ('winbox-bridge',$1,'POST','/api/v1/tools/winbox/redeem',200,'winbox',$2,'{"action":"launch"}')
	`, clientIP(r), deviceID)

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"address":     target.IP,
		"username":    target.Username,
		"password":    password,
		"device_name": target.Name,
	}})
}

func (s *Server) downloadWinboxBridge(w http.ResponseWriter, r *http.Request) {
	server := requestBaseURL(r)
	bridge := strings.ReplaceAll(winboxBridgeScript, "__SERVER__", strings.ReplaceAll(server, "'", "''"))
	bridgeB64 := base64.StdEncoding.EncodeToString([]byte(bridge))
	installer := strings.ReplaceAll(winboxBridgeInstaller, "__BRIDGE_B64__", bridgeB64)
	encoded := encodePowerShell(installer)
	cmd := "@echo off\r\n" +
		"title NetMonitor Winbox Bridge\r\n" +
		"powershell.exe -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + encoded + "\r\n" +
		"if errorlevel 1 pause\r\n"

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="netmonitor-winbox-bridge.cmd"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(cmd))
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func clientIP(r *http.Request) string {
	if value := r.Header.Get("X-Forwarded-For"); value != "" {
		return strings.TrimSpace(strings.Split(value, ",")[0])
	}
	return r.RemoteAddr
}

func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	out := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(out[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(out)
}

const winboxBridgeScript = `param([string]$LaunchUri)
$ErrorActionPreference = 'Stop'
$server = '__SERVER__'

function Show-WinboxError([string]$Message) {
  Add-Type -AssemblyName System.Windows.Forms
  [System.Windows.Forms.MessageBox]::Show($Message, 'NetMonitor - Winbox', 'OK', 'Error') | Out-Null
}

function Quote-WinboxArg([string]$Value) {
  if ($null -eq $Value) { $Value = '' }
  $builder = New-Object System.Text.StringBuilder
  [void]$builder.Append('"')
  $backslashes = 0
  foreach ($ch in $Value.ToCharArray()) {
    if ($ch -eq [char]'\') {
      $backslashes++
    } elseif ($ch -eq [char]'"') {
      [void]$builder.Append(('\' * ($backslashes * 2 + 1)))
      [void]$builder.Append('"')
      $backslashes = 0
    } else {
      if ($backslashes -gt 0) { [void]$builder.Append(('\' * $backslashes)) }
      [void]$builder.Append($ch)
      $backslashes = 0
    }
  }
  if ($backslashes -gt 0) { [void]$builder.Append(('\' * ($backslashes * 2))) }
  [void]$builder.Append('"')
  return $builder.ToString()
}

try {
  if ($LaunchUri -notmatch '^netmonitor-winbox://([A-Za-z0-9]+)/?$') { throw 'El enlace de Winbox no es valido.' }
  $ticket = $Matches[1]
  $body = @{ ticket = $ticket } | ConvertTo-Json -Compress
  $response = Invoke-RestMethod -Method Post -Uri ($server + '/api/v1/tools/winbox/redeem') -ContentType 'application/json' -Body $body -TimeoutSec 15

  $candidates = @()
  if ($env:ProgramFiles) {
    $candidates += (Join-Path $env:ProgramFiles 'Winbox\winbox.exe')
    $candidates += (Join-Path $env:ProgramFiles 'winbox64.exe')
    $candidates += (Join-Path $env:ProgramFiles 'MikroTik\Winbox\winbox.exe')
  }
  if (${env:ProgramFiles(x86)}) {
    $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Winbox\winbox.exe')
  }
  if ($env:LOCALAPPDATA) {
    $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Winbox\winbox.exe')
  }
  $appPath = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\winbox.exe' -ErrorAction SilentlyContinue).'(default)'
  if ($appPath) { $candidates += $appPath }
  $appPath = (Get-ItemProperty 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\winbox.exe' -ErrorAction SilentlyContinue).'(default)'
  if ($appPath) { $candidates += $appPath }
  $found = Get-Command 'winbox.exe' -ErrorAction SilentlyContinue
  if ($found) { $candidates += $found.Source }

  $winbox = $candidates | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
  if (-not $winbox) { throw 'No se encontro Winbox.exe. Instala Winbox o anade su carpeta al PATH.' }

  $winboxArgs = (Quote-WinboxArg $response.address) + ' ' + (Quote-WinboxArg $response.username) + ' ' + (Quote-WinboxArg $response.password)
  Start-Process -FilePath $winbox -ArgumentList $winboxArgs
} catch {
  Show-WinboxError $_.Exception.Message
}
`

const winboxBridgeInstaller = `$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'NetMonitor'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$bridgePath = Join-Path $installDir 'winbox-bridge.ps1'
$bridge = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('__BRIDGE_B64__'))
Set-Content -Path $bridgePath -Value $bridge -Encoding UTF8
$command = 'powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $bridgePath + '" "%1"'
$base = 'HKCU:\Software\Classes\netmonitor-winbox'
New-Item -Path $base -Force | Out-Null
Set-Item -Path $base -Value 'URL:NetMonitor Winbox Bridge'
New-ItemProperty -Path $base -Name 'URL Protocol' -Value '' -PropertyType String -Force | Out-Null
New-Item -Path ($base + '\shell\open\command') -Force | Out-Null
Set-Item -Path ($base + '\shell\open\command') -Value $command
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.MessageBox]::Show('Puente Winbox instalado. Vuelve a NetMonitor y pulsa Abrir Winbox.', 'NetMonitor') | Out-Null
`
