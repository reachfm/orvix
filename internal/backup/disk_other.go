//go:build !unix

package backup

func diskFreeBytes(path string) int64 {
	return 0
}
