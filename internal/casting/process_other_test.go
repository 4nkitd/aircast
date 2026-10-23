//go:build !linux && !windows

package casting

func platformHelper(string) bool { return false }
