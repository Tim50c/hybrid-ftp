# Part 1 — Manual Testing with Generated Test Files

How to test all features of Hybrid FTP using small hand-crafted files and
synthetic large binaries. Covers authentication, navigation, directory
operations, all commands, transfer correctness, concurrency, BBR, and RDT
protocol verification.

---

## 0. Build & Environment

```bash
cd hybrid-ftp
go build ./cmd/server
go build ./cmd/client
go vet ./...
go test ./internal/... -v
go test -race ./internal/...
go test ./test/integration/... -v -timeout 180s
```

### Fixture Setup

```bash
ROOT=/tmp/hybrid-ftp-test
mkdir -p $ROOT/sub1/sub2/sub3

echo "hello from hybrid ftp" > $ROOT/hello.txt
echo "binary content" > $ROOT/data.bin
echo "nested file" > $ROOT/sub1/nested.txt
touch $ROOT/empty.bin
printf "line1\r\nline2\r\n" > $ROOT/crlf.txt

dd if=/dev/urandom of=$ROOT/big5.bin bs=1M count=5 status=none
dd if=/dev/urandom of=$ROOT/big50.bin bs=1M count=50 status=none
dd if=/dev/urandom of=$ROOT/big100.bin bs=1M count=100 status=none
dd if=/dev/urandom of=$ROOT/huge.bin bs=1M count=200 status=none
```

### Kill stale server and start fresh

```bash
fuser -k 2130/tcp 2>/dev/null
sleep 1
```

```bash
./server -ctrl :2130 -root $ROOT -users users.conf
```

Expected: `[CTRL] server listening on :2130`

Leave this terminal open. Open a second terminal for the client.

```bash
./client -host 127.0.0.1 -ctrl 2130
```

Expected: `220 Hybrid FTP Ready.`

> PASV and PORT are single-use. Re-issue before every data command.

---

## 1. Authentication

### 1.A — Login success

```
ftp> USER admin            Expected: 331 Password required.
ftp> PASS admin            Expected: 230 Login successful.
```

### 1.B — Wrong password

```
ftp> USER admin
ftp> PASS wrongpass        Expected: 530 Login failed.
```

Server log: `[AUTH] user admin password mismatch`

### 1.C — Unknown user

```
ftp> USER nobody
ftp> PASS anything         Expected: 530
```

### 1.D — Unauthenticated commands rejected

```
ftp> PWD                   Expected: 530 Login first.
ftp> RETR x                Expected: 530 Login first.
```

### 1.E — Double PASS when already logged in

```
ftp> USER admin; PASS admin
ftp> PASS admin            Expected: 503 Already logged in.
```

---

## 2. Greeting

```bash
nc 127.0.0.1 2130
```

Expected output: `220 Hybrid FTP Ready.`

Only one 220 per connection. Subsequent NOOP replies are 200.

---

## 3. Navigation — PWD, CWD, CDUP

### 3.A — PWD at root

```
ftp> PWD                   Expected: 257 "..." is current directory.
```

### 3.B — CWD into subdirectory

```
ftp> CWD sub1              Expected: 250 Directory changed.
ftp> PWD                   Expected: path includes sub1
```

### 3.C — CWD nonexistent

```
ftp> CWD doesnotexist      Expected: 550 Directory not found.
```

### 3.D — CDUP from subdirectory

```
ftp> CWD sub1
ftp> CDUP                  Expected: 250 Directory changed to parent.
ftp> PWD                   Expected: back at root
```

### 3.E — CDUP from root

```
ftp> CDUP                  Expected: 250 Directory changed to parent.
```

Stays at root. No crash.

---

## 4. Path Containment

### 4.A — CWD .. from root

```
ftp> CWD ..                Expected: 550 Access denied.
```

### 4.B — RETR absolute path

```
ftp> RETR /etc/passwd      Expected: 550 Cannot open /etc/passwd.
```

### 4.C — MKD and STOR traversal

```
ftp> MKD ../evil           Expected: 550 Cannot create directory.
ftp> PASV
ftp> STOR /tmp/evil.txt    Expected: 550 Access denied.
```

No files created outside root.

---

## 5. Directory Operations — MKD, RMD

### 5.A — Create and remove

```
ftp> MKD newdir            Expected: 257 "/newdir" created.
ftp> RMD newdir            Expected: 250 Directory removed.
```

### 5.B — Edge cases

```
ftp> MKD                   Expected: 501 MKD requires a directory name.
ftp> MKD dup
ftp> MKD dup               Expected: 550
ftp> RMD                   Expected: 501 RMD requires a directory name.
```

---

## 6. Transfer Setup — TYPE, MODE

```
ftp> TYPE A      Expected: 200 Type set.
ftp> TYPE I      Expected: 200 Type set.
ftp> TYPE X      Expected: 500 TYPE must be A or I.
ftp> TYPE        Expected: 500 TYPE must be A or I.
ftp> MODE S      Expected: 200 Mode set to Stream.
ftp> MODE B      Expected: 504 Block mode not implemented.
ftp> MODE C      Expected: 504 Compressed mode not implemented.
ftp> MODE X      Expected: 501 MODE must be S, B, or C.
```

---

## 7. Listing — LIST, NLST, STAT

Re-issue PASV before every LIST/NLST.

### 7.A — LIST root

```
ftp> PASV
ftp> LIST        Expected: 150 ... 226 Directory listing complete.
```

Payload shows `hello.txt`, `data.bin`, `sub1/`, `empty.bin`, etc.

### 7.B — NLST

```
ftp> PASV
ftp> NLST        Expected: 150 ... names only ... 226
```

### 7.C — LIST/NLST without PASV

```
ftp> LIST        Expected: 503 Use PASV or PORT first.
```

### 7.D — STAT

```
ftp> STAT                  Expected: 211 Hybrid FTP server status: running.
ftp> STAT hello.txt        Expected: 213 File: hello.txt | Size: 21
ftp> STAT sub1             Expected: 212 Directory: sub1
ftp> STAT nope             Expected: 550 Cannot stat path.
```

---

## 8. File Metadata — SIZE, MDTM

### 8.A — SIZE

```
ftp> SIZE hello.txt        Expected: 213 21
ftp> SIZE sub1             Expected: 550 Cannot get file size.
ftp> SIZE                  Expected: 501 SIZE requires a filename.
```

### 8.B — MDTM

```
ftp> MDTM hello.txt        Expected: 213 YYYYMMDDhhmmss
ftp> MDTM                  Expected: 501 MDTM requires a filename.
```

---

## 9. PASV + PORT

### 9.A — PASV format

```
ftp> PASV        Expected: 227 Entering Passive Mode (127,0,0,1,p1,p2).
```

Six comma-separated numbers in parentheses.

### 9.B — PORT parsing

```
ftp> PORT 1,2,3,4,5         Expected: 501 PORT requires h1,h2,h3,h4,p1,p2.
ftp> PORT 1,2,3,4,1,300     Expected: 501 Invalid PORT parameter.
ftp> PORT a,b,c,d,e,f        Expected: 501 Invalid PORT parameter.
ftp> PORT 127,0,0,1,200,0   Expected: 200 PORT command successful.
```

### 9.C — PASV is single-use

```
ftp> PASV
ftp> RETR hello.txt         Expected: 226
ftp> RETR data.bin          Hangs. Re-PASV needed.
```

### 9.D — Data command without mode

```
ftp> RETR hello.txt         Expected: 503 Use PASV or PORT first.
```

---

## 10. RETR — Download

### 10.A — Small ASCII file

```
ftp> PASV
ftp> RETR hello.txt          Expected: 150 ... 226 Transfer complete.
```

Local file matches `hello from hybrid ftp`.

### 10.B — Zero-byte file

```
ftp> PASV
ftp> RETR empty.bin          Expected: 150 ... 226
```

Zero bytes received, no hang.

### 10.C — Missing file

```
ftp> PASV
ftp> RETR nope               Expected: 550 Cannot open nope.
```

### 10.D — Binary CRLF preservation (TYPE I)

```
ftp> TYPE I
ftp> PASV
ftp> RETR crlf.txt
```

Downloaded bytes match original exactly — no `\r\n` to `\n` conversion.

### 10.E — 5 MB file with progress bar

```
ftp> PASV
ftp> RETR big5.bin
```

Progress bar shows: `/ X.X MiB downloaded | X.X MiB/s`
Updates ~10 times per second. SHA-256 matches.

### 10.F — 50 MB file

```
ftp> PASV
ftp> RETR big50.bin          Expected: 226. SHA-256 matches.
```

### 10.G — 100 MB file

```
ftp> PASV
ftp> RETR big100.bin         Expected: 226. SHA-256 matches.
```

---

## 11. STOR — Upload

### 11.A — Small upload

```bash
echo "upload test content" > /tmp/up.txt
```

```
ftp> lcd /tmp
ftp> PASV
ftp> STOR up.txt             Expected: 150 ... 226
```

Server file byte-identical to local.

### 11.B — Large upload (5 MB)

```bash
dd if=/dev/urandom of=/tmp/big5_up.bin bs=1M count=5 status=none
```

```
ftp> lcd /tmp
ftp> PASV
ftp> STOR big5_up.bin        Expected: 150 ... 226
```

Server file SHA-256 matches local.

### 11.C — Overwrite

```
ftp> PASV
ftp> STOR up.txt             First upload
ftp> PASV
ftp> STOR up.txt             Overwrite. Expected: 226.
```

Overwritten correctly. Hash matches latest content.

---

## 12. STOU — Store Unique

```
ftp> PASV
ftp> STOU                  Expected: 150 ... 257 "/stou_<ts>_<hex>.dat" stored.
ftp> PASV
ftp> STOU                  Different name from first call.
```

Two calls produce two different `stou_...` names.

---

## 13. APPE — Append

```bash
echo "hello" > $ROOT/a.txt
```

```
ftp> PASV
ftp> APPE a.txt            Send " world"
```

Server file reads `hello world`. Expected: 226.

---

## 14. DELE — Delete

```bash
echo "to delete" > $ROOT/todelete.txt
```

```
ftp> DELE todelete.txt     Expected: 250 File deleted.
ftp> RETR todelete.txt     Expected: 550 Cannot open todelete.txt.
ftp> DELE                  Expected: 501 DELE requires a filename.
```

---

## 15. RNFR / RNTO — Rename

```bash
echo "rename me" > $ROOT/oldname.txt
```

```
ftp> RNFR oldname.txt      Expected: 350 File exists, ready for RNTO.
ftp> RNTO newname.txt      Expected: 250 Rename successful.
ftp> RETR oldname.txt      Expected: 550
```

New name exists, old name gone.

---

## 16. HASH — Integrity

### 16.A — Match

```
ftp> HASH hello.txt        Expected: 213 SHA-256 0-<hex> hello.txt
                           Expected: Hashes match - file integrity verified.
```

### 16.B — Mismatch

```bash
echo "local content" > /tmp/mismatch.txt
echo "server content" > $ROOT/mismatch.txt
```

```
ftp> lcd /tmp
ftp> PASV
ftp> HASH mismatch.txt
```

Expected output:
```
213 SHA-256 0-<server_hash> mismatch.txt
MISMATCH!
Local:  <local_hash>
Remote: <server_hash>
```

### 16.C — Edge cases

```
ftp> HASH sub1             Expected: 550 Cannot compute hash.
ftp> HASH                  Expected: 501 HASH requires a filename.
```

---

## 17. ABOR

### 17.A — No transfer

```
ftp> ABOR                  Expected: 226 ABOR successful.
```

### 17.B — Mid-transfer

```
ftp> PASV
ftp> RETR big100.bin       Starts 100 MB download
ftp> ABOR                  Expected: 426 Transfer aborted.
```

Server log: `[CTRL] transfer aborted by ABOR`

### 17.C — Recovery

```
ftp> ABOR
ftp> PASV
ftp> RETR hello.txt        Works normally after abort.
```

---

## 18. NOOP, HELP, QUIT

```
ftp> NOOP        Expected: 200 NOOP ok.
ftp> HELP        Expected: 214 Supported commands: USER PASS ...
ftp> HELP RETR   Expected: 214 Help for RETR: see RFC 959.
ftp> QUIT        Expected: 221 Goodbye.
ftp> GARBAGE     Expected: 500 Unknown command.
```

---

## 19. Local Commands — lpwd, lcd, lls

```
ftp> lpwd                    Prints current working directory
ftp> lcd /tmp                No error; lpwd now shows /tmp
ftp> lcd /nonexistent        Error printed, session continues
ftp> lls                     Lists files in current directory
```

All three work. Errors don't crash the client.

---

## 20. PORT Mode Full Transfer

### 20.A — PORT RETR

```
ftp> PORT 127,0,0,1,200,0   Expected: 200 PORT command successful.
ftp> RETR hello.txt          Expected: 150 ... 226
```

### 20.B — PORT STOR

```
ftp> PORT 127,0,0,1,200,1   Expected: 200
ftp> STOR up_port.txt        Expected: 150 ... 226
```

### 20.C — PORT is single-use

```
ftp> PORT ...                First PORT
ftp> RETR a.txt              Expected: 226
ftp> RETR b.txt              Fails without re-PORT
```

---

## 21. Concurrency

### 21.A — Two parallel downloads

Open two client terminals and run simultaneously:

**Terminal 2:**
```
ftp> USER admin; PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> RETR big5.bin
ftp> HASH big5.bin
ftp> QUIT
```

**Terminal 3:**
```
ftp> USER admin; PASS admin
ftp> lcd /tmp
ftp> PASV
ftp> RETR big50.bin
ftp> HASH big50.bin
ftp> QUIT
```

Both complete with 226. Both SHA-256 match. Server log shows
`[SESS] registered session N` with distinct IDs.

### 21.B — Automated concurrency script

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

dd if=/dev/urandom of="$ROOT/c1.bin" bs=1M count=5 status=none
dd if=/dev/urandom of="$ROOT/c2.bin" bs=1M count=5 status=none

H1=$(sha256sum "$ROOT/c1.bin" | awk '{print $1}')
H2=$(sha256sum "$ROOT/c2.bin" | awk '{print $1}')

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/conc.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/conca.txt 2>&1 &
USER admin
PASS admin
lcd $DL/1
PASV
RETR c1.bin
HASH c1.bin
QUIT
EOF
PID1=$!

cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/concb.txt 2>&1 &
USER admin
PASS admin
lcd $DL/2
PASV
RETR c2.bin
HASH c2.bin
QUIT
EOF
PID2=$!

wait $PID1 2>/dev/null || true
wait $PID2 2>/dev/null || true

check() {
    local file="$1" expected="$2" label="$3"
    if [ -f "$file" ]; then
        local got
        got=$(sha256sum "$file" | awk '{print $1}')
        if [ "$got" = "$expected" ]; then
            echo "PASS: $label ($(stat -c%s "$file") bytes)"
        else
            echo "FAIL: $label - hash mismatch"
        fi
    else
        echo "FAIL: $label - file not found"
    fi
}

check "$DL/1/c1.bin" "$H1" "session A"
check "$DL/2/c2.bin" "$H2" "session B"
```

### 21.C — Five parallel transfers (stress)

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-stress
CTRL=2130
DL=/tmp/hybrid-ftp-stress-dl

rm -rf "$ROOT" "$DL"
mkdir -p "$ROOT"

for i in 1 2 3 4 5; do
    dd if=/dev/urandom of="$ROOT/stress${i}.bin" bs=1M count=5 status=none
    mkdir -p "$DL/s${i}"
done

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$HYBRID_DIR/users.conf" > /tmp/stress.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

for i in 1 2 3 4 5; do
    cat <<EOF | timeout 60 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" > /tmp/stress${i}.txt 2>&1 &
USER admin
PASS admin
lcd $DL/s${i}
PASV
RETR stress${i}.bin
QUIT
EOF
done

wait

P=0; F=0
for i in 1 2 3 4 5; do
    WANT=$(sha256sum "$ROOT/stress${i}.bin" | awk '{print $1}')
    if [ -f "$DL/s${i}/stress${i}.bin" ]; then
        GOT=$(sha256sum "$DL/s${i}/stress${i}.bin" | awk '{print $1}')
        if [ "$GOT" = "$WANT" ]; then
            echo "PASS: stress${i} ($(stat -c%s "$DL/s${i}/stress${i}.bin") bytes)"
            P=$((P+1))
        else
            echo "FAIL: stress${i} - hash mismatch"
            F=$((F+1))
        fi
    else
        echo "FAIL: stress${i} - file not found"
        F=$((F+1))
    fi
done

echo ""
echo "Stress test: $((P+F)) sessions, $P passed, $F failed"
```

Expected: All 5 complete. Server still responsive after test.

### 21.D — Session isolation

```
Terminal 1:
  CWD sub1
  PWD          Shows /sub1

Terminal 2:
  PWD          Shows /
```

CWD changes do not affect other sessions.

---

## 22. BBR Congestion Control

### 22.A — Basic state transitions

After any transfer of a file >= 5 MB:

```bash
grep "BBR:" /path/to/server.log
```

Expected output contains:
```
[RDT] BBR: STARTUP -> DRAIN
[RDT] BBR: DRAIN -> PROBE_BW
```

### 22.B — PROBE_RTT cycle

```bash
ftp> PASV
ftp> RETR huge.bin    200 MB file, runs > 12 seconds
```

```bash
grep "PROBE_RTT" /path/to/server.log
```

Expected:
```
[RDT] BBR: PROBE_BW -> PROBE_RTT       after ~10 s
[RDT] BBR: PROBE_RTT -> PROBE_BW       after ~200 ms
```

### 22.C — cwnd floor

```bash
go test ./internal/rdt/... -run TestBBR -v
```

Expected: `cwnd=5600` (4 x 1400-byte MSS).

---

## 23. RDT Protocol Verification

Requires `iptables` or `tc` on Linux.

### 23.A — Packet size

```bash
tcpdump -i lo -nn udp port <pasv-port> -c 20
```

Every UDP datagram <= 1421 bytes (21 header + max 1400 payload).

### 23.B — Loss recovery

```bash
sudo iptables -A INPUT -p udp --sport <pasv-port> \
    -m statistic --mode random --probability 0.05 -j DROP

ftp> PASV
ftp> RETR big50.bin

sudo iptables -D INPUT -p udp --sport <pasv-port> \
    -m statistic --mode random --probability 0.05 -j DROP
```

Transfer completes. SHA-256 matches. Log shows `retransmitting seq=`.

### 23.C — Checksum rejection

```bash
sudo tc qdisc add dev lo root netem corrupt 5%

ftp> PASV
ftp> RETR big5.bin

sudo tc qdisc del dev lo root
```

Corrupted packets discarded (`receiver checksum fail` in log). Transfer completes.

### 23.D — Reordering

```bash
sudo tc qdisc add dev lo root netem delay 10ms reorder 25%

ftp> PASV
ftp> RETR big5.bin

sudo tc qdisc del dev lo root
```

Log shows `buffered out-of-order seq=`. Eventual in-order delivery.

### 23.E — 3-way handshake

```bash
grep "\[RDT\]" server.log | head -10
```

SYN, SYN+ACK, ACK sequence visible.

### 23.F — Max retransmit abort

```bash
sudo iptables -A INPUT -p udp --sport <pasv-port> -j DROP

ftp> PASV
ftp> RETR big5.bin       Will time out

sudo iptables -D INPUT -p udp --sport <pasv-port> -j DROP
```

Log: `max retransmits exceeded`. Transfer aborted with 426 or connection error.

---

## 24. Server Lifecycle

```bash
./server -ctrl :2130               Expected: [CTRL] server listening on :2130
./server -ctrl :2222               Listens on custom port
./server -root /tmp/notexist       Auto-creates directory then starts

fuser -k 2130/tcp                   Kill old server
./server -ctrl :2130 &              Start in background
./server -ctrl :2130                Expected: bind error, exits non-zero

Ctrl+C                              Expected: [CTRL] shutting down... [CTRL] server stopped.

Ctrl+C mid-transfer                 Transfer aborts within 1s. No zombie goroutines.
```

---

## Quick Smoke Test (all-in-one script)

```bash
#!/usr/bin/env bash
set -euo pipefail

HYBRID_DIR="/home/tanp21/Projects/Computer_Networking/Project_1/hybrid-ftp"
SERVER="$HYBRID_DIR/server"
CLIENT="$HYBRID_DIR/client"
ROOT=/tmp/hybrid-ftp-smoke
CTRL=2130
DL=/tmp/hybrid-ftp-smoke-dl
CONF="$HYBRID_DIR/users.conf"

rm -rf "$ROOT" "$DL"
mkdir -p "$ROOT/sub1" "$ROOT/sub2/sub3" "$DL"
echo "hello" > "$ROOT/hello.txt"
echo "nested" > "$ROOT/sub1/nested.txt"
touch "$ROOT/empty.bin"

fuser -k ${CTRL}/tcp 2>/dev/null || true
sleep 1

"$SERVER" -ctrl ":${CTRL}" -root "$ROOT" -users "$CONF" > /tmp/smoke.log 2>&1 &
trap "kill %1 2>/dev/null; rm -rf $ROOT $DL" EXIT
sleep 1

run() {
    echo "--- $1 ---"
    cat <<EOF | timeout 15 "$CLIENT" -host 127.0.0.1 -ctrl "$CTRL" 2>&1
$2
EOF
    echo ""
}

run "Login"                        "USER admin\nPASS admin\nQUIT"
run "Wrong password"               "USER admin\nPASS wrong\nQUIT"
run "PWD + STAT"                   "USER admin\nPASS admin\nPWD\nSTAT\nQUIT"
run "CWD + CDUP"                   "USER admin\nPASS admin\nCWD sub1\nPWD\nCDUP\nPWD\nQUIT"
run "MKD + RMD"                    "USER admin\nPASS admin\nMKD tmp\nRMD tmp\nQUIT"
run "TYPE + MODE"                  "USER admin\nPASS admin\nTYPE I\nMODE S\nQUIT"
run "PASV + LIST"                  "USER admin\nPASS admin\nPASV\nLIST\nQUIT"
run "RETR hello.txt"               "USER admin\nPASS admin\nlcd $DL\nPASV\nRETR hello.txt\nQUIT"
run "HASH hello.txt"               "USER admin\nPASS admin\nlcd $DL\nPASV\nHASH hello.txt\nQUIT"
run "SIZE + MDTM"                  "USER admin\nPASS admin\nSIZE hello.txt\nMDTM hello.txt\nQUIT"
run "DELE"                         "USER admin\nPASS admin\nDELE tmpfile\nQUIT"
run "RNFR/RNTO"                    "USER admin\nPASS admin\nRNFR old\nRNTO new\nQUIT"
run "ABOR"                         "USER admin\nPASS admin\nABOR\nQUIT"
run "NOOP + HELP"                  "USER admin\nPASS admin\nNOOP\nHELP\nQUIT"
run "PORT"                         "USER admin\nPASS admin\nPORT 127,0,0,1,200,0\nQUIT"
run "Path containment"             "USER admin\nPASS admin\nCWD ..\nRETR /etc/passwd\nQUIT"

echo "=== Check BBR transitions ==="
grep "BBR:" /tmp/smoke.log | head -4
echo ""
echo "=== All smoke tests done ==="
```

---

## Reply Code Quick Reference

| Code | Meaning |
|------|---------|
| 150 | Opening data connection |
| 200 | Command okay |
| 211 | System status |
| 212 | Directory status |
| 213 | File status / SIZE / MDTM / HASH |
| 214 | Help message |
| 220 | Service ready |
| 221 | Goodbye |
| 226 | Transfer complete |
| 227 | Entering Passive Mode |
| 230 | Login successful |
| 250 | Requested file action okay |
| 257 | Pathname created / PWD |
| 331 | Need password |
| 350 | Need RNTO |
| 425 | Can't open data connection |
| 426 | Transfer aborted |
| 500 | Unknown command / bad args |
| 501 | Syntax error in parameters |
| 503 | Bad sequence |
| 504 | Not implemented (MODE B/C) |
| 530 | Not logged in / login failed |
| 550 | No such file / permission denied |

## Log Prefix Reference

| Prefix | Module | What to grep |
|--------|--------|-------------|
| `[CTRL]` | Control plane | `connected from`, `cmd:`, `shutting down` |
| `[AUTH]` | Authentication | `authenticated`, `password mismatch`, `not found` |
| `[SESS]` | Session manager | `registered session N`, `unregistered` |
| `[FS]` | File system | File/directory operations |
| `[RDT]` | RDT protocol | `SYN`, `ACK`, `FIN`, `retransmitting`, `buffered out-of-order`, `checksum fail`, `BBR:` |
