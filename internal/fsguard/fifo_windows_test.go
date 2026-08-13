//go:build windows

package fsguard

import "errors"

func makeFIFO(path string) error { return errors.New("FIFOs are unavailable on Windows") }
