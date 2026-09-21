package main

import (
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// watchStatus polls wpa_supplicant's control socket for the supplicant port
// state and mirrors it to the marker file: present while Authorized, absent
// otherwise. Log lines are emitted on transitions only, and never include
// identity material.
func watchStatus(ctx context.Context, iface, marker string) {
	sock := filepath.Join(runDir, iface)
	authorized := false
	for {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
		status, err := ctrlRequest(sock, "STATUS")
		if err != nil {
			if authorized {
				log.Printf("%s: supplicant unreachable, clearing authorized state", iface)
				authorized = false
				_ = os.Remove(marker)
			}
			continue
		}
		now := portAuthorized(status)
		if now != authorized {
			authorized = now
			if authorized {
				log.Printf("%s: port Authorized", iface)
				_ = os.WriteFile(marker, []byte("authorized\n"), 0o644)
			} else {
				log.Printf("%s: port no longer Authorized (%s)", iface, field(status, "suppPortStatus"))
				_ = os.Remove(marker)
			}
		}
	}
}

// ctrlRequest sends one command over the wpa_supplicant UNIX datagram
// control interface and returns the reply.
func ctrlRequest(sock, cmd string) (string, error) {
	local := filepath.Join(runDir, ".boot-"+filepath.Base(sock))
	_ = os.Remove(local)
	laddr, _ := net.ResolveUnixAddr("unixgram", local)
	raddr, _ := net.ResolveUnixAddr("unixgram", sock)
	c, err := net.DialUnix("unixgram", laddr, raddr)
	if err != nil {
		return "", err
	}
	defer func() { c.Close(); _ = os.Remove(local) }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(cmd)); err != nil {
		return "", err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func field(status, key string) string {
	for _, line := range strings.Split(status, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func portAuthorized(status string) bool {
	return field(status, "suppPortStatus") == "Authorized"
}
