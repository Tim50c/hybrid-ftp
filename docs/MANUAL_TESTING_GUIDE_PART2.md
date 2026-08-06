# Part 2 — Manual Testing with test/file_tests/ Files

How to test Hybrid FTP using the real-world sample files in `test/file_tests/`.
Covers PASV downloads, PORT downloads, STOR upload, HASH verification, and
concurrency — all validated with SHA-256 hash comparison against the originals.

---

## 0. Setup

### Kill any stale server

```bash
fuser -k 2130/tcp 2>/dev/null
sleep 1
```

### Build

```bash
cd hybrid-ftp
go build ./cmd/server
go build ./cmd/client
```

### Start Server

```bash
ROOT=/tmp/hybrid-ftp-filetest

rm -rf $ROOT
mkdir -p $ROOT

cp test/file_tests/10mb-example-jpg.jpg $ROOT/
cp test/file_tests/book.pdf $ROOT/
cp test/file_tests/pexels-simplyart-9020071.mp4 $ROOT/
cp test/file_tests/viber.deb $ROOT/
cp test/file_tests/window.zip $ROOT/

ls -la $ROOT

./server -ctrl :2130 -root $ROOT -users users.conf
```

Expected server output:
```
[CTRL] server listening on :2130
```

Leave this terminal running and open a second terminal for the client.

### The Test Files

| File | Size |
|------|------|
| `10mb-example-jpg.jpg` | 10,809,064 |
| `book.pdf` | 22,254,526 |
| `pexels-simplyart-9020071.mp4` | 16,887,888 |
| `viber.deb` | 110,082,688 |
| `window.zip` | 27,921,455 |

### Compute Expected Hashes

```bash
sha256sum test/file_tests/10mb-example-jpg.jpg
sha256sum test/file_tests/book.pdf
sha256sum test/file_tests/pexels-simplyart-9020071.mp4
sha256sum test/file_tests/viber.deb
sha256sum test/file_tests/window.zip
```

Save these hashes — they are the ground truth for all verification steps below.

---

## 1. PASV Download — All Five Files

### Manual Steps

For each file, run the same procedure in the client terminal:

```bash
./client -host 127.0.0.1 -ctrl 2130
```

```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> RETR <filename>
       Wait for progress bar and "226 Transfer complete."
ftp> HASH <filename>
       Should print "Hashes match - file integrity verified."
ftp> QUIT
```

Then verify locally:

```bash
sha256sum /tmp/<filename>
```

Compare with the hash you computed above. Must match exactly.

### Automated Script

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-filetest
CTRL=2130
DL=/tmp/hybrid-ftp-dl

rm -rf "$ROOT" "$DL"
mkdir -p "$ROOT" "$DL"
cp "$HYBRID_DIR/test/file_tests/10mb-example-jpg.jpg" "$ROOT/"
cp "$HYBRID_DIR/test/file_tests/book.pdf" "$ROOT/"
cp "$HYBRID_DIR/test/file_tests/pexels-simplyart-9020071.mp4" "$ROOT/"
cp "$HYBRID_DIR/test/file_tests/viber.deb" "$ROOT/"
cp "$HYBRID_DIR/test/file_tests/window.zip" "$ROOT/"

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/ft.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

PASSED=0
FAILED=0

download_and_check() {
    local base="$1"
    local local_hash="$2"

    echo "--- $base ---"

    {
        echo "USER admin"
        echo "PASS admin"
        echo "lcd $DL"
        echo "PASV"
        echo "RETR $base"
        sleep 3
        echo "HASH $base"
        sleep 1
        echo "QUIT"
    } | timeout 180 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > "/tmp/out_${base}.txt" 2>&1

    local dl_file="$DL/$base"
    if [ -f "$dl_file" ]; then
        local dl_hash
        dl_hash=$(sha256sum "$dl_file" | awk '{print $1}')
        if [ "$dl_hash" = "$local_hash" ]; then
            echo "  PASS: hash match ($(stat -c%s "$dl_file") bytes)"
            PASSED=$((PASSED+1))
        else
            echo "  FAIL: hash mismatch"
            echo "    local:  $local_hash"
            echo "    dl:     $dl_hash"
            FAILED=$((FAILED+1))
        fi
    else
        echo "  FAIL: file not downloaded"
        FAILED=$((FAILED+1))
    fi
    rm -f "$dl_file"
}

download_and_check "10mb-example-jpg.jpg" \
    "$(sha256sum "$HYBRID_DIR/test/file_tests/10mb-example-jpg.jpg" | awk '{print $1}')"

download_and_check "book.pdf" \
    "$(sha256sum "$HYBRID_DIR/test/file_tests/book.pdf" | awk '{print $1}')"

download_and_check "pexels-simplyart-9020071.mp4" \
    "$(sha256sum "$HYBRID_DIR/test/file_tests/pexels-simplyart-9020071.mp4" | awk '{print $1}')"

download_and_check "viber.deb" \
    "$(sha256sum "$HYBRID_DIR/test/file_tests/viber.deb" | awk '{print $1}')"

download_and_check "window.zip" \
    "$(sha256sum "$HYBRID_DIR/test/file_tests/window.zip" | awk '{print $1}')"

echo ""
echo "PASV download results: $((PASSED+FAILED)) files, $PASSED passed, $FAILED failed"
```

Save as `test_pasv.sh`, `chmod +x test_pasv.sh`, then run:

```bash
./test_pasv.sh
```

Expected output:
```
--- 10mb-example-jpg.jpg ---
  PASS: hash match (10809064 bytes)
--- book.pdf ---
  PASS: hash match (22254526 bytes)
...
PASV download results: 5 files, 5 passed, 0 failed
```

---

## 2. PORT Mode Download — book.pdf

### Manual Steps

```bash
./client -host 127.0.0.1 -ctrl 2130
```

```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PORT 127,0,0,1,200,3
ftp> RETR book.pdf
       Wait for progress bar and "226 Transfer complete."
ftp> HASH book.pdf
       Should print "Hashes match - file integrity verified."
ftp> QUIT
```

```bash
sha256sum /tmp/book.pdf
sha256sum test/file_tests/book.pdf
```

Both hashes must match.

### Automated Script

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-port
CTRL=2130
DL=/tmp/hybrid-ftp-port-dl

rm -rf "$ROOT" "$DL"
mkdir -p "$ROOT" "$DL"
cp "$HYBRID_DIR/test/file_tests/book.pdf" "$ROOT/"

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/port.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

LOCAL_HASH=$(sha256sum "$HYBRID_DIR/test/file_tests/book.pdf" | awk '{print $1}')

cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/port_out.txt 2>&1
USER admin
PASS admin
lcd $DL
PORT 127,0,0,1,200,3
RETR book.pdf
HASH book.pdf
QUIT
EOF

if [ -f "$DL/book.pdf" ]; then
    DL_HASH=$(sha256sum "$DL/book.pdf" | awk '{print $1}')
    if [ "$DL_HASH" = "$LOCAL_HASH" ]; then
        echo "PASS: PORT download - hash match ($(stat -c%s "$DL/book.pdf") bytes)"
    else
        echo "FAIL: PORT download - hash mismatch"
    fi
else
    echo "FAIL: PORT download - no file"
fi
```

---

## 3. STOR Upload — Upload a test/file_tests/ File

### Manual Steps

```bash
cp test/file_tests/10mb-example-jpg.jpg /tmp/upload_test.jpg
```

```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> STOR upload_test.jpg
       Wait for progress bar and "226 Transfer complete."
ftp> HASH upload_test.jpg
       Should print "Hashes match - file integrity verified."
ftp> QUIT
```

```bash
sha256sum $ROOT/upload_test.jpg
sha256sum test/file_tests/10mb-example-jpg.jpg
```

Both hashes must match.

### Automated Script

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-stor
CTRL=2130
TMP=/tmp/hybrid-ftp-stor-tmp

rm -rf "$ROOT" "$TMP"
mkdir -p "$ROOT" "$TMP"
cp "$HYBRID_DIR/test/file_tests/10mb-example-jpg.jpg" "$TMP/upload.jpg"

LOCAL_HASH=$(sha256sum "$TMP/upload.jpg" | awk '{print $1}')

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/stor.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $TMP" EXIT
sleep 1

cat <<EOF | timeout 120 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/stor_out.txt 2>&1
USER admin
PASS admin
lcd $TMP
PASV
STOR upload.jpg
HASH upload.jpg
QUIT
EOF

if [ -f "$ROOT/upload.jpg" ]; then
    SERVER_HASH=$(sha256sum "$ROOT/upload.jpg" | awk '{print $1}')
    if [ "$SERVER_HASH" = "$LOCAL_HASH" ]; then
        echo "PASS: STOR upload - hash match ($(stat -c%s "$ROOT/upload.jpg") bytes)"
    else
        echo "FAIL: STOR upload - hash mismatch"
    fi
else
    echo "FAIL: STOR upload - file not created on server"
fi
```

---

## 4. Concurrency — Two Files Downloaded in Parallel

### Manual Steps

Open two client terminals simultaneously.

**Terminal 2:**
```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> RETR 10mb-example-jpg.jpg
ftp> HASH 10mb-example-jpg.jpg
ftp> QUIT
```

**Terminal 3:**
```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> RETR window.zip
ftp> HASH window.zip
ftp> QUIT
```

Verify after both complete:

```bash
sha256sum /tmp/10mb-example-jpg.jpg
sha256sum /tmp/window.zip
```

Both must match the originals in `test/file_tests/`.

**Pass criteria:**
- Both sessions receive `230 Login successful.`
- Both receive `227 Entering Passive Mode` with distinct ports
- Both complete with `226 Transfer complete.`
- Both HASH show `Hashes match - file integrity verified.`
- Server log shows two distinct `[SESS] registered session N` lines

### Automated Script

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-conc
CTRL=2130
DL=/tmp/hybrid-ftp-conc-dl

rm -rf "$ROOT" "$DL"
mkdir -p "$ROOT" "$DL/1" "$DL/2"
cp "$HYBRID_DIR/test/file_tests/10mb-example-jpg.jpg" "$ROOT/"
cp "$HYBRID_DIR/test/file_tests/window.zip" "$ROOT/"

H1=$(sha256sum "$HYBRID_DIR/test/file_tests/10mb-example-jpg.jpg" | awk '{print $1}')
H2=$(sha256sum "$HYBRID_DIR/test/file_tests/window.zip" | awk '{print $1}')

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/conc.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/conc_a.txt 2>&1 &
USER admin
PASS admin
lcd $DL/1
PASV
RETR 10mb-example-jpg.jpg
HASH 10mb-example-jpg.jpg
QUIT
EOF
PID_A=$!

cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/conc_b.txt 2>&1 &
USER admin
PASS admin
lcd $DL/2
PASV
RETR window.zip
HASH window.zip
QUIT
EOF
PID_B=$!

wait $PID_A 2>/dev/null || true
wait $PID_B 2>/dev/null || true

P=0
F=0

check() {
    local file="$1" expected="$2" label="$3"
    if [ -f "$file" ]; then
        local got
        got=$(sha256sum "$file" | awk '{print $1}')
        if [ "$got" = "$expected" ]; then
            echo "PASS: $label ($(stat -c%s "$file") bytes)"
            P=$((P+1))
        else
            echo "FAIL: $label - hash mismatch"
            F=$((F+1))
        fi
    else
        echo "FAIL: $label - file not found"
        F=$((F+1))
    fi
}

check "$DL/1/10mb-example-jpg.jpg" "$H1" "session A - 10mb-example-jpg.jpg"
check "$DL/2/window.zip" "$H2" "session B - window.zip"

echo ""
echo "Concurrency test: $((P+F)) sessions, $P passed, $F failed"

echo ""
echo "Server sessions:"
grep "registered session" /tmp/conc.log | tail -5

echo ""
echo "BBR transitions:"
grep "BBR:" /tmp/conc.log | head -4
```

Expected output:
```
PASS: session A - 10mb-example-jpg.jpg (10809064 bytes)
PASS: session B - window.zip (27921455 bytes)

Concurrency test: 2 sessions, 2 passed, 0 failed

Server sessions:
[SESS] registered session 1
[SESS] registered session 2

BBR transitions:
[RDT] BBR: STARTUP -> DRAIN
[RDT] BBR: DRAIN -> PROBE_BW
[RDT] BBR: STARTUP -> DRAIN
[RDT] BBR: DRAIN -> PROBE_BW
```

---

## 5. HASH Mismatch Test

```bash
echo "SERVER version of the file" > $ROOT/mismatch.txt
echo "LOCAL version, different content" > /tmp/mismatch.txt
```

```
ftp> USER admin
ftp> PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> HASH mismatch.txt
```

Expected client output:
```
213 SHA-256 0-<server_hash> mismatch.txt
MISMATCH!
Local:  <local_hash>
Remote: <server_hash>
```

**Pass:** Client prints `MISMATCH!` with both hashes shown. Must NOT print "Hashes match".

---

## 6. SIZE and MDTM of test/file_tests/ Files

```
ftp> USER admin
ftp> PASS admin
ftp> SIZE 10mb-example-jpg.jpg
       Expected: 213 10809064
ftp> SIZE book.pdf
       Expected: 213 22254526
ftp> SIZE pexels-simplyart-9020071.mp4
       Expected: 213 16887888
ftp> SIZE viber.deb
       Expected: 213 110082688
ftp> SIZE window.zip
       Expected: 213 27921455
ftp> MDTM book.pdf
       Expected: 213 YYYYMMDDhhmmss
ftp> QUIT
```

---

## 7. LIST After File Placement

```
ftp> USER admin
ftp> PASS admin
ftp> PASV
ftp> LIST
```

Data payload must contain all five filenames:
```
10mb-example-jpg.jpg
book.pdf
pexels-simplyart-9020071.mp4
viber.deb
window.zip
```
