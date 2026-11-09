package bench

import "time"

// SetAfterCopy runs f between copying the files of a DB and checking them again, until the test
// ends (it makes a copy race).
func SetAfterCopy(f func(try int)) (restore func()) {
	old := afterCopy
	afterCopy = f
	return func() { afterCopy = old }
}

// SetRetryWait changes how long CopyDB waits after the given failed try, until the test ends.
func SetRetryWait(f func(try int) time.Duration) (restore func()) {
	old := retryWait
	retryWait = f
	return func() { retryWait = old }
}

// DefaultRetryWait is the wait CopyDB uses unless a test changes it.
func DefaultRetryWait(try int) time.Duration { return defaultRetryWait(try) }

// CopyFiles runs the copy step of CopyDB alone, so a test can look at the files it copies.
func CopyFiles(src, dst string) error { return copyFiles(src, dst) }
