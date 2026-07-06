package resp

import (
	"bufio"
	"io"
	"strconv"
)

// Writer serialises RESP2 replies to a buffered stream. Call Flush to ensure
// the bytes reach the underlying connection.
type Writer struct {
	w *bufio.Writer
}

// NewWriter wraps wr in a RESP2 writer.
func NewWriter(wr io.Writer) *Writer {
	return &Writer{w: bufio.NewWriter(wr)}
}

// Flush writes any buffered data to the underlying connection.
func (w *Writer) Flush() error { return w.w.Flush() }

// WriteSimpleString writes "+<s>\r\n" (e.g. +OK).
func (w *Writer) WriteSimpleString(s string) error {
	w.w.WriteByte('+')
	w.w.WriteString(s)
	return w.crlf()
}

// WriteError writes "-<msg>\r\n". The msg should begin with an error code
// such as "ERR" or "WRONGTYPE".
func (w *Writer) WriteError(msg string) error {
	w.w.WriteByte('-')
	w.w.WriteString(msg)
	return w.crlf()
}

// WriteInteger writes ":<n>\r\n".
func (w *Writer) WriteInteger(n int64) error {
	w.w.WriteByte(':')
	w.w.WriteString(strconv.FormatInt(n, 10))
	return w.crlf()
}

// WriteBulkString writes a bulk string "$<len>\r\n<s>\r\n".
func (w *Writer) WriteBulkString(s string) error {
	w.w.WriteByte('$')
	w.w.WriteString(strconv.Itoa(len(s)))
	w.crlf()
	w.w.WriteString(s)
	return w.crlf()
}

// WriteNull writes a RESP2 null bulk string "$-1\r\n".
func (w *Writer) WriteNull() error {
	_, err := w.w.WriteString("$-1\r\n")
	return err
}

// WriteNullArray writes a RESP2 null array "*-1\r\n".
func (w *Writer) WriteNullArray() error {
	_, err := w.w.WriteString("*-1\r\n")
	return err
}

// WriteArrayHeader writes "*<n>\r\n"; the caller then writes n elements.
func (w *Writer) WriteArrayHeader(n int) error {
	w.w.WriteByte('*')
	w.w.WriteString(strconv.Itoa(n))
	return w.crlf()
}

// WriteStringArray writes an array of bulk strings.
func (w *Writer) WriteStringArray(items []string) error {
	if err := w.WriteArrayHeader(len(items)); err != nil {
		return err
	}
	for _, it := range items {
		if err := w.WriteBulkString(it); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) crlf() error {
	_, err := w.w.WriteString("\r\n")
	return err
}
