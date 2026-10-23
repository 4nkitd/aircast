package casting

import (
	"errors"
	"os"
)

func startChild(string, *os.File, *os.File, *os.File) (childProcess, error) {
	return nil, errors.New("safe casting engine process-group supervision is unavailable on macOS")
}

func lockProcessThread() func() { return func() {} }
