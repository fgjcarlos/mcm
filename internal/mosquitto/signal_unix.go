//go:build !windows

package mosquitto

import "syscall"

func defaultSignal(pid int) error {
	return syscall.Kill(pid, syscall.SIGHUP)
}
