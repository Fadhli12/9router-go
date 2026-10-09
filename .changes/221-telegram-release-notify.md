### 📢 Notifikasi release ke grup Telegram

- **Kabar rilis**: setiap tag baru (`v*`) memunculkan ringkasan di grup Telegram — versi, channel
  (stabil atau eksperimental), 10 entri changelog teratas, link ke Release page, dan perintah
  `docker pull`.
- **Isi pesan diringkas, bukan changelog penuh.** Telegram membatasi 4096 karakter, sementara
  `CHANGELOG.md` repo ini 4.971 baris dan satu entry pernah melewati 4.000 karakter sendiri.
  Yang dikirim hanya heading (`###`), karena bullet-nya ada di Release page — dan 700 baris di
  HP tidak terbaca.
- **`notify` jadi job keempat di `release.yml`, `needs: [binaries, docker]`.** Itu inti
  desainnya: job berjalan setelah Release page dan image Docker benar-benar ada, jadi link di
  pesan tidak pernah 404. Workflow terpisah yang trigger `push: tags` berjalan paralel dengan
  build dan rutin mengirim tautan mati.
- **Catatan dioper lewat artifact, bukan `/tmp`.** Tiap job GitHub punya runner sendiri, jadi
  `/tmp/notes.md` dari job `binaries` tidak ada di `notify`. Artifact ini mencegah 40 baris
  logika fallback diduplikasi — kalau tidak, ringkasan di Telegram bisa berbeda dari yang
  dibaca pengguna di halaman Release.
- **`continue-on-error: true`**, karena Telegram yang down atau token dicabut tidak boleh
  menandai release sukses jadi merah; artifact sudah terbit sebelum job ini jalan.
- **`scripts/notify-telegram.sh` bisa dijalankan lokal** dengan `--dry-run` untuk mengecek
  format tanpa tag baru, dan dengan `--notes` untuk menguji jalur kirim sungguhan.
- **Lima bug format, tiga di antaranya hanya muncul saat kirim sungguhan**:
  1. Pemotongan pesan awal memakai `head -n 40` — itu menghitung *baris*, bukan karakter.
     Dengan fixture 59 entry beremoji, pesan keluar **11.131 karakter** dan pasti ditolak
     Telegram. Sekarang dipotong per karakter lalu dirapikan ke batas baris.
  2. `grep '^#{2,4} '` ikut menarik `[Unreleased]` dan judul `[v1.9.11]` seolah entri,
     jadi baris paling atas pesan tidak membawa informasi apa pun. Sekarang `#{3,4}` saja.
  3. Pesan dikirim sebagai argumen `curl`, dan **di Git Bash (Windows) variabel bash yang
     memuat non-ASCII tidak selamat lewat argv** — ditranscode ke code page ANSI, lalu Telegram
     menjawab `400 strings must be encoded in UTF-8` tanpa menerima apa pun. Terukur: variabel
     ASCII murni terkirim, variabel yang sama berisi em-dash tidak pernah — dari file, dari
     `printf`, maupun literal. Emoji sendiri selamat, jadi gaganya baru muncul begitu ada entry
     bertanda hubung, dan entry di repo ini semuanya begitu. Runner `ubuntu-latest` tidak
     terpengaruh, jadi ini tidak pernah muncul di CI — tapi justru merusak jalur tes lokal,
     satu-satunya alasan skrip ini ada. Payload sekarang di-URL-encode ke file, dikirim
     dengan `--data-binary @file`.
  4. Field form dipisah baris baru, bukan `&`. curl menerimanya, Telegram menjawab
     `400 message text is empty`.
  5. `curl --fail-with-body` mengubah penolakan jadi exit 22 dan membuang deskripsi error
     Telegram, sehingga log job menampilkan error transport, bukan "chat not found". Skrip
     sekarang membaca body dan menentukan exit sendiri.
- **Topik forum ditangani lewat `TELEGRAM_TOPIC_ID` opsional.** Grup ini `is_forum: true`;
  tanpa thread id, Telegram membuat topik "Messages" baru yang mungkin tidak pernah dibuka
  anggota. Field-nya dihilangkan sepenuhnya kalau variabel belum di-set, jadi tidak
  mengaturnya adalah konfigurasi yang valid, bukan rusak.
- **Verifikasi**: `bash -n` bersih; dry-run terhadap `CHANGELOG.md` asli (1.069 karakter);
  oversize 19 KB dipotong ke 3.727 dengan link utuh; file kosong, argumen hilang, flag asing,
  dan secret kosong semuanya keluar dengan pesan dan exit code yang benar; `release.yml`
  di-parse dengan `needs: [binaries, docker]`. **Jalur kirim sungguhan diuji langsung ke grup**:
  pesan ber-emoji + em-dash, pesan penuh dari `CHANGELOG.md`, dan pesan oversize — ketiganya
  `ok:true`; token salah memberi `404 Not Found` beserta deskripsinya dan exit 1.
- **Setup ada di [`docs/TELEGRAM_RELEASE_NOTIFICATIONS.md`](docs/TELEGRAM_RELEASE_NOTIFICATIONS.md)**:
  buat bot via BotFather, **tambahkan bot ke grup dulu** (tanpa itu API balas `403`, bukan
  `404`), ambil `chat.id` dari `getUpdates`, lalu simpan `TELEGRAM_BOT_TOKEN` dan
  `TELEGRAM_CHAT_ID` sebagai repository secrets.