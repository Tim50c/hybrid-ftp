package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sha256Hex returns the lowercase hex SHA-256 of b (matches the server format).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}

// ---------------------------------------------------------------------------
// parseHASHReply
// ---------------------------------------------------------------------------

func TestParseHASHReply_Valid(t *testing.T) {
	reply := "213 SHA-256 0-5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03 hello.txt"
	got := parseHASHReply(reply)
	want := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got != want {
		t.Errorf("parseHASHReply(%q) = %q, want %q", reply, got, want)
	}
}

func TestParseHASHReply_FilenameWithSpaces(t *testing.T) {
	reply := "213 SHA-256 0-d24065ab9b333e5fed1206ea898f5d2f3c44e3ecdd6c1214a0537ebb42964bc1 my file.txt"
	got := parseHASHReply(reply)
	want := "d24065ab9b333e5fed1206ea898f5d2f3c44e3ecdd6c1214a0537ebb42964bc1"
	if got != want {
		t.Errorf("parseHASHReply(%q) = %q, want %q", reply, got, want)
	}
}

func TestParseHASHReply_UppercaseHex(t *testing.T) {
	reply := "213 SHA-256 0-5891B5B522D5DF086D0FF0B110FBD9D21BB4FC7163AF34D08286A2E846F6BE03 hello.txt"
	got := parseHASHReply(reply)
	want := "5891B5B522D5DF086D0FF0B110FBD9D21BB4FC7163AF34D08286A2E846F6BE03"
	if got != want {
		t.Errorf("parseHASHReply(%q) = %q, want %q", reply, got, want)
	}
}

func TestParseHASHReply_NoFilename(t *testing.T) {
	reply := "213 SHA-256 0-5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	got := parseHASHReply(reply)
	want := "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got != want {
		t.Errorf("parseHASHReply(%q) = %q, want %q", reply, got, want)
	}
}

func TestParseHASHReply_NoPrefix(t *testing.T) {
	reply := "213 5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got := parseHASHReply(reply); got != "" {
		t.Errorf("expected empty for missing prefix, got %q", got)
	}
}

func TestParseHASHReply_ShortHash(t *testing.T) {
	reply := "213 SHA-256 0-deadbeef hello.txt"
	if got := parseHASHReply(reply); got != "" {
		t.Errorf("expected empty for too-short hash, got %q", got)
	}
}

func TestParseHASHReply_NonHex(t *testing.T) {
	reply := "213 SHA-256 0-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz hello.txt"
	if got := parseHASHReply(reply); got != "" {
		t.Errorf("expected empty for non-hex, got %q", got)
	}
}

func TestParseHASHReply_EmptyString(t *testing.T) {
	if got := parseHASHReply(""); got != "" {
		t.Errorf("expected empty for empty input, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// evalHASHReply  — this is the function the original bug regressed.
// A hard test for the *behaviour the user sees* (match / mismatch / parse err).
// ---------------------------------------------------------------------------

func TestEvalHASHReply_Match(t *testing.T) {
	h := sha256Hex([]byte("hello world"))
	reply := "213 SHA-256 0-" + h + " file.txt"
	got := evalHASHReply(h, reply)
	want := "Hashes match - file integrity verified."
	if got != want {
		t.Errorf("evalHASHReply match: got %q, want %q", got, want)
	}
}

// The original bug: client took the whole "SHA-256 0-<hash> file.txt" string
// as the remote hash, so the comparison always failed and the user saw
// MISMATCH even when the file was identical. This test would fail on the
// pre-fix code.
func TestEvalHASHReply_Match_BugRegression(t *testing.T) {
	content := []byte("regression payload for HASH match")
	h := sha256Hex(content)
	reply := "213 SHA-256 0-" + h + " payload.bin"
	got := evalHASHReply(h, reply)
	if !strings.Contains(got, "Hashes match") {
		t.Errorf("expected match message, got %q", got)
	}
	if strings.Contains(got, "MISMATCH") {
		t.Errorf("MISMATCH should not appear for matching hashes, got %q", got)
	}
}

func TestEvalHASHReply_Match_CaseInsensitive(t *testing.T) {
	local := sha256Hex([]byte("x"))
	upperReply := "213 SHA-256 0-" + strings.ToUpper(local) + " x"
	if got := evalHASHReply(local, upperReply); got != "Hashes match - file integrity verified." {
		t.Errorf("case-insensitive match failed: got %q", got)
	}
}

func TestEvalHASHReply_Mismatch(t *testing.T) {
	local := sha256Hex([]byte("local content"))
	remote := sha256Hex([]byte("different content"))
	reply := "213 SHA-256 0-" + remote + " file.txt"
	got := evalHASHReply(local, reply)
	if !strings.Contains(got, "MISMATCH!") {
		t.Errorf("expected MISMATCH! in output, got %q", got)
	}
	if !strings.Contains(got, local) {
		t.Errorf("MISMATCH output should contain local hash, got %q", got)
	}
	if !strings.Contains(got, remote) {
		t.Errorf("MISMATCH output should contain remote hash, got %q", got)
	}
}

func TestEvalHASHReply_ParseError(t *testing.T) {
	local := sha256Hex([]byte("x"))
	reply := "213 this is not a SHA-256 reply at all"
	got := evalHASHReply(local, reply)
	if got != "HASH: could not parse server reply." {
		t.Errorf("expected parse-error message, got %q", got)
	}
}

func TestEvalHASHReply_ShortHashIsParseError(t *testing.T) {
	local := sha256Hex([]byte("x"))
	reply := "213 SHA-256 0-deadbeef file.txt"
	got := evalHASHReply(local, reply)
	if got != "HASH: could not parse server reply." {
		t.Errorf("expected parse-error message for short hash, got %q", got)
	}
}

func TestEvalHASHReply_NotA213Reply(t *testing.T) {
	// 550 file not found, etc. — should produce no message.
	cases := []string{
		"550 Cannot compute hash.",
		"501 HASH requires a filename.",
		"530 Not logged in.",
		"",
	}
	for _, reply := range cases {
		if got := evalHASHReply("anything", reply); got != "" {
			t.Errorf("evalHASHReply(%q) = %q, want \"\"", reply, got)
		}
	}
}

// ---------------------------------------------------------------------------
// handleHASH end-to-end with an in-memory net.Conn (no real network).
// Captures stdout to verify the user-visible message.
// ---------------------------------------------------------------------------

// pipeConn is an in-memory net.Conn. It is a *strict* subset of net.Conn: the
// only methods the production code uses (Read, Write, Close, LocalAddr,
// RemoteAddr, SetDeadline/Read/Write) are satisfied. Any other method panics.
type pipeConn struct {
	readBuf  *bytes.Buffer
	writeBuf bytes.Buffer
}

func (p *pipeConn) Read(b []byte) (int, error)         { return p.readBuf.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error)        { return p.writeBuf.Write(b) }
func (p *pipeConn) Close() error                       { return nil }
func (p *pipeConn) LocalAddr() net.Addr                { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (p *pipeConn) RemoteAddr() net.Addr               { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2} }
func (p *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return nil }

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was printed. Restores the original stdout even if fn panics.
//
// Implementation note: we must wait for the reader goroutine to drain the
// pipe (and finish its io.Copy into the buffer) BEFORE reading buf.String(),
// otherwise the first run can lose data while the goroutine is still in
// flight. The goroutine only exits when the writer end is closed AND all
// buffered bytes have been read, so we close w and then block on done.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	defer func() {
		os.Stdout = old
	}()
	fn()
	w.Close()
	<-done
	return buf.String()
}

func TestHandleHASH_MatchEndToEnd(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	name := "match.txt"
	content := []byte("Hello, World!\n")
	if err := os.WriteFile(filepath.Join(dir, name), content, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := sha256Hex(content)
	reply := "213 SHA-256 0-" + h + " " + name + "\r\n"

	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleHASH(conn, name)
	})

	// 1. HASH command was sent
	if !strings.Contains(conn.writeBuf.String(), "HASH "+name+"\r\n") {
		t.Errorf("expected HASH command to be written, got %q", conn.writeBuf.String())
	}
	// 2. The server's 213 reply was echoed
	if !strings.Contains(out, reply[:len(reply)-2]) {
		t.Errorf("expected echo of server reply, got %q", out)
	}
	// 3. The match line was printed (THE bug regression check)
	if !strings.Contains(out, "Hashes match - file integrity verified.") {
		t.Errorf("expected 'Hashes match' line, got:\n%s", out)
	}
	// 4. No MISMATCH for matching hashes
	if strings.Contains(out, "MISMATCH!") {
		t.Errorf("unexpected MISMATCH for matching hashes, got:\n%s", out)
	}
}

func TestHandleHASH_MismatchEndToEnd(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	name := "mismatch.txt"
	localContent := []byte("local")
	if err := os.WriteFile(filepath.Join(dir, name), localContent, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	remoteContent := []byte("remote")
	remoteHash := sha256Hex(remoteContent)
	reply := "213 SHA-256 0-" + remoteHash + " " + name + "\r\n"

	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleHASH(conn, name)
	})

	if !strings.Contains(out, "MISMATCH!") {
		t.Errorf("expected MISMATCH! line, got:\n%s", out)
	}
	if !strings.Contains(out, sha256Hex(localContent)) {
		t.Errorf("expected local hash in output, got:\n%s", out)
	}
	if !strings.Contains(out, remoteHash) {
		t.Errorf("expected remote hash in output, got:\n%s", out)
	}
}

func TestHandleHASH_ParseErrorEndToEnd(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	name := "parse_err.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := "213 this is garbage, not a hash reply\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleHASH(conn, name)
	})

	if !strings.Contains(out, "HASH: could not parse server reply.") {
		t.Errorf("expected parse-error line, got:\n%s", out)
	}
	if strings.Contains(out, "MISMATCH!") {
		t.Errorf("parse error should not be reported as MISMATCH, got:\n%s", out)
	}
	if strings.Contains(out, "Hashes match") {
		t.Errorf("parse error should not be reported as match, got:\n%s", out)
	}
}

func TestHandleHASH_MissingLocalFile(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	conn := &pipeConn{readBuf: bytes.NewBufferString("")}
	out := captureStdout(t, func() {
		handleHASH(conn, "no_such_file.xyz")
	})
	if !strings.Contains(out, "HASH:") {
		t.Errorf("expected HASH: error prefix, got %q", out)
	}
	// Should NOT have written the HASH command to the conn.
	if strings.Contains(conn.writeBuf.String(), "HASH ") {
		t.Errorf("HASH command should not be sent when local file is missing, got %q", conn.writeBuf.String())
	}
}

func TestHandleHASH_Non213Reply(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	name := "ok.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reply := "550 Cannot compute hash.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleHASH(conn, name)
	})
	if strings.Contains(out, "MISMATCH!") || strings.Contains(out, "Hashes match") || strings.Contains(out, "could not parse") {
		t.Errorf("non-213 reply should produce no result line, got:\n%s", out)
	}
}

// withCwd changes the current working directory for the duration of the test
// and restores it on cleanup. The real client relies on cwd (after `lcd`),
// so the test mirrors that contract.
func withCwd(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Logf("warning: chdir restore: %v", err)
		}
	})
}

func TestHandleNLST_WritesCommand(t *testing.T) {
	reply := "150 Opening data connection for name list.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleNLST(conn, "")
	})

	if !strings.Contains(conn.writeBuf.String(), "NLST\r\n") {
		t.Errorf("expected NLST command, got %q", conn.writeBuf.String())
	}
	if !strings.Contains(out, "150 Opening data connection") {
		t.Errorf("expected 150 reply echo, got %q", out)
	}
}

func TestHandleNLST_WritesCommandWithPath(t *testing.T) {
	reply := "150 Opening data connection for name list.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	_ = captureStdout(t, func() {
		handleNLST(conn, "subdir")
	})

	if !strings.Contains(conn.writeBuf.String(), "NLST subdir\r\n") {
		t.Errorf("expected NLST subdir command, got %q", conn.writeBuf.String())
	}
}

func TestHandleNLST_Non150Reply(t *testing.T) {
	reply := "550 Cannot list directory.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleNLST(conn, "")
	})

	if !strings.Contains(out, "550 Cannot list directory") {
		t.Errorf("expected 550 reply echo, got %q", out)
	}
}

func TestHandleSTOU_EmptyFilename(t *testing.T) {
	conn := &pipeConn{readBuf: bytes.NewBufferString("")}
	out := captureStdout(t, func() {
		handleSTOU(conn, "")
	})

	if !strings.Contains(out, "STOU requires a local filename") {
		t.Errorf("expected missing-filename error, got %q", out)
	}
	if strings.Contains(conn.writeBuf.String(), "STOU") {
		t.Errorf("no command should be written for empty filename, got %q", conn.writeBuf.String())
	}
}

func TestHandleSTOU_WritesCommand(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	filename := "upload.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("data"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reply := "150 Opening data connection for stou_test.dat.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleSTOU(conn, filename)
	})

	if !strings.Contains(conn.writeBuf.String(), "STOU "+filename+"\r\n") {
		t.Errorf("expected STOU %s command, got %q", filename, conn.writeBuf.String())
	}
	if !strings.Contains(out, "150 Opening data connection") {
		t.Errorf("expected 150 reply echo, got %q", out)
	}
}

func TestHandleSTOU_MissingLocalFile(t *testing.T) {
	reply := "150 Opening data connection for stou_test.dat.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleSTOU(conn, "nonexistent.txt")
	})

	if !strings.Contains(out, "Cannot open local file") {
		t.Errorf("expected open error, got %q", out)
	}
}

func TestHandleSTOU_Non150Reply(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	filename := "upload.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("data"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reply := "425 Cannot open data connection.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleSTOU(conn, filename)
	})

	if !strings.Contains(out, "425 Cannot open data connection") {
		t.Errorf("expected 425 reply echo, got %q", out)
	}
}

func TestHandleAPPE_WritesCommand(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	filename := "append.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("data"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reply := "150 Opening data connection for append.txt.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleAPPE(conn, filename)
	})

	if !strings.Contains(conn.writeBuf.String(), "APPE "+filename+"\r\n") {
		t.Errorf("expected APPE %s command, got %q", filename, conn.writeBuf.String())
	}
	if !strings.Contains(out, "150 Opening data connection") {
		t.Errorf("expected 150 reply echo, got %q", out)
	}
}

func TestHandleAPPE_EmptyFilename(t *testing.T) {
	reply := "501 APPE requires a filename.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleAPPE(conn, "")
	})

	if !strings.Contains(conn.writeBuf.String(), "APPE\r\n") {
		t.Errorf("expected APPE command for empty filename, got %q", conn.writeBuf.String())
	}
	if !strings.Contains(out, "501 APPE requires a filename") {
		t.Errorf("expected 501 reply echo, got %q", out)
	}
}

func TestHandleAPPE_MissingLocalFile(t *testing.T) {
	reply := "150 Opening data connection for nonexistent.txt.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleAPPE(conn, "nonexistent.txt")
	})

	if !strings.Contains(out, "Cannot open local file") {
		t.Errorf("expected open error, got %q", out)
	}
}

func TestHandleAPPE_Non150Reply(t *testing.T) {
	dir := t.TempDir()
	withCwd(t, dir)
	filename := "append.txt"
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("data"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	reply := "503 Use PASV or PORT first.\r\n"
	conn := &pipeConn{readBuf: bytes.NewBufferString(reply)}
	out := captureStdout(t, func() {
		handleAPPE(conn, filename)
	})

	if !strings.Contains(out, "503 Use PASV or PORT first") {
		t.Errorf("expected 503 reply echo, got %q", out)
	}
}
