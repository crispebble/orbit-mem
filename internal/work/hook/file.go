package hook

import (
	"io"
	"os"
)

// readTail reads at most n bytes from the end of a file.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > n {
		if _, err := f.Seek(st.Size()-n, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, n))
}
