// wpa-supplicant-boot is the entrypoint of the Talos wpa-supplicant system
// extension (the containerboot pattern). It is deliberately small and
// stdlib-only:
//
//  1. optionally load kernel modules (WPA_MODULES, e.g. "macsec"),
//  2. wait for the interface to exist,
//  3. run wpa_supplicant in the foreground with the user's configuration,
//     restarting it with backoff if it exits,
//  4. watch the control socket and mirror the port state to
//     /run/wpa_supplicant/<iface>.authorized so other extension services can
//     `depends: - path:` on it.
//
// All settings come from the environment (ExtensionServiceConfig):
//
//	WPA_INTERFACE  interface name or Talos link alias (required)
//	WPA_DRIVER     wpa_supplicant driver: macsec_linux (default) or wired
//	WPA_CONFIG     config path, default /etc/wpa_supplicant/wpa_supplicant.conf
//	WPA_MODULES    comma-separated kernel modules to load first (best effort)
//	WPA_DEBUG      any value: pass -d to wpa_supplicant
//	WPA_EXTRA_ARGS extra wpa_supplicant arguments, whitespace separated
//	WPA_LD_PRELOAD library to preload into wpa_supplicant (normally unset)
//	TPM_SERVER     path of pkcs11-tpm-server, "" to not run one
//	               (default /usr/local/bin/pkcs11-tpm-server); it is started and
//	               supervised alongside wpa_supplicant and listens on
//	               PKCS11_TPM_SOCKET (default /run/pkcs11-tpm/pkcs11-tpm.sock)
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const runDir = "/run/wpa_supplicant"

func main() {
	log.SetFlags(0)
	log.SetPrefix("wpa-supplicant-boot: ")

	iface := os.Getenv("WPA_INTERFACE")
	if iface == "" {
		log.Fatal("WPA_INTERFACE is not set (ExtensionServiceConfig environment)")
	}
	driver := envOr("WPA_DRIVER", "macsec_linux")
	config := envOr("WPA_CONFIG", "/etc/wpa_supplicant/wpa_supplicant.conf")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := os.MkdirAll(runDir, 0o755); err != nil {
		log.Fatalf("%s: %v", runDir, err)
	}
	if mods := os.Getenv("WPA_MODULES"); mods != "" {
		for _, m := range strings.Split(mods, ",") {
			if m = strings.TrimSpace(m); m != "" {
				if err := loadModule(m); err != nil {
					log.Printf("module %s: %v (continuing; use KernelModuleConfig if it is required)", m, err)
				} else {
					log.Printf("module %s loaded", m)
				}
			}
		}
	}
	if err := waitForInterface(ctx, iface); err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(config); err != nil {
		log.Fatalf("configuration %s: %v", config, err)
	}
	conf, err := prepareConfig(config, filepath.Join(runDir, "wpa_supplicant.conf"))
	if err != nil {
		log.Fatal(err)
	}

	marker := filepath.Join(runDir, iface+".authorized")
	_ = os.Remove(marker)
	go watchStatus(ctx, iface, marker)

	// The PKCS#11 token runs in its own process: a Go shared object cannot be
	// loaded into wpa_supplicant on musl (see preloadEnv), so the module in
	// the supplicant is the C client and pkcs11-tpm-server holds the key.
	if srv := tpmServerPath(); srv != "" {
		go supervise(ctx, "pkcs11-tpm-server", func() *exec.Cmd {
			return exec.CommandContext(ctx, srv, "-socket", tpmSocket())
		})
		waitForSocket(ctx, tpmSocket())
	}

	backoff := time.Second
	for ctx.Err() == nil {
		args := []string{"-D" + driver, "-i" + iface, "-c" + conf, "-P" + filepath.Join(runDir, iface+".pid")}
		if os.Getenv("WPA_DEBUG") != "" {
			args = append(args, "-d")
		}
		args = append(args, strings.Fields(os.Getenv("WPA_EXTRA_ARGS"))...)
		log.Printf("starting wpa_supplicant %s", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, "wpa_supplicant", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.Env = preloadEnv(withSocket(os.Environ()))
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		start := time.Now()
		err := cmd.Run()
		_ = os.Remove(marker)
		if ctx.Err() != nil {
			return
		}
		log.Printf("wpa_supplicant exited after %s: %v; restarting in %s", time.Since(start).Round(time.Second), err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// prepareConfig copies the user's configuration into /run and guarantees a
// ctrl_interface line so the status watcher (and wpa_cli) can reach the
// daemon. Everything else in the file is the user's business.
func prepareConfig(src, dst string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	out := ensureCtrlInterface(string(b))
	if err := os.WriteFile(dst, []byte(out), 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func ensureCtrlInterface(cfg string) string {
	for _, line := range strings.Split(cfg, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ctrl_interface=") {
			return cfg
		}
	}
	return "ctrl_interface=" + runDir + "\n" + cfg
}

// waitForInterface polls sysfs until the interface (or an alias resolving
// to it, which the kernel exposes through the same ioctl paths) exists.
func waitForInterface(ctx context.Context, iface string) error {
	logged := false
	for {
		if _, err := os.Stat(filepath.Join("/sys/class/net", iface)); err == nil {
			return nil
		}
		if ifaceExistsByAltname(iface) {
			return nil
		}
		if !logged {
			log.Printf("waiting for interface %s", iface)
			logged = true
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("interrupted while waiting for %s", iface)
		}
	}
}

// preloadEnv adds LD_PRELOAD=<lib> for wpa_supplicant only when
// WPA_LD_PRELOAD is set. It is not the default: a Go c-shared library such as
// the in-process pkcs11-tpm.so can neither be dlopen()ed nor preloaded on
// musl (golang/go#54805), which is why the extension runs pkcs11-tpm-server
// as a separate process and loads the C client module instead.
func preloadEnv(env []string) []string {
	lib := os.Getenv("WPA_LD_PRELOAD")
	if lib == "" {
		return env
	}
	if _, err := os.Stat(lib); err != nil {
		log.Printf("not preloading %s: %v", lib, err)
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "LD_PRELOAD=") && !strings.HasPrefix(kv, "WPA_LD_PRELOAD=") {
			out = append(out, kv)
		}
	}
	return append(out, "LD_PRELOAD="+lib)
}

const defaultTPMServer = "/usr/local/bin/pkcs11-tpm-server"

func tpmServerPath() string {
	if v, ok := os.LookupEnv("TPM_SERVER"); ok {
		return v // "" disables
	}
	if _, err := os.Stat(defaultTPMServer); err != nil {
		return ""
	}
	return defaultTPMServer
}

func tpmSocket() string {
	return envOr("PKCS11_TPM_SOCKET", "/run/pkcs11-tpm/pkcs11-tpm.sock")
}

// withSocket makes sure the client module inside wpa_supplicant finds the
// server even when only the default socket path is in use.
func withSocket(env []string) []string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PKCS11_TPM_SOCKET=") {
			return env
		}
	}
	return append(env, "PKCS11_TPM_SOCKET="+tpmSocket())
}

// supervise runs a helper process and restarts it with backoff until ctx ends.
func supervise(ctx context.Context, name string, mk func() *exec.Cmd) {
	backoff := time.Second
	for ctx.Err() == nil {
		cmd := mk()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		start := time.Now()
		err := cmd.Run()
		if ctx.Err() != nil {
			return
		}
		log.Printf("%s exited after %s: %v; restarting in %s", name, time.Since(start).Round(time.Second), err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// waitForSocket gives the server a moment to come up before wpa_supplicant
// starts; a slow server is not fatal, the client reconnects on demand.
func waitForSocket(ctx context.Context, path string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
	log.Printf("%s not present yet; continuing", path)
}
