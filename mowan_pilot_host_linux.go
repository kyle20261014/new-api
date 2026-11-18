package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const pilotHostLockPath = "/run/mowan-pilot/writer.lock"

// ponytail: one Linux host only. Multi-host operation needs a business-write
// fence, not another independent flock file or a faster PostgreSQL watchdog.
func acquirePilotHostLock() error {
	mounts, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer mounts.Close()
	mounted := false
	scanner := bufio.NewScanner(mounts)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 6 && fields[4] == pilotHostLockPath && strings.Contains(","+fields[5]+",", ",ro,") {
			mounted = true
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if !mounted {
		return fmt.Errorf("pilot requires a read-only host file bind mount at %s", pilotHostLockPath)
	}
	fd, err := unix.Open(pilotHostLockPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	var fs unix.Statfs_t
	if err = unix.Fstat(fd, &st); err == nil {
		err = unix.Fstatfs(fd, &fs)
	}
	if err == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0444 || st.Uid != 0 || st.Nlink != 1 ||
		(fs.Type != unix.EXT4_SUPER_MAGIC && fs.Type != unix.XFS_SUPER_MAGIC && fs.Type != unix.BTRFS_SUPER_MAGIC)) {
		err = fmt.Errorf("pilot writer lock must be a root-owned 0444 regular file on local ext4, XFS or Btrfs")
	}
	if err == nil {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
	}
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("pilot host writer lock unavailable: %w", err)
	}
	// Deliberately retain the raw descriptor, without an os.File finalizer or
	// shutdown unlock. Only process death releases it, even if the PG lock dies.
	return nil
}
