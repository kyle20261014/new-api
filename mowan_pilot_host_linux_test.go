package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFundingPilotHostLockProcess(t *testing.T) {
	if os.Getenv("TEST_PILOT_HOST_CHILD") != "" {
		if err := acquirePilotHostLock(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			if errors.Is(err, unix.EWOULDBLOCK) {
				os.Exit(20)
			}
			os.Exit(21)
		}
		fmt.Println("HELD")
		bufio.NewScanner(os.Stdin).Scan()
		os.Exit(0)
	}
	if os.Getenv("TEST_PILOT_HOST_NAMESPACE") != "1" {
		t.Skip("run in a disposable root mount namespace with TEST_PILOT_HOST_NAMESPACE=1")
	}
	// Caller supplies a private mount namespace. Never mount over a live fence.
	if _, err := os.Stat(pilotHostLockPath); !os.IsNotExist(err) {
		t.Fatal("fixed lock path must be absent in the disposable namespace")
	}
	if err := acquirePilotHostLock(); err == nil {
		t.Fatal("missing mount accepted")
	}
	dir := filepath.Dir(pilotHostLockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", dir, "tmpfs", 0, "mode=0755"); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(dir, 0)
	source := filepath.Join(t.TempDir(), "writer.lock")
	if err := os.WriteFile(source, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pilotHostLockPath, nil, 0444); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(source, pilotHostLockPath, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(pilotHostLockPath, 0)
	if err := acquirePilotHostLock(); err == nil {
		t.Fatal("writable mount accepted")
	}
	if err := unix.Mount("", pilotHostLockPath, "", unix.MS_BIND|unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		t.Fatal(err)
	}
	if err := acquirePilotHostLock(); err == nil {
		t.Fatal("writable source permissions accepted")
	}
	if err := os.Chmod(source, 0444); err != nil {
		t.Fatal(err)
	}
	child := func() *exec.Cmd {
		// A distinct container-like mount namespace still sees the same inode.
		cmd := exec.Command("unshare", "--mount", "--propagation", "private", os.Args[0], "-test.run=^TestFundingPilotHostLockProcess$")
		cmd.Env = append(os.Environ(), "TEST_PILOT_HOST_CHILD=1")
		return cmd
	}
	for _, abrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("process_exit_abrupt_%t", abrupt), func(t *testing.T) {
			holder := child()
			in, err := holder.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := holder.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			holder.Stderr = os.Stderr
			if err := holder.Start(); err != nil {
				t.Fatal(err)
			}
			defer holder.Process.Kill()
			scanner := bufio.NewScanner(out)
			if !scanner.Scan() || scanner.Text() != "HELD" {
				t.Fatal("holder failed to acquire fence")
			}
			if output, err := child().CombinedOutput(); err == nil {
				t.Fatalf("second process acquired live holder's inode: %s", output)
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 20 {
				t.Fatalf("competitor did not reach the lock check: %v: %s", err, output)
			}
			if abrupt {
				_ = holder.Process.Kill()
			} else {
				_ = in.Close()
			}
			err = holder.Wait()
			if !abrupt && err != nil {
				t.Fatal(err)
			}
			if output, err := child().CombinedOutput(); err != nil {
				t.Fatalf("process death did not release fence: %v: %s", err, output)
			}
		})
	}
}
