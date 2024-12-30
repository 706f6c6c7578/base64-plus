package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	bufferSize = 4 * 1024 * 1024 // 4MB buffer
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, bufferSize)
	},
}

type fastLineWriter struct {
	w    *bufio.Writer
	buf  []byte
	pos  int
	size int
}

func newFastLineWriter(w io.Writer, size int) *fastLineWriter {
	return &fastLineWriter{
		w:    bufio.NewWriterSize(w, bufferSize),
		buf:  make([]byte, size+1),
		size: size,
	}
}

func (w *fastLineWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	for _, b := range p {
		w.buf[w.pos] = b
		w.pos++
		if w.pos == w.size {
			w.buf[w.pos] = '\n'
			if _, err = w.w.Write(w.buf[:w.pos+1]); err != nil {
				return
			}
			w.pos = 0
		}
	}
	return
}

func (w *fastLineWriter) Flush() error {
	if w.pos > 0 {
		w.buf[w.pos] = '\n'
		if _, err := w.w.Write(w.buf[:w.pos+1]); err != nil {
			return err
		}
	}
	return w.w.Flush()
}

func encodeFast(input io.Reader, output io.Writer, filename string) error {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	h := sha256.New()
	size := 0

	for {
		n, err := input.Read(buf)
		if n > 0 {
			size += n
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	// Write header information vertically with a blank line after
	fmt.Fprintf(output, "%s\n%d\n%x\n\n", filename, size, h.Sum(nil))

	if f, ok := input.(*os.File); ok {
		f.Seek(0, 0)
	}

	w := newFastLineWriter(output, 64)
	encoder := base64.NewEncoder(base64.StdEncoding, w)

	for {
		n, err := input.Read(buf)
		if n > 0 {
			if _, err := encoder.Write(buf[:n]); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	encoder.Close()
	return w.Flush()
}

func decodeFast(input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)

	// Read header information line by line
	filename, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	filename = strings.TrimSpace(filename)

	sizeStr, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	sizeStr = strings.TrimSpace(sizeStr)

	originalHash, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	originalHash = strings.TrimSpace(originalHash)

	// Read the blank line
	_, err = reader.ReadString('\n')
	if err != nil {
		return err
	}

	// Create output file
	outFile, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer outFile.Close()

	// Setup buffered writer and hasher
	bufWriter := bufio.NewWriterSize(outFile, bufferSize)
	h := sha256.New()
	mw := io.MultiWriter(bufWriter, h)

	// Setup efficient base64 decoder
	decoder := base64.NewDecoder(base64.StdEncoding, &base64Reader{r: reader})
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	// Copy data with buffering
	_, err = io.CopyBuffer(mw, decoder, buf)
	if err != nil {
		return err
	}

	// Flush buffer
	if err := bufWriter.Flush(); err != nil {
		return err
	}

	// Output verification info
	fmt.Fprintf(os.Stderr, "Original size: %s bytes\n", sizeStr)
	fmt.Fprintf(os.Stderr, "SHA256: %x\n", h.Sum(nil))
	fmt.Fprintf(os.Stderr, "Matches original: %v\n", fmt.Sprintf("%x", h.Sum(nil)) == originalHash)

	return nil
}

type base64Reader struct {
	r   *bufio.Reader
	buf []byte
}

func (r *base64Reader) Read(p []byte) (n int, err error) {
	if r.buf == nil {
		r.buf = make([]byte, 8*1024) // 8KB read buffer
	}

	// Read until buffer is full or EOF
	var total int
	for total < len(p) {
		line, err := r.r.ReadSlice('\n')
		if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
			return total, err
		}

		// Remove newline
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}

		// Copy without newlines
		copy(p[total:], line)
		total += len(line)

		if err == io.EOF {
			if total == 0 {
				return 0, io.EOF
			}
			break
		}
	}
	return total, nil
}

func encodeLegacyFast(input io.Reader, output io.Writer) error {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	w := newFastLineWriter(output, 64)
	encoder := base64.NewEncoder(base64.StdEncoding, w)

	_, err := io.CopyBuffer(encoder, input, buf)
	if err != nil {
		return err
	}

	encoder.Close()
	return w.Flush()
}

func decodeLegacyFast(input io.Reader, output io.Writer) error {
	decoder := base64.NewDecoder(base64.StdEncoding, &base64Reader{r: bufio.NewReader(input)})
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	_, err := io.CopyBuffer(output, decoder, buf)
	return err
}

func main() {
	decodeFlag := flag.Bool("d", false, "decode mode")
	legacyFlag := flag.Bool("l", false, "legacy mode (no headers)")
	flag.Parse()

	if *decodeFlag {
		if *legacyFlag {
			if err := decodeLegacyFast(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "Error decoding: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := decodeFast(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "Error decoding: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	if *legacyFlag {
		if err := encodeLegacyFast(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
			os.Exit(1)
		}
		return
	}

	filename := getInputFilename()
	if args := flag.Args(); len(args) > 0 {
		filename = filepath.Base(args[0])
	}

	if err := encodeFast(os.Stdin, os.Stdout, filename); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
		os.Exit(1)
	}
}

func getInputFilename() string {
	if stat, err := os.Stdin.Stat(); err == nil {
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			if realPath, err := os.Readlink("/proc/self/fd/0"); err == nil {
				return filepath.Base(realPath)
			}
		}
	}
	return "stdin"
}
