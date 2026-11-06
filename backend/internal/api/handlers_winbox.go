package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

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
	server := strings.ReplaceAll(requestBaseURL(r), `"`, `""`)
	vbs := strings.ReplaceAll(winboxInstallerVBS, "__SERVER__", server)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="netmonitor-bridge-installer.vbs"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(vbs))
}

// downloadWinboxBridgePS1 is public on purpose: it only contains the local
// helper script and the public NetMonitor URL, never credentials.
func (s *Server) downloadWinboxBridgePS1(w http.ResponseWriter, r *http.Request) {
	server := strings.ReplaceAll(requestBaseURL(r), "'", "''")
	script := strings.ReplaceAll(winboxBridgeScript, "__SERVER__", server)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}

func (s *Server) downloadWinboxLauncherVBS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(winboxLauncherVBS))
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

const winboxLauncherVBS = `Set shell = CreateObject("WScript.Shell")
q = Chr(34)
script = shell.ExpandEnvironmentStrings("%LOCALAPPDATA%") & "\NetMonitor\netmonitor-helper.ps1"
If WScript.Arguments.Count > 0 Then
  cmd = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File " & q & script & q & " " & q & WScript.Arguments(0) & q
Else
  cmd = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File " & q & script & q & " -Serve"
End If
shell.Run cmd, 0, False
`

const winboxInstallerVBS = `Option Explicit
Dim shell, fso, http, stream, dir, scriptPath, launcherPath, server, q, attempt, ok
Dim wmi, procs, proc, health
Set shell = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
server = "__SERVER__"
dir = shell.ExpandEnvironmentStrings("%LOCALAPPDATA%") & "\NetMonitor"
If Not fso.FolderExists(dir) Then fso.CreateFolder(dir)
scriptPath = dir & "\netmonitor-helper.ps1"
launcherPath = dir & "\netmonitor-helper.vbs"
q = Chr(34)
On Error Resume Next
Set wmi = GetObject("winmgmts:\\.\root\cimv2")
Set procs = wmi.ExecQuery("SELECT Name, ProcessId, CommandLine FROM Win32_Process WHERE Name='powershell.exe' OR Name='netmonitor-tools.exe'")
For Each proc In procs
  If InStr(1, LCase("" & proc.CommandLine), "\netmonitor\netmonitor-helper.ps1", 1) > 0 Then proc.Terminate
  If LCase("" & proc.Name) = "netmonitor-tools.exe" Then proc.Terminate
Next
If fso.FileExists(dir & "\netmonitor-tools.exe") Then fso.DeleteFile dir & "\netmonitor-tools.exe", True
If fso.FileExists(dir & "\netmonitor-winbridge.exe") Then fso.DeleteFile dir & "\netmonitor-winbridge.exe", True
If fso.FileExists(dir & "\winbox-bridge.ps1") Then fso.DeleteFile dir & "\winbox-bridge.ps1", True
If fso.FileExists(dir & "\winbox-bridge.vbs") Then fso.DeleteFile dir & "\winbox-bridge.vbs", True
If fso.FileExists(dir & "\server.txt") Then fso.DeleteFile dir & "\server.txt", True
Err.Clear

Set http = CreateObject("WinHttp.WinHttpRequest.5.1")
If Err.Number <> 0 Then
  MsgBox "No se pudo iniciar la descarga del puente: " & Err.Description, 16, "NetMonitor"
  WScript.Quit 1
End If
http.SetProxy 1
http.Open "GET", server & "/api/v1/tools/winbox/bridge.ps1", False
http.Send
If Err.Number <> 0 Then
  MsgBox "No se pudo descargar el puente: " & Err.Description, 16, "NetMonitor"
  WScript.Quit 1
End If
If http.Status <> 200 Then
  MsgBox "No se pudo descargar el puente (HTTP " & http.Status & ").", 16, "NetMonitor"
  WScript.Quit 1
End If
Set stream = CreateObject("ADODB.Stream")
stream.Type = 1
stream.Open
stream.Write http.ResponseBody
stream.SaveToFile scriptPath, 2
stream.Close
If Err.Number <> 0 Then
  MsgBox "No se pudo guardar el puente: " & Err.Description, 16, "NetMonitor"
  WScript.Quit 1
End If
Err.Clear

Set http = CreateObject("WinHttp.WinHttpRequest.5.1")
http.SetProxy 1
http.Open "GET", server & "/api/v1/tools/winbox/launcher.vbs", False
http.Send
If Err.Number <> 0 Then
  MsgBox "No se pudo descargar el lanzador: " & Err.Description, 16, "NetMonitor"
  WScript.Quit 1
End If
If http.Status <> 200 Then
  MsgBox "No se pudo descargar el lanzador (HTTP " & http.Status & ").", 16, "NetMonitor"
  WScript.Quit 1
End If
Set stream = CreateObject("ADODB.Stream")
stream.Type = 1
stream.Open
stream.Write http.ResponseBody
stream.SaveToFile launcherPath, 2
stream.Close
If Err.Number <> 0 Then
  MsgBox "No se pudo guardar el lanzador: " & Err.Description, 16, "NetMonitor"
  WScript.Quit 1
End If
Err.Clear

shell.RegWrite "HKCU\Software\Microsoft\Windows\CurrentVersion\Run\NetMonitorAccessBridge", q & "wscript.exe" & q & " //B //NoLogo " & q & launcherPath & q, "REG_SZ"
Register "netmonitor-winbox"
Register "netmonitor-tools"
shell.Run q & "wscript.exe" & q & " //B //NoLogo " & q & launcherPath & q, 0, False
Err.Clear

ok = False
For attempt = 1 To 12
  WScript.Sleep 500
  Set health = CreateObject("WinHttp.WinHttpRequest.5.1")
  health.SetProxy 1
  health.Open "GET", "http://127.0.0.1:47821/health", False
  health.Send
  If Err.Number = 0 Then
    If health.Status = 200 Then
      ok = True
      Exit For
    End If
  End If
  Err.Clear
Next
On Error GoTo 0
If ok Then
  MsgBox "Puente de accesos instalado. Vuelve a NetMonitor y pulsa Winbox o SSH.", 64, "NetMonitor"
Else
  MsgBox "El puente se instaló, pero no se pudo iniciar. Cierra sesión y vuelve a entrar, o ejecuta de nuevo este instalador.", 48, "NetMonitor"
End If
Sub Register(scheme)
  Dim base
  base = "HKCU\Software\Classes\" & scheme
  shell.RegWrite base & "\", "URL:NetMonitor Access Bridge", "REG_SZ"
  shell.RegWrite base & "\URL Protocol", "", "REG_SZ"
  shell.RegWrite base & "\shell\open\command\", q & "wscript.exe" & q & " //B //NoLogo " & q & launcherPath & q & " " & q & "%1" & q, "REG_SZ"
End Sub
`

const winboxBridgeScript = `param([string]$LaunchUri = '', [switch]$Serve)
$ErrorActionPreference = 'Stop'
$server = '__SERVER__'
$server = $server.Replace('://localhost', '://127.0.0.1')
$logDir = Join-Path $env:LOCALAPPDATA 'NetMonitor'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$logPath = Join-Path $logDir 'bridge.log'

function Write-BridgeLog([string]$Message) {
  try { Add-Content -Path $logPath -Value ((Get-Date).ToString('s') + ' ' + $Message) -Encoding UTF8 } catch {}
}

function Show-BridgeError([string]$Message) {
  Write-BridgeLog ('ERROR ' + $Message)
  Add-Type -AssemblyName System.Windows.Forms
  [System.Windows.Forms.MessageBox]::Show($Message, 'NetMonitor', 'OK', 'Error') | Out-Null
}

function Focus-Process($Process) {
  if (-not $Process) { return }
  try {
    Start-Sleep -Milliseconds 700
    $shell = New-Object -ComObject WScript.Shell
    [void]$shell.AppActivate($Process.Id)
  } catch {
    Write-BridgeLog ('focus error: ' + $_.Exception.Message)
  }
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
  if ($process) {
    Write-BridgeLog ('winbox started pid=' + $process.Id)
    Focus-Process $process
  }
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
    $process = Start-Process -FilePath $putty -ArgumentList $arguments -PassThru
    Focus-Process $process
    return
  }
  $ssh = Find-Ssh
  if ($ssh) {
    try { Set-Clipboard -Value $password } catch {}
    $arguments = '-p ' + [string]$port + ' ' + (Quote-Arg ($user + '@' + $address))
    Write-BridgeLog ('ssh path=' + $ssh + ' args=' + $arguments)
    $process = Start-Process -FilePath $ssh -ArgumentList $arguments -PassThru
    Focus-Process $process
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

function Invoke-BridgeLaunch([string]$Ticket) {
  if (-not $Ticket) { throw 'ticket vacio.' }
  $body = @{ ticket = $Ticket } | ConvertTo-Json -Compress
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
    $data = $payload.data
    if (-not $data) { throw 'NetMonitor devolvio una respuesta no valida.' }
  } finally {
    $client.Dispose()
  }
  Write-BridgeLog ('ticket kind=' + [string]$data.kind + ' address=' + [string]$data.address + ' user=' + [string]$data.username + ' passLen=' + ([string]$data.password).Length + ' port=' + [string]$data.port)
  switch ([string]$data.kind) {
    'ssh' { Start-Ssh $data }
    'web' { Start-Web $data }
    default { Start-Winbox $data }
  }
  return $data
}

function Write-BridgeJson($Response, [int]$Status, $Value) {
  $json = $Value | ConvertTo-Json -Compress
  $bytes = [Text.Encoding]::UTF8.GetBytes($json)
  $Response.StatusCode = $Status
  $Response.ContentType = 'application/json'
  $Response.ContentLength64 = $bytes.Length
  $Response.OutputStream.Write($bytes, 0, $bytes.Length)
  $Response.OutputStream.Close()
}

function Start-BridgeServer {
  $listener = New-Object System.Net.HttpListener
  $listener.Prefixes.Add('http://127.0.0.1:47821/')
  try {
    $listener.Start()
  } catch {
    Write-BridgeLog ('helper listen error: ' + $_.Exception.Message)
    return
  }
  Write-BridgeLog 'helper listening on http://127.0.0.1:47821/'
  while ($listener.IsListening) {
    try { $context = $listener.GetContext() } catch { break }
    $request = $context.Request
    $response = $context.Response
    $response.Headers.Add('Access-Control-Allow-Origin', '*')
    $response.Headers.Add('Access-Control-Allow-Methods', 'GET, POST, OPTIONS')
    $response.Headers.Add('Access-Control-Allow-Headers', 'Content-Type, Access-Control-Request-Private-Network')
    $response.Headers.Add('Access-Control-Allow-Private-Network', 'true')
    try {
      if ($request.HttpMethod -eq 'OPTIONS') {
        $response.StatusCode = 204
        $response.Close()
        continue
      }
      if ($request.Url.AbsolutePath -eq '/health') {
        Write-BridgeJson $response 200 @{ ok = $true; version = '2' }
        continue
      }
      if ($request.Url.AbsolutePath -eq '/launch' -and $request.HttpMethod -eq 'POST') {
        $reader = New-Object System.IO.StreamReader($request.InputStream, [Text.Encoding]::UTF8)
        $raw = $reader.ReadToEnd()
        $reader.Close()
        $payload = $raw | ConvertFrom-Json
        $result = Invoke-BridgeLaunch ([string]$payload.ticket)
        Write-BridgeJson $response 200 @{ ok = $true; kind = [string]$result.kind }
        continue
      }
      Write-BridgeJson $response 404 @{ ok = $false; error = 'not found' }
    } catch {
      Write-BridgeLog ('helper request error: ' + $_.Exception.Message)
      try { Write-BridgeJson $response 400 @{ ok = $false; error = $_.Exception.Message } } catch {}
    }
  }
  $listener.Stop()
  $listener.Close()
}

if ($Serve) {
  Start-BridgeServer
  exit
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
  Invoke-BridgeLaunch $ticket | Out-Null
} catch {
  Show-BridgeError $_.Exception.Message
}
`
