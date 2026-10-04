# BRANCHING STRATEGY — E-Tix Flash

Dokumen ini menjelaskan strategi branch yang dipakai di repo E-Tix Flash.

## Prinsip Umum

- main = production branch. Wajib PR + CI pass.
- develop = staging/integration branch. Tempat merge fitur-fitur yang sudah selesai.
- feature/* = branch untuk fitur baru. Dibuat dari develop.
- fix/* = branch untuk bug fix non-kritis. Dibuat dari develop.
- hotfix/* = branch untuk emergency fix. Dibuat dari main.

## Aturan Praktis

1. Jangan commit langsung ke main atau develop. Selalu via PR.
2. Satu PR = satu fitur atau satu fix. Jangan campur.
3. Delete branch setelah PR merged.
4. Nama branch harus deskriptif:
   - feature/fase-0-e1t3-branch-strategy
   - fix/split-pay-typo
   - hotfix/jwt-expiry-bug
5. Rebase dari develop sebelum PR untuk menghindari conflict.
6. Commit message format: type(scope): description (conventional commits).

## Alur Kerja

Fitur baru:
1. git checkout develop
2. git pull origin develop
3. git checkout -b feature/nama-fitur
4. Kerjakan, commit, push.
5. Buat PR ke develop.
6. Setelah CI pass + review, merge.

Hotfix:
1. git checkout main
2. git pull origin main
3. git checkout -b hotfix/nama-fix
4. Kerjakan, commit, push.
5. Buat PR ke main.
6. Setelah merge ke main, cherry-pick ke develop.

Rilis:
1. PR dari develop ke main.
2. Tag versi: v0.0.X-nama.
3. Push tag ke origin.

## Branch Protection Rules (GitHub)

Branch main:
- Require PR before merging.
- Require status checks to pass.
- Require branches to be up to date before merging.
- Require conversation resolution.

Branch develop:
- Require PR before merging (opsional, kalau tim lebih dari 1).
- Require status checks to pass (setelah CI ready).
