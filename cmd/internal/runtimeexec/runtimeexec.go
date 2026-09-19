// Package runtimeexec validates the archive-owned Bun runtime before a launcher
// hands off to it. Rejecting a missing, symlinked or wrong-architecture binary
// here produces a clear archive error instead of an exec format failure.
package runtimeexec

import (
	"errors"
	"io"
	"os"
	"runtime"
)

const (
	elfHeaderSize = 20
	elfClass64    = 2
	elfDataLSB    = 1
)

// elfMachines pins the only two Linux architectures the release archives
// build. The values are the standard ELF e_machine identifiers.
var elfMachines = map[string]uint16{
	"amd64": 62,
	"arm64": 183,
}

// Validate requires path to be a regular, non-symlink executable that is a
// 64-bit little-endian ELF built for the launcher's own Linux architecture.
func Validate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("not a regular executable")
	}
	expected, err := elfMachine(runtime.GOARCH)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, elfHeaderSize)
	if _, err := io.ReadFull(file, header); err != nil {
		return errors.New("not a Linux ELF executable")
	}
	if header[0] != 0x7f || header[1] != 'E' || header[2] != 'L' || header[3] != 'F' ||
		header[4] != elfClass64 || header[5] != elfDataLSB {
		return errors.New("not a 64-bit little-endian Linux ELF executable")
	}
	if machine := uint16(header[18]) | uint16(header[19])<<8; machine != expected {
		return errors.New("runtime was built for a different Linux architecture")
	}
	return nil
}

func elfMachine(goarch string) (uint16, error) {
	machine, ok := elfMachines[goarch]
	if !ok {
		return 0, errors.New("launcher architecture has no supported Linux runtime")
	}
	return machine, nil
}
