package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/redis/go-redis/v9"

	"github.com/netmonitor/netmonitor/internal/auth"
)

const winboxTicketTTL = 90 * time.Second

// createWinboxTicket issues a short-lived, one-time ticket. The browser only
// receives the ticket, never the RouterOS password. The local bridge redeems
// the ticket and launches Winbox with the stored credential.
func (s *Server) createWinboxTicket(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
		Kind     string `json:"kind"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
		writeError(w, http.StatusBadRequest, "device_id es obligatorio")
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "winbox"
	}
	switch kind {
	case "winbox", "ssh", "web":
	default:
		writeError(w, http.StatusBadRequest, "tipo de acceso no válido")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if !s.canAccessDevice(r.Context(), user, req.DeviceID) {
		writeError(w, http.StatusForbidden, "sin acceso a este dispositivo")
		return
	}

	credential, err := s.resolveToolCredential(r.Context(), req.DeviceID, kind)
	if errors.Is(err, errNoToolCredential) {
		writeError(w, http.StatusBadRequest, "el dispositivo no tiene credencial para ese acceso")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(credential.Username) == "" {
		writeError(w, http.StatusBadRequest, "la credencial no tiene usuario")
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
	if err := s.store.RDB.Set(r.Context(), key, req.DeviceID+"|"+kind, winboxTicketTTL).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "no se pudo guardar el ticket")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"url":         "netmonitor-winbox://" + token,
		"expires_in":  int(winboxTicketTTL.Seconds()),
		"device_name": credential.DeviceName,
		"kind":        kind,
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
	stored, err := s.store.RDB.GetDel(r.Context(), key).Result()
	if err == redis.Nil {
		writeError(w, http.StatusNotFound, "enlace caducado o ya utilizado")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no se pudo validar el ticket")
		return
	}
	deviceID, kind := stored, "winbox"
	if idx := strings.LastIndex(stored, "|"); idx > 0 {
		deviceID = stored[:idx]
		kind = stored[idx+1:]
	}
	switch kind {
	case "winbox", "ssh", "web":
	default:
		writeError(w, http.StatusBadRequest, "tipo de acceso no válido")
		return
	}

	credential, err := s.resolveToolCredential(r.Context(), deviceID, kind)
	if err != nil || strings.TrimSpace(credential.Username) == "" || credential.Address == "" {
		writeError(w, http.StatusBadRequest, "la credencial ya no está disponible")
		return
	}

	_, _ = s.pool.Exec(r.Context(), `
		INSERT INTO audit_logs (username, ip, method, path, status, resource_type, resource_id, details)
		VALUES ('winbox-bridge',$1,'POST','/api/v1/tools/winbox/redeem',200,'winbox',$2,'{"action":"launch"}')
	`, clientIP(r), deviceID)

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"kind":        kind,
		"address":     credential.Address,
		"port":        credential.Port,
		"username":    credential.Username,
		"password":    credential.Password,
		"use_tls":     credential.UseTLS,
		"path":        credential.Path,
		"device_name": credential.DeviceName,
	}})
}

func (s *Server) downloadWinboxBridge(w http.ResponseWriter, r *http.Request) {
	// Keep the downloaded .cmd small: cmd.exe has a ~8191 character command
	// line limit, and embedding the whole installer made Windows reject it.
	// The small bootstrap downloads and runs the real installer.
	installURL := requestBaseURL(r) + "/api/v1/tools/winbox/install.ps1"
	bootstrap := strings.ReplaceAll(winboxBootstrapScript, "__INSTALL_URL__", strings.ReplaceAll(installURL, "'", "''"))
	encoded := encodePowerShell(bootstrap)
	cmd := "@echo off\r\n" +
		"title NetMonitor Access Bridge\r\n" +
		"powershell.exe -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + encoded + "\r\n" +
		"if errorlevel 1 pause\r\n"

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="netmonitor-winbox-bridge.cmd"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(cmd))
}

// downloadWinboxInstallerPS1 is public on purpose: it only contains the local
// bridge script and the public NetMonitor URL, never credentials. Keeping it
// as a separate download avoids the Windows command-line length limit.
func (s *Server) downloadWinboxInstallerPS1(w http.ResponseWriter, r *http.Request) {
	server := requestBaseURL(r)
	bridge := strings.ReplaceAll(winboxBridgeScript, "__SERVER__", strings.ReplaceAll(server, "'", "''"))
	bridgeB64 := base64.StdEncoding.EncodeToString([]byte(bridge))
	installer := strings.ReplaceAll(winboxBridgeInstaller, "__BRIDGE_B64__", bridgeB64)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(installer))
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
	// Windows PowerShell resolves localhost to ::1 while the container port
	// mapping commonly listens on IPv4 only. Use the IPv4 loopback explicitly.
	if strings.EqualFold(host, "localhost") || strings.HasPrefix(strings.ToLower(host), "localhost:") {
		host = "127.0.0.1" + host[len("localhost"):]
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

const winboxBootstrapScript = `$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$client = New-Object System.Net.WebClient
$client.Proxy = $null
$installer = Join-Path $env:TEMP 'netmonitor-winbox-install.ps1'
$client.DownloadFile('__INSTALL_URL__', $installer)
& $installer
`

const winboxBridgeScript = `param([string]$LaunchUri)
$ErrorActionPreference = 'Stop'
$server = '__SERVER__'
$server = $server.Replace('://localhost', '://127.0.0.1')
$logDir = Join-Path $env:LOCALAPPDATA 'NetMonitor'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$logPath = Join-Path $logDir 'bridge.log'

function Write-BridgeLog([string]$Message) {
  try { Add-Content -Path $logPath -Value ((Get-Date).ToString('s') + ' ' + $Message) -Encoding UTF8 } catch {}
}

function Show-BridgeMessage([string]$Message, [string]$Title) {
  Add-Type -AssemblyName System.Windows.Forms
  [System.Windows.Forms.MessageBox]::Show($Message, $Title, 'OK', 'Information') | Out-Null
}

function Show-BridgeError([string]$Message) {
  Write-BridgeLog ('ERROR ' + $Message)
  Add-Type -AssemblyName System.Windows.Forms
  [System.Windows.Forms.MessageBox]::Show($Message, 'NetMonitor', 'OK', 'Error') | Out-Null
}

function Quote-Arg([string]$Value) {
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

function Format-WinboxArgs([string]$Address, [string]$User, [string]$Password) {
  if ($Password -eq '') { return (Quote-Arg $Address) + ' ' + (Quote-Arg $User) }
  if ($Address -match '[\s"]' -or $User -match '[\s"]' -or $Password -match '[\s"]') {
    return (Quote-Arg $Address) + ' ' + (Quote-Arg $User) + ' ' + (Quote-Arg $Password)
  }
  return $Address + ' ' + $User + ' ' + $Password
}

function Redact-Args([string]$Arguments, [string]$Password) {
  if (-not $Password) { return $Arguments }
  return $Arguments.Replace($Password, '<password>')
}

function Find-Executable([string[]]$Candidates, [string]$CommandName) {
  foreach ($candidate in $Candidates) {
    if ($candidate -and (Test-Path $candidate)) { return $candidate }
  }
  if ($CommandName) {
    $command = Get-Command $CommandName -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
  }
  return $null
}

function Find-Winbox {
  $candidates = @()
  if ($env:ProgramFiles) {
    $candidates += (Join-Path $env:ProgramFiles 'Winbox\winbox.exe')
    $candidates += (Join-Path $env:ProgramFiles 'Winbox\winbox64.exe')
    $candidates += (Join-Path $env:ProgramFiles 'winbox64.exe')
    $candidates += (Join-Path $env:ProgramFiles 'MikroTik\Winbox\winbox.exe')
    $candidates += (Join-Path $env:ProgramFiles 'MikroTik\Winbox\winbox64.exe')
  }
  if (${env:ProgramFiles(x86)}) {
    $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Winbox\winbox.exe')
    $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Winbox\winbox64.exe')
  }
  if ($env:LOCALAPPDATA) {
    $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Winbox\winbox.exe')
    $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\Winbox\winbox64.exe')
  }
  foreach ($registryPath in @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\winbox.exe', 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\winbox.exe')) {
    $appPath = (Get-ItemProperty $registryPath -ErrorAction SilentlyContinue).'(default)'
    if ($appPath) { $candidates += $appPath }
  }
  return Find-Executable -Candidates $candidates -CommandName 'winbox.exe'
}

function Find-Putty {
  $candidates = @()
  if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles 'PuTTY\putty.exe') }
  if (${env:ProgramFiles(x86)}) { $candidates += (Join-Path ${env:ProgramFiles(x86)} 'PuTTY\putty.exe') }
  if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA 'Programs\PuTTY\putty.exe') }
  foreach ($registryPath in @('HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\putty.exe', 'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\putty.exe')) {
    $appPath = (Get-ItemProperty $registryPath -ErrorAction SilentlyContinue).'(default)'
    if ($appPath) { $candidates += $appPath }
  }
  return Find-Executable -Candidates $candidates -CommandName 'putty.exe'
}

function Find-Ssh {
  $candidates = @()
  if ($env:SystemRoot) { $candidates += (Join-Path $env:SystemRoot 'System32\OpenSSH\ssh.exe') }
  if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles 'OpenSSH\ssh.exe') }
  return Find-Executable -Candidates $candidates -CommandName 'ssh.exe'
}

function Start-Winbox($Response) {
  $winbox = Find-Winbox
  if (-not $winbox) { throw 'No se encontro Winbox.exe. Instala Winbox o anade su carpeta al PATH.' }
  $address = [string]$Response.address
  if ($Response.port -and [int]$Response.port -ne 8291) { $address = $address + ':' + [string]$Response.port }
  $password = [string]$Response.password
  $arguments = Format-WinboxArgs $address ([string]$Response.username) $password
  Write-BridgeLog ('winbox path=' + $winbox + ' args=' + (Redact-Args $arguments $password))
  $startInfo = New-Object System.Diagnostics.ProcessStartInfo
  $startInfo.FileName = $winbox
  $startInfo.Arguments = $arguments
  $startInfo.UseShellExecute = $false
  $startInfo.WorkingDirectory = Split-Path $winbox -Parent
  $process = [System.Diagnostics.Process]::Start($startInfo)
  if ($process) { Write-BridgeLog ('winbox started pid=' + $process.Id) }
}

function Start-Ssh($Response) {
  $port = 22
  if ($Response.port) { $port = [int]$Response.port }
  $user = [string]$Response.username
  $address = [string]$Response.address
  $password = [string]$Response.password
  $putty = Find-Putty
  if ($putty) {
    $arguments = '-ssh -P ' + [string]$port + ' -l ' + (Quote-Arg $user) + ' -pw ' + (Quote-Arg $password) + ' ' + (Quote-Arg $address)
    Write-BridgeLog ('ssh path=' + $putty + ' args=' + (Redact-Args $arguments $password))
    Start-Process -FilePath $putty -ArgumentList $arguments
    return
  }
  $ssh = Find-Ssh
  if ($ssh) {
    try { Set-Clipboard -Value $password } catch {}
    $arguments = '-p ' + [string]$port + ' ' + (Quote-Arg ($user + '@' + $address))
    Write-BridgeLog ('ssh path=' + $ssh + ' args=' + $arguments)
    Start-Process -FilePath $ssh -ArgumentList $arguments
    Show-BridgeMessage 'No se encontro PuTTY. Se abrio OpenSSH y la contrasena se copio al portapapeles. Pegala cuando se solicite.' 'NetMonitor - SSH'
    return
  }
  throw 'No se encontro PuTTY ni OpenSSH. Instala PuTTY o activa el cliente OpenSSH de Windows.'
}

function Start-Web($Response) {
  $tls = [bool]$Response.use_tls
  $port = if ($tls) { 443 } else { 80 }
  if ($Response.port) { $port = [int]$Response.port }
  $path = [string]$Response.path
  if (-not $path) { $path = '/' }
  if (-not $path.StartsWith('/')) { $path = '/' + $path }
  $scheme = if ($tls) { 'https' } else { 'http' }
  $url = $scheme + '://' + [string]$Response.address
  if (($tls -and $port -ne 443) -or ((-not $tls) -and $port -ne 80)) { $url = $url + ':' + [string]$port }
  $url = $url + $path
  try { Set-Clipboard -Value ([string]$Response.password) } catch {}
  Write-BridgeLog ('web url=' + $url)
  Start-Process $url
}

try {
  $ticket = ''
  if ($LaunchUri -match '^netmonitor-winbox://([A-Za-z0-9]+)/?$') {
    $ticket = $Matches[1]
  } elseif ($LaunchUri -match '^netmonitor-tools://([a-z]+)/([A-Za-z0-9]+)/?$') {
    $ticket = $Matches[2]
  } else {
    throw 'El enlace de acceso no es valido.'
  }
  $body = @{ ticket = $ticket } | ConvertTo-Json -Compress
  Add-Type -AssemblyName System.Net.Http
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  $handler = New-Object System.Net.Http.HttpClientHandler
  $handler.UseProxy = $false
  $client = New-Object System.Net.Http.HttpClient($handler)
  $client.Timeout = [TimeSpan]::FromSeconds(15)
  try {
    $content = New-Object System.Net.Http.StringContent($body, [Text.Encoding]::UTF8, 'application/json')
    $result = $client.PostAsync(($server + '/api/v1/tools/winbox/redeem'), $content).GetAwaiter().GetResult()
    if (-not $result.IsSuccessStatusCode) { throw ('NetMonitor respondio ' + [int]$result.StatusCode) }
    $json = $result.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    $payload = $json | ConvertFrom-Json
    $response = $payload.data
    if (-not $response) { throw 'NetMonitor devolvio una respuesta no valida.' }
  } finally {
    $client.Dispose()
  }
  Write-BridgeLog ('ticket kind=' + [string]$response.kind + ' address=' + [string]$response.address + ' user=' + [string]$response.username + ' passLen=' + ([string]$response.password).Length + ' port=' + [string]$response.port)
  switch ([string]$response.kind) {
    'ssh' { Start-Ssh $response }
    'web' { Start-Web $response }
    default { Start-Winbox $response }
  }
} catch {
  Show-BridgeError $_.Exception.Message
}
`

const winboxBridgeInstaller = `$ErrorActionPreference = 'Stop'
$installDir = Join-Path $env:LOCALAPPDATA 'NetMonitor'
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$bridgePath = Join-Path $installDir 'winbox-bridge.ps1'
$bridge = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('__BRIDGE_B64__'))
Set-Content -Path $bridgePath -Value $bridge -Encoding UTF8
$command = 'powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $bridgePath + '" "%1"'
foreach ($scheme in @('netmonitor-winbox', 'netmonitor-tools')) {
  $base = 'HKCU:\Software\Classes\' + $scheme
  New-Item -Path $base -Force | Out-Null
  Set-Item -Path $base -Value 'URL:NetMonitor Access Bridge'
  New-ItemProperty -Path $base -Name 'URL Protocol' -Value '' -PropertyType String -Force | Out-Null
  New-Item -Path ($base + '\shell\open\command') -Force | Out-Null
  Set-Item -Path ($base + '\shell\open\command') -Value $command
}
Add-Type -AssemblyName System.Windows.Forms
[System.Windows.Forms.MessageBox]::Show('Puente de accesos instalado. Vuelve a NetMonitor y pulsa Winbox o SSH.', 'NetMonitor') | Out-Null
`
