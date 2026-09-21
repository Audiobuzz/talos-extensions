package main

import "testing"

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
