# DESIGN.MD - E-Tix Flash

## Status

Placeholder. Dokumen ini belum diisi.
Pengisian lengkap dijadwalkan di FASE 9.

## Isi Docs

- Judul: DESIGN.MD - E-Tix Flash
- Status: Placeholder, diisi di FASE 9
- Sumber Riset Design yang Ada: docs/_archive/UI UX/ (arsip, proyek lain)
- Target Output: style guide komponen

## Sumber Riset Design yang Ada

Folder desain yang pernah ada di docs/ sudah dipindahkan ke folder arsip.
Semua file di dalam arsip itu berasal dari proyek DENTFLOW, yaitu sistem
klinik gigi dengan role Pasien dan Dokter. File-file itu bukan desain E-Tix Flash.

E-Tix Flash adalah platform tiket konser dan acara besar. Kebutuhannya
berbeda: pemesanan tiket, denah kursi, group locking, dan resale market.
Desain DENTFLOW tidak bisa dipakai langsung.

Lokasi arsip:

- docs/_archive/UI UX/RANGKUMAN DESIGN UI UX.txt
  Ringkasan design system global DENTFLOW v10.0, termasuk color palette
  dan aturan aksesibilitas WCAG AA.
- docs/_archive/UI UX/DESIGN ADMIN WEB.md
  Desain portal admin DENTFLOW v10.0 untuk desktop. Mengikuti PRD v10.0
  dan HALAMAN.txt versi DENTFLOW.
- docs/_archive/UI UX/DESIGN DOCTOR WEB.md
  Desain portal dokter DENTFLOW v10.0 untuk desktop. Tidak ada role dokter
  di E-Tix Flash, jadi file ini tidak relevan untuk scope saat ini.
- docs/_archive/UI UX/DESIGN MOBILE.md
  Desain aplikasi mobile pasien DENTFLOW v4.0, gaya soft UI neumorphism
  dan glassmorphism.

Yang masih bisa diambil dari arsip hanya prinsip umum yang independen
dari domain, misalnya aturan kontras WCAG AA dan target ukuran sentuh.
Prinsip ini harus ditulis ulang dari awal, bukan disalin mentah.

## Target Output

Isi DESIGN.MD di FASE 9 akan berisi:

- Design token. Warna, tipografi, spacing, radius, shadow.
- Component library. Daftar komponen, varian, dan state tiap komponen.
- Layout rules. Grid, container, breakpoint, perilaku responsif.
- Interaction rules. Feedback, loading, empty state, error state.
- Accessibility rules. Contrast, target sentuh, navigasi keyboard.
- Design token dan komponen harus punya nama yang konsisten supaya
  bisa dipakai langsung di frontend tanpa interpretasi ulang.

Komponen yang paling perlu didesain ulang untuk E-Tix Flash:

- Denah kursi interaktif untuk pilih kursi, termasuk state kursi terisi,
  dikunci, dan tidak tersedia.
- Tabel harga dan ongkos biaya yang berubah sesuai tipe tiket.
- Form pemesanan bertahap dengan proteksi group locking.
- Tampilan countdown 3 menit untuk kursi berstatus PENDING_SPLIT_PAY,
  beserta state saat countdown berakhir.

## Rujukan

- docs/TEAM_CONTRACT.md. Aturan kerja tim.
- docs/SUSUNAN DOKUMEN.txt. Urutan dokumen proyek. DESIGN.MD sudah
  tercatat di chain, setelah HALAMAN dan sebelum TDD.
- docs/PRD.txt. Scope produk E-Tix Flash.
- docs/_archive/UI UX/. Arsip desain proyek lain, bukan sumber kebenaran.