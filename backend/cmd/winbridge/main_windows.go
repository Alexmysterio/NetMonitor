//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	helperAddr       = "127.0.0.1:47821"
	clipboardText    = 13
	gmemMoveable     = 0x0002
	mbOK             = 0x00000000
	mbIconError      = 0x00000010
	mbIconInfo       = 0x00000040
	createNewConsole = 0x00000010
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")
)

type redeemData struct {
	Kind       string `json:"kind"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	UseTLS     bool   `json:"use_tls"`
	Path       string `json:"path"`
	DeviceName string `json:"device_name"`
}

type redeemResponse struct {
	Success bool       `json:"success"`
	Data    redeemData `json:"data"`
	Error   string     `json:"error"`
}

type launchRequest struct {
	Server string `json:"server"`
	Ticket string `json:"ticket"`
}

func messageBox(title, text string, flags uint32) {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
}

func setClipboard(text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	h, _, callErr := procGlobalAlloc.Call(gmemMoveable, uintptr(len(utf16)*2))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %v", callErr)
	}
	ptr, _, callErr := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock: %v", callErr)
	}
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)*2))
	procGlobalUnlock.Call(h)
	if ok, _, callErr := procOpenClipboard.Call(0); ok == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("OpenClipboard: %v", callErr)
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if ok, _, callErr := procSetClipboardData.Call(clipboardText, h); ok == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %v", callErr)
	}
	runtime.KeepAlive(utf16)
	return nil
}

func localDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "NetMonitor")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func logPath() string { return filepath.Join(localDir(), "bridge.log") }

func logf(format string, args ...any) {
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02T15:04:05"), fmt.Sprintf(format, args...))
}

func configuredServer() string {
	raw, err := os.ReadFile(filepath.Join(localDir(), "server.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(string(raw)), "/")
}

func validServer(server string) bool {
	return strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://")
}

func redeem(server, ticket string) (redeemData, error) {
	var out redeemData
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if !validServer(server) {
		return out, errors.New("servidor de NetMonitor no válido")
	}
	if strings.TrimSpace(ticket) == "" {
		return out, errors.New("ticket vacío")
	}
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	req, err := http.NewRequest(http.MethodPost, server+"/api/v1/tools/winbox/redeem", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("NetMonitor respondió %d", resp.StatusCode)
	}
	var payload redeemResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return out, fmt.Errorf("respuesta no válida: %w", err)
	}
	if !payload.Success {
		if payload.Error != "" {
			return out, errors.New(payload.Error)
		}
		return out, errors.New("NetMonitor rechazó el ticket")
	}
	return payload.Data, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func configuredPath(name string) string {
	raw, err := os.ReadFile(filepath.Join(localDir(), name))
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(raw))
	if fileExists(path) {
		return path
	}
	return ""
}

func appPath(name string) string {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, err := key.GetStringValue("")
		key.Close()
		if err == nil && fileExists(value) {
			return value
		}
	}
	return ""
}

func firstExisting(candidates []string) string {
	for _, candidate := range candidates {
		if candidate != "" && fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func findWinbox() string {
	if path := configuredPath("winbox.txt"); path != "" {
		return path
	}
	candidates := []string{}
	if base := os.Getenv("ProgramFiles"); base != "" {
		candidates = append(candidates,
			filepath.Join(base, "Winbox", "winbox.exe"),
			filepath.Join(base, "Winbox", "winbox64.exe"),
			filepath.Join(base, "winbox64.exe"),
			filepath.Join(base, "MikroTik", "Winbox", "winbox.exe"),
			filepath.Join(base, "MikroTik", "Winbox", "winbox64.exe"),
		)
	}
	if base := os.Getenv("ProgramFiles(x86)"); base != "" {
		candidates = append(candidates,
			filepath.Join(base, "Winbox", "winbox.exe"),
			filepath.Join(base, "Winbox", "winbox64.exe"),
		)
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		candidates = append(candidates,
			filepath.Join(base, "Programs", "Winbox", "winbox.exe"),
			filepath.Join(base, "Programs", "Winbox", "winbox64.exe"),
		)
	}
	candidates = append(candidates, appPath("winbox.exe"), appPath("winbox64.exe"))
	for _, name := range []string{"winbox.exe", "winbox64.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	return firstExisting(candidates)
}

func findPutty() string {
	if path := configuredPath("putty.txt"); path != "" {
		return path
	}
	candidates := []string{}
	if base := os.Getenv("ProgramFiles"); base != "" {
		candidates = append(candidates, filepath.Join(base, "PuTTY", "putty.exe"))
	}
	if base := os.Getenv("ProgramFiles(x86)"); base != "" {
		candidates = append(candidates, filepath.Join(base, "PuTTY", "putty.exe"))
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		candidates = append(candidates, filepath.Join(base, "Programs", "PuTTY", "putty.exe"))
	}
	candidates = append(candidates, appPath("putty.exe"))
	if path, err := exec.LookPath("putty.exe"); err == nil {
		candidates = append(candidates, path)
	}
	return firstExisting(candidates)
}

func findSSH() string {
	candidates := []string{}
	if base := os.Getenv("SystemRoot"); base != "" {
		candidates = append(candidates, filepath.Join(base, "System32", "OpenSSH", "ssh.exe"))
	}
	if base := os.Getenv("ProgramFiles"); base != "" {
		candidates = append(candidates, filepath.Join(base, "OpenSSH", "ssh.exe"))
	}
	if path, err := exec.LookPath("ssh.exe"); err == nil {
		candidates = append(candidates, path)
	}
	return firstExisting(candidates)
}

func hostPort(host string, port, defaultPort int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port > 0 && port != defaultPort {
		return fmt.Sprintf("%s:%d", host, port)
	}
	return host
}

func redacted(args []string, password string) []string {
	out := make([]string, len(args))
	copy(out, args)
	if password == "" {
		return out
	}
	for i := range out {
		if out[i] == password {
			out[i] = "<password>"
		}
	}
	return out
}

func launchWinbox(data redeemData) error {
	exe := findWinbox()
	if exe == "" {
		return errors.New("No se encontró Winbox. Instala Winbox o añade su carpeta al PATH.")
	}
	address := hostPort(data.Address, data.Port, 8291)
	args := []string{address, data.Username}
	if data.Password != "" {
		args = append(args, data.Password)
	}
	logf("winbox path=%s args=%v", exe, redacted(args, data.Password))
	return exec.Command(exe, args...).Start()
}

func launchSSH(data redeemData) error {
	port := data.Port
	if port <= 0 {
		port = 22
	}
	if putty := findPutty(); putty != "" {
		args := []string{"-ssh", "-P", strconv.Itoa(port), "-l", data.Username, "-pw", data.Password, data.Address}
		logf("ssh path=%s args=%v", putty, redacted(args, data.Password))
		return exec.Command(putty, args...).Start()
	}
	ssh := findSSH()
	if ssh == "" {
		return errors.New("No se encontró PuTTY ni OpenSSH. Instala PuTTY o activa el Cliente OpenSSH de Windows.")
	}
	clipErr := setClipboard(data.Password)
	args := []string{"-p", strconv.Itoa(port), data.Username + "@" + data.Address}
	cmd := exec.Command(ssh, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	logf("ssh path=%s args=%v", ssh, args)
	if err := cmd.Start(); err != nil {
		return err
	}
	message := "No se encontró PuTTY. Se abrió OpenSSH."
	if clipErr == nil {
		message += " La contraseña está en el portapapeles; pégala cuando se solicite."
	} else {
		message += " No se pudo copiar la contraseña al portapapeles."
	}
	messageBox("NetMonitor - SSH", message, mbOK|mbIconInfo)
	return nil
}

func launchWeb(data redeemData) error {
	scheme := "http"
	defaultPort := 80
	if data.UseTLS {
		scheme = "https"
		defaultPort = 443
	}
	port := data.Port
	if port <= 0 {
		port = defaultPort
	}
	path := data.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target := scheme + "://" + hostPort(data.Address, port, defaultPort) + path
	_ = setClipboard(data.Password)
	logf("web url=%s", target)
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}

func launch(data redeemData) error {
	switch data.Kind {
	case "ssh":
		return launchSSH(data)
	case "web":
		return launchWeb(data)
	default:
		return launchWinbox(data)
	}
}

func parseLaunch(raw string) (server, ticket string, err error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "netmonitor-winbox://"):
		ticket = strings.Trim(strings.TrimPrefix(raw, "netmonitor-winbox://"), "/")
	case strings.HasPrefix(raw, "netmonitor-tools://"):
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", parseErr
		}
		query := u.Query()
		ticket = query.Get("ticket")
		server = query.Get("server")
	default:
		return "", "", errors.New("enlace de NetMonitor no válido")
	}
	if ticket == "" {
		return "", "", errors.New("el enlace no contiene ticket")
	}
	if configured := configuredServer(); configured != "" {
		server = configured
	}
	if server == "" {
		return "", "", errors.New("el puente no tiene configurado el servidor de NetMonitor")
	}
	return server, ticket, nil
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Access-Control-Request-Private-Network")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": "1"})
	})
	mux.HandleFunc("/launch", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "método no permitido"})
			return
		}
		var req launchRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "petición no válida"})
			return
		}
		server := req.Server
		if configured := configuredServer(); configured != "" {
			server = configured
		}
		data, err := redeem(server, req.Ticket)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if err := launch(data); err != nil {
			logf("launch error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": data.Kind})
	})

	var listener net.Listener
	var listenErr error
	for attempt := 0; attempt < 20; attempt++ {
		listener, listenErr = net.Listen("tcp", helperAddr)
		if listenErr == nil {
			break
		}
		// TIME_WAIT after an upgrade, or another instance is already running.
		logf("helper listen retry: %v", listenErr)
		time.Sleep(500 * time.Millisecond)
	}
	if listener == nil {
		logf("helper listen: %v", listenErr)
		return
	}
	logf("helper listening on %s", helperAddr)
	if err := http.Serve(listener, mux); err != nil {
		logf("helper serve: %v", err)
	}
}

func main() {
	if len(os.Args) < 2 {
		messageBox("NetMonitor", "Este programa lo usa NetMonitor para abrir Winbox, SSH y Web. Instálalo desde Accesos → Instalar o actualizar puente.", mbOK|mbIconInfo)
		return
	}
	arg := os.Args[1]
	if arg == "--serve" {
		serve()
		return
	}
	server, ticket, err := parseLaunch(arg)
	if err != nil {
		messageBox("NetMonitor", err.Error(), mbOK|mbIconError)
		return
	}
	data, err := redeem(server, ticket)
	if err != nil {
		messageBox("NetMonitor", err.Error(), mbOK|mbIconError)
		return
	}
	if err := launch(data); err != nil {
		messageBox("NetMonitor", err.Error(), mbOK|mbIconError)
	}
}
