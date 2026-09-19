package persist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const maxJSONBytes = 16 * 1024 * 1024

type Store struct {
	Dir string
}

func Read[T any](s Store, name string, dst *T, validate func(T) error) (bool, error) {
	var failures []error
	for _, path := range []string{filepath.Join(s.Dir, name), filepath.Join(s.Dir, name) + ".bak"} {
		raw, _, err := ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil {
			err = Decode(raw, dst, validate)
		}
		if err == nil {
			return true, nil
		}
		failures = append(failures, fmt.Errorf("read %s: %w", filepath.Base(path), err))
	}
	return false, errors.Join(failures...)
}

func ReadFile(path string) ([]byte, os.FileMode, error) {
	return ReadBoundedFile(path, maxJSONBytes)
}

func OpenRegularFile(path string, limit int64) (*os.File, os.FileInfo, error) {
	// Reject an existing named pipe or device before opening it.
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("only regular files can be read")
	}
	if info.Size() > limit {
		return nil, nil, fmt.Errorf("file exceeds the %d-byte limit", limit)
	}
	// A path can become a FIFO between Stat and Open. Nonblocking open lets
	// the descriptor check below reject it without waiting for a writer. It
	// also avoids Go toggling blocking mode twice for each regular-file open.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err = file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		file.Close()
		return nil, nil, fmt.Errorf("file changed while opening")
	}
	return file, info, nil
}

func ReadBoundedFile(path string, limit int64) ([]byte, os.FileMode, error) {
	file, info, err := OpenRegularFile(path, limit)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	// Use the checked size as a capacity hint without trusting it as the limit.
	// Leave ReadFrom's minimum slack so an EOF probe does not grow a full buffer.
	var buffer bytes.Buffer
	buffer.Grow(int(info.Size()) + bytes.MinRead)
	_, err = buffer.ReadFrom(io.LimitReader(file, limit+1))
	raw := buffer.Bytes()
	if int64(len(raw)) > limit {
		return nil, 0, fmt.Errorf("file grew beyond its %d-byte limit", limit)
	}
	return raw, info.Mode().Perm(), err
}

func Decode[T any](raw []byte, dst *T, validate func(T) error) error {
	var candidate T
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return err
	}
	if validate != nil {
		if err := validate(candidate); err != nil {
			return err
		}
	}
	*dst = candidate
	return nil
}

func Write[T any](s Store, name string, value T, validate func(T) error) error {
	_, err := WriteWithOutcome(s, name, value, validate, PublishFaults{})
	return err
}

// ReplaceFaults injects deterministic filesystem failures into the staged
// temp-file replacement sequence. A nil entry disables that fault. Callers
// inject realistic error values such as syscall.ENOSPC (disk full),
// os.ErrPermission (permission denied) or a rename error and observe the same
// typed publication outcome production would report. ShortWrite simulates a
// partial write by persisting only that many payload bytes; the length check
// then reports io.ErrShortWrite. Injected faults never fall back to an
// in-place write of the target.
type ReplaceFaults struct {
	FailCreateTemp error
	FailChmod      error
	FailWrite      error
	FailFileSync   error
	FailClose      error
	FailRename     error
	ShortWrite     int
}

// atomicReplaceWithFaults stages data in a restrictive temp file in target's
// directory, flushes it, then renames it over target. The rename is the only
// visibility point, so a failure at any earlier step leaves the previous
// target intact and removes the temp file. It never writes to target directly.
func atomicReplaceWithFaults(target string, data []byte, mode os.FileMode, faults ReplaceFaults) error {
	if faults.FailCreateTemp != nil {
		return faults.FailCreateTemp
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if faults.FailChmod != nil {
		temporary.Close()
		return faults.FailChmod
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if faults.FailWrite != nil {
		temporary.Close()
		return faults.FailWrite
	}
	payload := data
	if faults.ShortWrite > 0 && faults.ShortWrite < len(data) {
		payload = data[:faults.ShortWrite]
	}
	written, err := temporary.Write(payload)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		temporary.Close()
		return err
	}
	if faults.FailFileSync != nil {
		temporary.Close()
		return faults.FailFileSync
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if faults.FailClose != nil {
		return faults.FailClose
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if faults.FailRename != nil {
		return faults.FailRename
	}
	return os.Rename(temporaryName, target)
}

func AtomicReplace(target string, data []byte, mode os.FileMode) error {
	return atomicReplaceWithFaults(target, data, mode, ReplaceFaults{})
}
