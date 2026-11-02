package logger

import (
	"errors"
	"syscall"
)

// kmsgRecords reads every record of /dev/kmsg that is in the buffer now,
// through raw non-blocking reads (an os.File would park on EAGAIN).
func kmsgRecords(path string) ([]string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	var out []string
	buf := make([]byte, 8192)
	for i := 0; i < 1<<20; i++ {
		n, err := syscall.Read(fd, buf)
		switch {
		case errors.Is(err, syscall.EPIPE):
			continue // record overwritten while reading: skip
		case err != nil || n <= 0:
			return out, nil // EAGAIN: end of the buffer
		}
		out = append(out, string(buf[:n]))
	}
	return out, nil
}
