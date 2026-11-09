package bench

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/crispebble/orbit-mem/internal/sqlitedb"
)

// copyTries is how many times CopyDB copies the files before it gives up on a DB that keeps
// changing under it.
const copyTries = 5

// retryWait is how long stableCopy waits after a failed try before it copies again, so that a
// burst of writes can end (tests make it short).
var retryWait = defaultRetryWait

// defaultRetryWait grows with the try (20 ms, 40 ms, ...) up to 100 ms.
func defaultRetryWait(try int) time.Duration {
	return min(time.Duration(try)*20*time.Millisecond, 100*time.Millisecond)
}

// copiedSuffixes are the files of a DB that hold its data. The -shm is not copied: it is only
// an index of the -wal that readers write to as well, and the first connection to the copy
// rebuilds it from the -wal.
var copiedSuffixes = []string{"", "-wal", "-journal"}

// afterCopy runs between copying the files and checking them again (tests change the source
// there to make a copy race).
var afterCopy func(try int)

// ErrCopyBusy is returned when the DB changed during every try of a copy.
var ErrCopyBusy = errors.New("the DB kept changing while it was copied: try again when it is quiet")

// CopyDB writes a copy of the DB at src to dst (which must not exist) with everything that is in
// its -wal too, and never opens src: the DB file, its -wal and its -journal (only those that
// exist) are copied byte for byte into a private temporary folder, checked to be unchanged after
// the copy (a write in between could pair an old DB file with a newer -wal; then the copy is
// made again), and that private copy is the one SQLite opens and writes into dst with VACUUM
// INTO. So no file is made, opened for writing or locked next to src, whether or not a
// connection to it is open.
func CopyDB(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "orbit-bench-copy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	raw := filepath.Join(dir, "raw.db")
	if err := stableCopy(src, raw); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sqlitedb.FileURI(raw)+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("copy the DB: %w", err)
	}
	return os.Chmod(dst, 0o600)
}

// stableCopy copies the data files of src to dst until the source files read the same after
// the copy as the bytes that were copied.
func stableCopy(src, dst string) error {
	for try := 1; try <= copyTries; try++ {
		if err := copyFiles(src, dst); err != nil {
			return err
		}
		if afterCopy != nil {
			afterCopy(try)
		}
		same, err := sameFiles(src, dst)
		if err != nil || same {
			return err
		}
		if try < copyTries {
			time.Sleep(retryWait(try))
		}
	}
	return ErrCopyBusy
}

// copyFiles copies every data file of src that exists.
func copyFiles(src, dst string) error {
	for _, suffix := range copiedSuffixes {
		if err := os.Remove(dst + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		in, err := os.Open(src + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = writeFrom(in, dst+suffix)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFrom(in io.Reader, dst string) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// sameFiles reports whether every data file of src exists exactly when its copy does and holds
// the same bytes.
func sameFiles(src, dst string) (bool, error) {
	for _, suffix := range copiedSuffixes {
		a, aerr := os.Open(src + suffix)
		b, berr := os.Open(dst + suffix)
		same, err := sameContent(a, aerr, b, berr)
		if a != nil {
			a.Close()
		}
		if b != nil {
			b.Close()
		}
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

func sameContent(a *os.File, aerr error, b *os.File, berr error) (bool, error) {
	switch aMissing, bMissing := errors.Is(aerr, os.ErrNotExist), errors.Is(berr, os.ErrNotExist); {
	case aMissing || bMissing:
		return aMissing && bMissing, nil
	case aerr != nil:
		return false, aerr
	case berr != nil:
		return false, berr
	}
	bufA, bufB := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(a, bufA)
		nb, eb := io.ReadFull(b, bufB)
		if !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		aEnd := ea == io.EOF || ea == io.ErrUnexpectedEOF
		bEnd := eb == io.EOF || eb == io.ErrUnexpectedEOF
		switch {
		case ea != nil && !aEnd:
			return false, ea
		case eb != nil && !bEnd:
			return false, eb
		case aEnd || bEnd:
			return aEnd && bEnd, nil
		}
	}
}
