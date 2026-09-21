package main

// finit_module(2) syscall number; the Go standard library does not export it
// and golang.org/x/sys is avoided to keep this binary dependency-free.
const sysFinitModule = 313
