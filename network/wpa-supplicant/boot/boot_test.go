package main

import (
	"os"
	"testing"
)

func TestEnsureCtrlInterface(t *testing.T) {
	in := "network={\n  key_mgmt=IEEE8021X\n}\n"
	out := ensureCtrlInterface(in)
	if out != "ctrl_interface=/run/wpa_supplicant\n"+in {
		t.Fatalf("got %q", out)
	}
	keep := "ctrl_interface=/tmp/x\n" + in
	if ensureCtrlInterface(keep) != keep {
		t.Fatal("existing ctrl_interface must be preserved")
	}
}

func TestPortAuthorized(t *testing.T) {
	s := "bssid=00:00:00:00:00:00\nsuppPortStatus=Authorized\nEAP state=SUCCESS\n"
	if !portAuthorized(s) {
		t.Fatal("expected authorized")
	}
	if portAuthorized("suppPortStatus=Unauthorized\n") {
		t.Fatal("expected unauthorized")
	}
	if field(s, "EAP state") != "SUCCESS" {
		t.Fatal("field parse")
	}
}

func TestPreloadEnv(t *testing.T) {
	os.Unsetenv("WPA_LD_PRELOAD")
	t.Setenv("PKCS11_PROVIDER_MODULE", "/some/module.so")
	if got := preloadEnv([]string{"A=1"}); len(got) != 1 {
		t.Fatalf("no preload by default: %v", got)
	}
	t.Setenv("WPA_LD_PRELOAD", "/nonexistent/module.so")
	if got := preloadEnv([]string{"A=1"}); len(got) != 1 {
		t.Fatalf("missing library must not be preloaded: %v", got)
	}
	f, err := os.CreateTemp(t.TempDir(), "mod*.so")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("WPA_LD_PRELOAD", f.Name())
	got := preloadEnv([]string{"A=1", "LD_PRELOAD=/old.so"})
	if len(got) != 2 || got[1] != "LD_PRELOAD="+f.Name() {
		t.Fatalf("explicit preload not applied: %v", got)
	}
}

func TestSocketEnv(t *testing.T) {
	os.Unsetenv("PKCS11_TPM_SOCKET")
	got := withSocket([]string{"A=1"})
	if len(got) != 2 || got[1] != "PKCS11_TPM_SOCKET=/run/pkcs11-tpm/pkcs11-tpm.sock" {
		t.Fatalf("default socket not exported: %v", got)
	}
	if got := withSocket([]string{"PKCS11_TPM_SOCKET=/x"}); len(got) != 1 {
		t.Fatalf("explicit socket must be kept: %v", got)
	}
	t.Setenv("TPM_SERVER", "")
	if tpmServerPath() != "" {
		t.Fatal("TPM_SERVER=\"\" must disable the server")
	}
}
