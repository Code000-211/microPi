//go:build windows

package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetThreadTimes = kernel32.NewProc("GetThreadTimes")

// filetimeDuration reads a FILETIME that counts a duration in 100 ns units, as GetThreadTimes reports CPU time.
func filetimeDuration(ft windows.Filetime) time.Duration {
	return time.Duration(int64(ft.HighDateTime)<<32|int64(ft.LowDateTime)) * 100
}

func pipeReader(t testing.TB) (reader *terminalReader, r, w *os.File, cancel context.CancelFunc) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, err = newTerminalReader(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); reader.close(); _ = r.Close(); _ = w.Close() })
	return reader, r, w, cancel
}

// A Windows pipe handle is signalled from the moment the pipe exists, and again after every read, whether or not input is queued. Waiting on the handle therefore reported every pipe ready, and the reader went into a ReadFile that only input or the end of the pipe could return. The reader now makes the read itself the cancellable wait.
func TestWindowsPipeReadWaitsForInput(t *testing.T) {
	reader, _, w, _ := pipeReader(t)
	if data, err := reader.read(20); err != nil || data != nil {
		t.Fatalf("read on an empty pipe = %q, %v; want a timeout", data, err)
	}
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	if data, err := reader.read(10_000); err != nil || string(data) != "x" {
		t.Fatalf("read on a pipe with input = %q, %v; want x", data, err)
	}
	if data, err := reader.read(20); err != nil || data != nil {
		t.Fatalf("read on a pipe its reader emptied = %q, %v; want a timeout", data, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := reader.read(10_000); !errors.Is(err, io.EOF) || len(data) != 0 {
		t.Fatalf("read on a pipe whose writer closed = %q, %v; want the end of input", data, err)
	}
}

func TestWindowsPipeReadCancelsOnAnEmptyPipe(t *testing.T) {
	reader, _, _, cancel := pipeReader(t)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		data, err := reader.read(-1)
		if len(data) != 0 {
			t.Errorf("cancelled read took %q", data)
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled read = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not end an unbounded read on an empty pipe")
	}
}

// Input that arrives while the reader is blocked in the pipe is the read's own result.
func TestWindowsPipeReadWakesOnInput(t *testing.T) {
	reader, _, w, _ := pipeReader(t)
	for range 100 {
		started, done := make(chan struct{}), make(chan string, 1)
		go func() {
			close(started)
			data, err := reader.read(-1)
			if err != nil {
				t.Errorf("read: %v", err)
			}
			done <- string(data)
		}()
		<-started
		if _, err := w.WriteString("k"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-done:
			if got != "k" {
				t.Fatalf("read = %q, want k", got)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("input did not wake a read on the pipe")
		}
	}
}

// A read that timed out must not leave an abort that ends a later read of the same pipe.
func TestWindowsPipeReadTimeoutDoesNotAbortALaterRead(t *testing.T) {
	reader, _, w, _ := pipeReader(t)
	for i := range 300 {
		if data, err := reader.read(1); err != nil || data != nil {
			t.Fatalf("round %d: read on an empty pipe = %q, %v; want a timeout", i, data, err)
		}
		if _, err := w.WriteString("t"); err != nil {
			t.Fatal(err)
		}
		if data, err := reader.read(10_000); err != nil || string(data) != "t" {
			t.Fatalf("round %d: read on a pipe with input = %q, %v; want t", i, data, err)
		}
	}
}

// An idle read on a pipe blocks in the kernel, so it costs no CPU: a read that polled without a pause would use a whole core. The read runs on a locked thread so the thread's CPU time is the read's alone.
func TestWindowsPipeReadIdleStaysIdle(t *testing.T) {
	const idle = 500 * time.Millisecond
	const budget = 50 * time.Millisecond // the read costs a few milliseconds at most; a loop without a pause costs the whole 500 ms
	reader, _, _, _ := pipeReader(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cpu := func() time.Duration {
		var creation, exit, kernel, user windows.Filetime
		if r1, _, err := procGetThreadTimes.Call(uintptr(windows.CurrentThread()), uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r1 == 0 { //nolint:gosec // G103: GetThreadTimes writes four FILETIMEs, and x/sys has no wrapper for it.
			t.Fatal(err)
		}
		return filetimeDuration(kernel) + filetimeDuration(user)
	}
	before := cpu()
	if data, err := reader.read(int(idle / time.Millisecond)); err != nil || data != nil {
		t.Fatalf("idle read = %q, %v; want a timeout", data, err)
	}
	if used := cpu() - before; used > budget {
		t.Fatalf("a %v idle read on a pipe used %v of CPU, want at most %v", idle, used, budget)
	}
}

// A byte that arrives as the reader is cancelled is delivered or left in the pipe, never lost: a cancelled ReadFile takes nothing, and one that completed as the cancel landed returns its byte.
func TestWindowsPipeReadLosesNoByteAtCancel(t *testing.T) {
	for i := range 300 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		var mu sync.Mutex
		var delivered []byte
		done := make(chan error, 1)
		go func() {
			done <- ReadInputStream(ctx, r, func(data []byte) { mu.Lock(); delivered = append(delivered, data...); mu.Unlock() })
		}()
		go cancel()
		if _, err := w.WriteString("x"); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("round %d: reader ended with %v, want context.Canceled", i, err)
		}
		_ = w.Close()
		rest, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := string(delivered) + string(rest)
		mu.Unlock()
		if got != "x" {
			t.Fatalf("round %d: the byte written as the reader was cancelled came out as %q (delivered %q, left in the pipe %q), want exactly x", i, got, delivered, rest)
		}
		_ = r.Close()
	}
}

// Cancelling a reader that waits on an empty pipe ends it without taking input that arrives afterwards: the next reader of the pipe gets it. The framing deadline ends the reader's first wait, which proves the wait is not parked in a read it cannot leave.
func TestReadInputStreamLeavesAPipeForTheNextReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var expiredOnce sync.Once
	expired := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ReadInputStreamUntil(ctx, r,
			func() time.Time { return time.Now().Add(time.Millisecond) },
			func(data []byte) { t.Errorf("the reader took input %q from an empty pipe", data) },
			func() { expiredOnce.Do(func() { close(expired) }) })
	}()
	select {
	case <-expired:
	case <-time.After(10 * time.Second):
		_ = w.Close() // frees the reader, which sits in ReadFile
		t.Fatal("the reader's wait on an empty pipe did not time out: it reported the pipe ready and sits in ReadFile")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("reader ended with %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		_ = w.Close()
		t.Fatal("cancellation did not end the reader")
	}
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if _, err := io.ReadFull(r, one[:]); err != nil || one[0] != 'x' {
		t.Fatalf("next reader got %q, %v; want the byte written after the first reader ended", one[:], err)
	}
}

// A pipe that ends delivers what it queued, then the end of input.
func TestReadInputStreamDeliversAPipeToItsEnd(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if _, err := w.WriteString("ab"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var got []byte
	err = ReadInputStream(t.Context(), r, func(data []byte) { got = append(got, data...) })
	if !errors.Is(err, io.EOF) || string(got) != "ab" {
		t.Fatalf("ReadInputStream = %q, %v; want \"ab\" then io.EOF", got, err)
	}
}

// A byte queued before the read is returned by the one ReadFile that waits for it.
func BenchmarkWindowsPipeReadQueuedInput(b *testing.B) {
	reader, _, w, _ := pipeReader(b)
	var one [1]byte
	b.ReportAllocs()
	for b.Loop() {
		if _, err := w.Write(one[:]); err != nil {
			b.Fatal(err)
		}
		if data, err := reader.read(-1); err != nil || len(data) != 1 {
			b.Fatalf("read = %q, %v", data, err)
		}
	}
}
