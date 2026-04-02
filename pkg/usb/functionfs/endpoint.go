package functionfs

import (
	"fmt"
	"os"
	"path/filepath"
)

type Endpoint struct {
	file *os.File
	path string
}

func OpenEndpoint(mountPath string, num int, out bool) (*Endpoint, error) {
	name := fmt.Sprintf("ep%d", num)
	path := filepath.Join(mountPath, name)
	mode := os.O_RDWR
	if out {
		mode = os.O_RDONLY
	} else {
		mode = os.O_WRONLY
	}

	f, err := os.OpenFile(path, mode, 0)
	if err != nil {
		return nil, err
	}
	return &Endpoint{file: f, path: path}, nil
}

func (e *Endpoint) Close() error {
	return e.file.Close()
}

func (e *Endpoint) Read(buf []byte) (int, error) {
	return e.file.Read(buf)
}

func (e *Endpoint) Write(buf []byte) (int, error) {
	return e.file.Write(buf)
}
