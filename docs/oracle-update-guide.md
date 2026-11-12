# Panduan Update 9router-go di Oracle Server

> Dokumen ini untuk agent/pengoperator yang akan meng-update gateway `9router-go` di Oracle Cloud.
> Terakhir diverifikasi: 2026-11-12 (versi `1.9.16-exp.13`).

## 1. Identitas Server

| Item | Nilai |
|---|---|
| Alias SSH | `oracle` (didefinisikan di `~/.ssh/config`) |
| Host | `129.225.7.86` |
| User login | `ubuntu` |
| Key | `~/.ssh/ssh_key_orcale.key` |
| Arsitektur | **aarch64 (ARM64 / Ampere A1)** — selalu build `9router-go-linux-arm64` |
| OS | Oracle Linux / Ubuntu (cek dengan `uname -m` sebelum build) |

Semua perintah di bawah dijalankan dari workstation lokal (root repo `9router-go`), kecuali disebut "di server".

## 2. Service yang Berjalan di Server (PENTING)

Ada **DUA kandidat proses** — jangan sampai salah:

1. **`9router.service` (systemd, milik user `fadhli`)** — ini yang "9router cloud" sesungguhnya:
   - Unit: `/etc/systemd/system/9router.service`
   - Binary: `/home/fadhli/.local/bin/9router-go`
   - Port: **40859** (`Environment=PORT=40859`)
   - Data: `/home/fadhli/.9router`
   - `Restart=always`, jadi **kill saja tidak mematikannya** — dia akan respawn 3 detik kemudian. Gunakan `systemctl` untuk mengendalikan.

2. **Proses manual user `ubuntu`** (`~/9router-go`, port 20130) — ini sisa eksperimen deploy. **Sudah dihapus bersih** (binary + `~/.9router` data dirnya, 2026-11-12). Bila muncul lagi dari deploy manual:
   ```bash
   ssh oracle "kill <pid> 2>/dev/null; rm -f /home/ubuntu/9router-go; rm -rf /home/ubuntu/.9router"
   ```

## 3. Alur Update Standar (dari workstation lokal)

```bash
cd /Users/ant.fadhli/Project/go/9router-go

# 3.1 Sync + merge upstream bila perlu (lihat §4), lalu build cross:
make cross   # menghasilkan 9router-go-linux-arm64 (web/dist harus sudah ada; make cross menjalankan web-build)

# 3.2 Upload ke server (ke home ubuntu sebagai staging, lalu pindahkan — jangan
# biarkan binary staging jalan; setelah cp ke fadhli, hapus staging:
#   rm -f /home/ubuntu/9router-go  — dan JANGAN start binary staging):
scp 9router-go-linux-arm64 oracle:~/9router-go

# 3.3 Ganti binary service. "Text file busy" bila service masih hidup — STOP dulu:
ssh oracle "sudo systemctl stop 9router.service && \
  sudo cp /home/ubuntu/9router-go /home/fadhli/.local/bin/9router-go && \
  sudo chown fadhli:fadhli /home/fadhli/.local/bin/9router-go && \
  sudo systemctl start 9router.service"

# 3.4 Verifikasi (versi + health + process):
ssh oracle "sleep 2 && \
  sudo systemctl status 9router.service --no-pager | head -n 6 && \
  /home/fadhli/.local/bin/9router-go version && \
  curl -s http://127.0.0.1:40859/health"
```

Berhasil bila output-nya:
- `Active: active (running)` untuk `9router.service`
- `9router-go version 1.9.16-exp.13 (linux/arm64)` (atau versi terbaru)
- `{"status":"ok"}` dari endpoint `/health` di port **40859**

## 4. Sinkronisasi Upstream Sebelum Build (bila update berasal dari upstream)

Upstream: `upstream` → `https://github.com/luqman-v1/9router-go` (fork kita: `origin` → `Fadhli12/9router-go`).

```bash
git fetch upstream
git merge --no-commit --no-ff upstream/main   # review dulu
# Bila conflict:
#   - internal/db/accounts_oauth_lock.go      → pertahankan BLOK LOKAL (ConnectionModelLockedFrom)
#                                               DAN blok upstream (account-ban ladder). Keduanya disimpan berdampingan.
#   - internal/translator/antigravity_test.go → ambil test baru upstream (TestAntigravityPromptRewrites),
#                                               jangan hapus asersi lokal "PreservesProviderIdentifiers".
# Perbaiki compile: bila "undefined: providers.AntigravityCLIVersion",
#   tambahkan alias di internal/providers/client_identity.go:
#   AntigravityCLIVersion = AntigravityHubVersion
git add ... && git commit  # conclude merge
git push origin main
```

Verifikasi sebelum push (yang dijalankan CI juga):

```bash
TMPDIR=/tmp go test ./...              # mcodelease butuh TMPDIR=/tmp di macOS (unix socket path)
go test -tags=integration -count=1 ./internal/integration/...
make vet-svelte                        # ratchet: total error tidak boleh naik
make build                             # binary lokal
make cross                             # semua target + SHA256SUMS
```

Catatan kegagalan yang pernah terjadi dan solusinya:

| Gejala | Penyebab | Solusi |
|---|---|---|
| `TestLeaseEndToEnd: bind: invalid argument` | socket unix terlalu panjang di TMPDIR default macOS | `TMPDIR=/tmp go test ./...` |
| `TestForwardQoder_UnknownModelNamesTheModel` timeout jaringan | `ConnData` tanpa `machineId` → fallback fetch model list live | tambahkan `"machineId": creds.MachineID` di ConnData test |
| `make test-integration` timeout 120s | suite ~90 detik + web rebuild | jalankan `go test -tags=integration ./internal/integration/...` langsung |
| `cp: Text file busy` saat ganti binary | service systemd masih berjalan | `sudo systemctl stop 9router.service` sebelum `cp` |
| Kill proses fadhli tapi muncul lagi | `Restart=always` | kendalikan via `systemctl`, bukan `kill` |

## 5. Panduan Merge "Hati-hati" (poin penting)

- **Jangan pernah** menyelesaikan conflict dengan membuang fungsi lokal: fungsi fork seperti `ConnectionModelLockedFrom` (anti N+1 query, AGENTS.md §4.A) tidak ada di upstream dan akan hilang bila side upstream diambil mentah-mentah.
- Sebaliknya, jangan blok fitur baru upstream (mis. escalating lockout ladder `RecordConnectionAccountBanFailure`) — keduanya bisa berdampingan dalam file yang sama.
- Setelah resolve, **wajib run test** paket yang filenya disentuh sebelum commit.
- Buat backup branch sebelum merge besar:
  ```bash
  git branch backup/pre-upstream-merge-$(date +%Y%m%d-%H%M%S)
  ```

## 6. Local (Mac) — Pengingat

- Build lokal: `make build` → `./9router-go`
- Jalankan background: `./9router-go start` (dashboard http://127.0.0.1:20130)
- Bila `start` bilang "already running ... started outside this binary": ada proses lain pegang port 20130 — cari `lsof -i :20130`, kill PID lama, `./9router-go stop`, lalu `start` ulang.

## 7. Checklist Cepat Update Harian

1. `git fetch upstream && git log --oneline main..upstream/main` — ada commit baru?
2. Merge (bila ada), resolve conflict, run test.
3. `make cross` → `scp 9router-go-linux-arm64 oracle:~/9router-go`
4. `ssh oracle` → stop service → cp binary → chown → start service.
5. Verifikasi `systemctl status`, `version`, `curl /health` (port 40859).
6. Pastikan tidak ada proses `ubuntu` liar di 20130 dan staging binary sudah dihapus (`ps aux | grep 9router-go`; hanya `fadhli` boleh ada).
7. Push `main` ke `origin`.
