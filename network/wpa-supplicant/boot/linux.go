package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// loadModule loads <name>.ko from /usr/lib/modules/<release>/ (bind-mounted
// from the host) with finit_module(2). It resolves nothing: a module with
// unmet dependencies fails and the log points the user at
// KernelModuleConfig, which is the supported way to load modules on Talos.
func loadModule(name string) error {
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err != nil {
		return err
	}
	release := cstr(uts.Release[:])
	root := filepath.Join("/usr/lib/modules", release)
	var path string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == name+".ko" {
			path = p
			return filepath.SkipAll
		}
		return nil
	})
	if path == "" {
		return fmt.Errorf("%s.ko not found under %s", name, root)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	params := []byte("\x00")
	_, _, errno := syscall.Syscall(sysFinitModule, f.Fd(), uintptr(unsafe.Pointer(&params[0])), 0)
	switch errno {
	case 0:
		return nil
	case syscall.EEXIST:
		return nil // already loaded
	default:
		return errors.New(errno.Error())
	}
}

// ifaceExistsByAltname checks whether iface is a kernel altname (as created
// by a Talos LinkAliasConfig) by asking for its index via SIOCGIFINDEX,
// which the kernel resolves through the altname table too.
func ifaceExistsByAltname(iface string) bool {
	if len(iface) >= syscall.IFNAMSIZ {
		return false // altnames longer than IFNAMSIZ can't be used by wpa_supplicant either
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer syscall.Close(fd)
	var req [syscall.IFNAMSIZ + 24]byte
	copy(req[:], iface)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFINDEX, uintptr(unsafe.Pointer(&req[0])))
	return errno == 0
}

func cstr(b []int8) string {
	var sb strings.Builder
	for _, c := range b {
		if c == 0 {
			break
		}
		sb.WriteByte(byte(c))
	}
	return sb.String()
}
