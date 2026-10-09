### 🔀 Changelog dipindah ke fragment per-PR — konflik `CHANGELOG.md` jadi mustahil

- **Latar belakang**: `CHANGELOG.md` 4.971 baris dan **setiap PR menulis entry baru persis di
  baris 5-6** (`## [Unreleased]` + entry pertama). Heading itu jadi hunk terpanas di repo,
  jadi tiap dua PR yang landai bareng pasti bentrok, dan `## [Unreleased]` menumpuk 675 baris
  sejak `v1.9.10-exp.3`. Aturan §7 "resolve additive" di `AGENTS.md` dibuat justru karena
  konflik itu selalu muncul di tempat yang sama — band'sat manual, bukan-solusi.
- **Kenapa tidak `merge=union` di `.gitattributes`**: itu pilihan yang murah dan kelihatannya
  tepat, tapi tidak berlaku di repo ini. Merge lewat tombol Merge di GitHub
  (commit `4dbd63d9 … (#219)` adalah squash merge server-side), dan GitHub **mengabaikan
  `.gitattributes` merge driver sepenuhnya** — limitation yang sudah lama, bukan config.
  Union merge lokal sudah diuji (merge, `--squash`, `cherry-pick` semuanya bersih), tapi lokal
  bukan yang dipakai repo ini. Bot union-merge lewat Actions ditolak karena butuh token write
  dan PR tetap `CONFLICTING` di UI sampai action jalan.
- **Fiks**: satu file `.changes/<pr-number>-<slug>.md` per PR. Dua PR yang bareng tidak pernah
  berbagi satu baris, jadi tidak ada yang bisa bentrok — bukan karena ada aturan resolusi, tapi
  karena tidak ada yang sama untuk dibentrokan. `CHANGELOG.md` sekarang ditulis **hanya** oleh
  `make changelog-merge TAG=…` saat release.
- **Dashboard tetap live, dan justru lebih segar dari sebelumnya**. `GET /api/changelog`
  merakit `CHANGELOG.md` + seluruh fragment (package baru `internal/changelogfrag`), jadi
  pekerjaan yang sudah merge tapi belum ada tag langsung terlihat — tanpa menunggu release.
  Ini menjawab kekhawatiran "kalau tiap PR nulis file baru, nanti dashboard-nya gimana?":
  file baru tidak muncul sebagai perubahan, hanya sebagai isi.
- **Rantai fallback release notes** (`release.yml`) sekarang: section `## [<tag>]` → `[Unreleased]`
  → fragment `.changes/` → commit log. Tanpa tahap fragment, halaman release akan jatuh ke
  commit subjects, karena tidak ada lagi yang mengisi `CHANGELOG.md` sebelum tag.
- **Tiga bug yang ketahuan saat verifikasi, bukan saat baca kode**:
  1. Fragmen di-join dengan `\n`, jadi `### heading` berikutnya menempel ke list item — marked
     mem-parsingnya sebagai continuation line dan entry hilang ke dalam bullet sebelumnya.
     Sekarang dipisah blank line, dengan `TestAssembleSeparatesFragmentsWithBlankLine`.
  2. Merge kedua (setelah `[Unreleased]` hilang) menaruh release **di atas** `# Changelog`, judul
     file hilang permanen. Ditangkap smoke test tiga release berturut-turut, bukan unit test.
  3. `.changes/README.md` ikut ter-render jadi entry — instruksi kontributor muncul di atas
     release notes di modal.
- **Verifikasi**: `go build ./...`, `go vet ./...`, unit test `internal/changelogfrag`
  (21 case: urutan numerik PR, blank-line separation, README exclusion, tag berisi bracket),
  `go test -race ./internal/integration/` dengan `TestChangelogShowsPendingFragments` lewat
  router produksi, plus smoke `changelog-merge` tiga release berturut-turut terhadap salinan repo
  dan rantai fallback `release.yml` diuji di bash dengan fixture 88/900/1012.
- **Dokumentasi**: `AGENTS.md` §7.6 (cara menulis fragment) dan §7.7 (konflik `CHANGELOG.md`
  kini tanda ada edit yang bocor, bukan pekerjaan manual), PR template, `.changes/README.md`.