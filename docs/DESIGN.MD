# DESIGN.md — E-Tix Flash Design System

> **Status:** synthesized from the FANG research brief (Vercel, Stripe, Linear,
> Apple HIG, Microsoft Fluent) plus the landing-page specification.
> The canonical blueprint text was not attached to the request, so this file is
> the reconstruction to code against. Replace this file verbatim if the original
> blueprint is supplied. Last updated: 2026-11-13.

## 1. Design Position

Minimalis, premium, "FANG-level". The interface should look like infrastructure,
not like a marketing brochure: light surfaces, one accent, hairline borders,
generous whitespace, typographic hierarchy instead of decoration.

**Prinsip:**

1. **One accent, used sparingly.** `#D08080` marks action and state only. It is
   never a background wash, never a gradient.
2. **Borders over shadows.** Structure comes from 1px `#E2E8F0` lines. Shadows are
   avoided; depth is not simulated.
3. **Typography carries the brand.** Weight and tracking do the work that
   illustrations and emoji do in lesser designs.
4. **Quiet by default, loud on interaction.** Hover and focus are the only
   animated states; nothing moves on page load.
5. **No AI-slop.** No gradients, no glassmorphism, no blobs, no 3D icons, no emoji
   in UI copy, no exclamation marks, no "revolutionize/unleash/elevate" copy.

## 2. Color Tokens

| Token | Hex | Use |
|---|---|---|
| `--accent` | `#D08080` | Primary action bg, active icon, focus ring, brand dot |
| `--accent-hover` | `#C47070` | Hover state of accent surfaces |
| `--accent-tint` | `#FAF2F2` | Optional selected/soft accent surface |
| `--ink` | `#171717` | Headings, primary text, text on accent |
| `--body` | `#4D4D4D` | Body copy, nav links, secondary labels |
| `--muted` | `#6B7280` | Meta text where AA is required |
| `--muted-soft` | `#8F8F8F` | Footer/meta text (decorative, see §8) |
| `--border` | `#E2E8F0` | All 1px borders and dividers |
| `--surface` | `#FAFAFA` | Page and navbar background |
| `--surface-raised` | `#FFFFFF` | Cards and content bands |

### Measured contrast (WCAG 2.1, computed 2026-11-13)

| Pair | Ratio | AA normal text (4.5:1) | Non-text UI (3:1) |
|---|---|---|---|
| `#FFFFFF` on `#D08080` | 2.97:1 | fail | fail |
| `#171717` on `#D08080` | 6.05:1 | pass | pass |
| `#4D4D4D` on `#FAFAFA` | 8.10:1 | pass | pass |
| `#6B7280` on `#FAFAFA` | 4.63:1 | pass | pass |
| `#8F8F8F` on `#FAFAFA` | 3.10:1 | fail | pass |
| `#171717` on `#FAFAFA` | 17.18:1 | pass | pass |

**Consequence:** accent buttons use `#171717` labels, not white. White on the
accent is forbidden for text. `#D08080` on `#FAFAFA` (2.84:1) is also not used as
text color — only as border, icon, or filled surface.

## 3. Typography

Font: **Inter** (`next/font/google`, `subsets: ["latin"]`, `display: "swap"`).

| Role | Size / line-height | Weight | Tracking |
|---|---|---|---|
| Display (H1) | 48px → 72px (`text-5xl md:text-7xl`) | 600 | `tracking-tight` |
| Section (H2) | 30px (`text-3xl`) | 600 | `tracking-tight` |
| Card title | 14px (`text-sm`) | 600 | normal |
| Body | 16px (`text-base`), `leading-relaxed` | 400 | normal |
| Small / nav | 14px (`text-sm`) | 400–500 | normal |
| Meta / footer | 12–14px | 400 | normal |

Rules: never more than two weights per screen region. No italics. No uppercase
labels except a 12px badge. Sentence case everywhere.

## 4. Spacing & Layout

- **Spacing scale:** 4px base, use Tailwind steps 1–24 (4, 8, 12, 16, 24, 32, 48, 64, 96px).
- **Container:** `mx-auto w-full max-w-6xl px-6`.
- **Vertical rhythm:** navbar `h-16`, hero `py-24`, content bands `py-20`, footer `py-8`.
- **Grid:** features `grid gap-6 sm:grid-cols-2 lg:grid-cols-3`.
- **Breakpoints:** `sm` 640, `md` 768, `lg` 1024. Mobile-first; no horizontal scroll at 320px.

## 5. Radius, Border, Elevation

| Token | Value | Use |
|---|---|---|
| `--radius-sm` | 6px (`rounded-md`) | Buttons, badges |
| `--radius-lg` | 12px (`rounded-xl`) | Cards, panels |
| `--border-width` | 1px | Every divider and card edge |
| `--shadow` | none | Not used |

`rounded-full` is reserved for status dots and avatars only — never for buttons.

## 6. Components

### Navbar
`h-16` (64px), `bg-[#FAFAFA]`, `border-b border-[#E2E8F0]`.
Left: wordmark `E-Tix Flash`, `text-base font-bold tracking-tight text-[#171717]`.
Center (hidden below `md`): links `text-sm text-[#4D4D4D]`, hover `text-[#171717]`, gap-8.
Right: ghost `Login` + solid accent `Get Started`, both `h-9`, `rounded-md`, `text-sm`.

### Button

| Variant | Classes | Notes |
|---|---|---|
| Primary | `bg-[#D08080] text-[#171717] hover:bg-[#C47070]` | 36px tall, 16px horizontal padding, semibold |
| Ghost | `text-[#171717] hover:bg-[#F1F2F4]` | No border by default; optional `border border-[#E2E8F0]` on light bands |
| Link | `text-[#4D4D4D] hover:text-[#171717]` | Inline navigation |

All buttons: `inline-flex items-center justify-center gap-2 rounded-md`,
`transition-colors duration-150`,
`focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#D08080] focus-visible:ring-offset-2`.

### Badge
`inline-flex items-center gap-2 rounded-md border border-[#E2E8F0] bg-white px-3 py-1 text-xs font-medium text-[#4D4D4D]`,
with a `h-1.5 w-1.5 rounded-full bg-[#D08080]` dot. Used for release/status markers.

### Card
`rounded-xl border border-[#E2E8F0] bg-white p-6 transition-colors hover:border-[#D08080]`.
Icon `h-5 w-5 text-[#D08080]`, title `mt-4 text-sm font-semibold text-[#171717]`,
description `mt-2 text-sm leading-relaxed text-[#4D4D4D]`.
Cards do not lift, scale, or cast shadows on hover — only the border color changes.

### Footer
`border-t border-[#E2E8F0] py-8 text-sm text-[#8F8F8F]`, container-aligned,
copyright left and secondary links right.

## 7. Motion

- Property: color/border-color only. Duration 150ms, `ease-out`.
- No entrance, parallax, or scroll-triggered animation.
- Respect `prefers-reduced-motion`: motion is already minimal; do not add more.

## 8. Accessibility

- Focus: 2px ring `#D08080` with 2px offset on every interactive element; never `outline: none` without a replacement.
- Hit targets: minimum 36×36px for compact controls, 44×44px for primary mobile actions.
- Semantics: one `h1` per page, landmarks (`header`/`main`/`footer`), nav links are real anchors.
- Known exception: footer text `#8F8F8F` is 3.10:1 on `#FAFAFA` — below AA for
  small text. It is kept because the specification fixes that value for footer
  meta only. Use `#6B7280` (4.63:1) anywhere the text carries information.
- Accent is never the only signal: pair color with text (e.g. status label), not color alone.

## 9. Prohibited

Gradients of any kind, colored glows, drop shadows thicker than 1px, `rounded-full`
CTAs, emoji in UI, icon illustrations, auto-playing motion, centered paragraphs
longer than two lines, more than one accent hue, stock photography.

## 10. Code Map

| Layer | File |
|---|---|
| Tokens (Tailwind arbitrary values) | `frontend/app/page.tsx`, `frontend/app/layout.tsx` |
| Font | `frontend/app/layout.tsx` (`Inter`) |
| Global base | `frontend/app/globals.css` |
| Landing page | `frontend/app/page.tsx` |
