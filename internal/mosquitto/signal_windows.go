//go:build windows

package mosquitto

import "fmt"

func defaultSignal(_ int) error {
	return fmt.Errorf("SIGHUP is not supported on Windows")
}
