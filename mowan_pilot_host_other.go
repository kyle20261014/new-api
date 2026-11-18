//go:build !linux

package main

import "fmt"

func acquirePilotHostLock() error {
	return fmt.Errorf("funding pilot requires the single Linux host writer fence")
}
