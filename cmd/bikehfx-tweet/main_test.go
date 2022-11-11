package main

import (
	"io"
	"os"
)

func writeFileFromReader(file string, r io.Reader) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return err
	}

	return f.Close()
}
