package message

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// headerField is one header line, kept in original order so the message can
// be reassembled deterministically after selective rewriting.
type headerField struct {
	Key   string
	Value string
}

// splitMessage splits a raw RFC 5322 message into its header block and body
// on the first blank line. Supports CRLF and bare-LF line endings, since not
// every SMTP client is strictly correct about line terminators.
func splitMessage(raw []byte) (headerBlock, body []byte) {
	for _, sep := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
		if idx := bytes.Index(raw, sep); idx != -1 {
			return raw[:idx], raw[idx+len(sep):]
		}
	}
	// No blank-line separator found: treat the whole payload as headers with
	// an empty body rather than guessing.
	return raw, nil
}

// parseHeaderFields parses a header block into an ordered list of fields,
// unfolding RFC 5322 continuation lines (lines starting with whitespace)
// into their parent field as a single logical line.
func parseHeaderFields(block []byte) ([]headerField, error) {
	var fields []headerField
	scanner := bufio.NewScanner(bytes.NewReader(block))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(fields) > 0 {
			fields[len(fields)-1].Value += " " + strings.TrimSpace(line)
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			return nil, fmt.Errorf("malformed header line: %q", line)
		}
		fields = append(fields, headerField{
			Key:   line[:idx],
			Value: strings.TrimSpace(line[idx+1:]),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return fields, nil
}

// serializeHeaderFields writes fields back out as CRLF-terminated header
// lines, in the same order they appear in the slice.
func serializeHeaderFields(fields []headerField) []byte {
	var buf bytes.Buffer
	for _, f := range fields {
		buf.WriteString(f.Key)
		buf.WriteString(": ")
		buf.WriteString(f.Value)
		buf.WriteString("\r\n")
	}
	return buf.Bytes()
}

func findField(fields []headerField, key string) int {
	for i, f := range fields {
		if strings.EqualFold(f.Key, key) {
			return i
		}
	}
	return -1
}

func removeFields(fields []headerField, key string) []headerField {
	out := make([]headerField, 0, len(fields))
	for _, f := range fields {
		if !strings.EqualFold(f.Key, key) {
			out = append(out, f)
		}
	}
	return out
}
