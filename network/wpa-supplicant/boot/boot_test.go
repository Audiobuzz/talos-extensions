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
	t.Setenv("WPA_LD_PRELOAD", "")
	if got := preloadEnv([]string{"A=1"}); len(got) != 1 {
		t.Fatalf("explicit empty must disable preload: %v", got)
	}
	os.Unsetenv("WPA_LD_PRELOAD")
	t.Setenv("PKCS11_PROVIDER_MODULE", "/nonexistent/module.so")
	if got := preloadEnv([]string{"A=1"}); len(got) != 1 {
		t.Fatalf("missing module must not be preloaded: %v", got)
	}
	f, err := os.CreateTemp(t.TempDir(), "mod*.so")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("PKCS11_PROVIDER_MODULE", f.Name())
	got := preloadEnv([]string{"A=1", "LD_PRELOAD=/old.so"})
	if len(got) != 2 || got[1] != "LD_PRELOAD="+f.Name() {
		t.Fatalf("preload not applied: %v", got)
	}
}
