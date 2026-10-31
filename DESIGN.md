---
name: MusicForge
description: An artwork-led operational interface for a self-hosted music library.
colors:
  primary: "#e6213c"
  primary-hover: "#cf1e36"
  primary-soft: "color-mix(in srgb, var(--accent) 8%, var(--surface))"
  canvas: "#f8f9fa"
  surface: "#ffffff"
  subtle: "#f1f3f5"
  ink: "#14151a"
  muted: "#616571"
  line: "#e4e7ec"
  success: "#15803d"
  success-soft: "rgba(21, 128, 61, 0.08)"
  warning: "#b45309"
  warning-soft: "rgba(180, 83, 9, 0.08)"
  danger: "#b91c1c"
  danger-soft: "rgba(185, 28, 28, 0.08)"
  info: "var(--accent)"
  info-soft: "var(--accent-soft)"
  dark-canvas: "#0b0c0e"
  dark-surface: "#121317"
  dark-subtle: "#191b20"
  dark-ink: "#f3f4f6"
  dark-muted: "#989da8"
  dark-line: "#23262e"
  dark-primary: "#f06f81"
  dark-primary-hover: "#cf1e36"
  dark-primary-soft: "color-mix(in srgb, var(--accent) 16%, var(--surface))"
  dark-primary-ink: "#ffffff"
  dark-success: "#86efac"
  dark-success-soft: "rgba(34, 197, 94, 0.11)"
  dark-warning: "#fcd34d"
  dark-warning-soft: "rgba(245, 158, 11, 0.12)"
  dark-danger: "#fca5a5"
  dark-danger-soft: "rgba(239, 68, 68, 0.12)"
  dark-info: "var(--accent)"
  dark-info-soft: "var(--accent-soft)"
typography:
  headline:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: "0"
  title:
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "0"
  body:
    fontSize: "13px"
    lineHeight: 1.55
    letterSpacing: "0"
  label:
    fontSize: "12px"
    fontWeight: 400
    letterSpacing: "0"
  mono:
    fontFamily: '"SFMono-Regular", Consolas, monospace'
    fontSize: "12px"
rounded:
  card: "14px"
  artwork: "12px"
  control: "8px"
spacing:
  compact: "8px"
  control: "12px"
  group: "16px"
  section: "24px"
  page: "28px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "var(--accent-ink)"
    rounded: "{rounded.control}"
  button-primary-hover:
    backgroundColor: "{colors.primary-hover}"
  button-outline:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "4px 11px"
---

# Design System: MusicForge

## Overview

Studio is the active direction, documented in [the UI specification](docs/ui-refinement.md).
It uses a refined rounded glassmorphism visual language: an atmospheric ambient canvas,
frosted glass surfaces with backdrop blur (`backdrop-filter: blur(...)`), subtle specular borders,
harmonious rounded corners, and compact ergonomic control layouts. The [A2](designs/variant-a2-canvas.html),
[A1](designs/variant-a1-compact.html), [A3](designs/variant-a3-sheet.html) and
[Flat Studio preview](docs/ui-refinement-preview.html) remain historical explorations.
Actual application screenshots from CI are the current visual evidence.

**Key Characteristics:**
- Real artwork with explicit missing-image fallbacks.
- Frosted glass layered surfaces, translucent cards, and persistent operational feedback.
- Equivalent light/dark surfaces and bilingual content.

## Colors

Apple Music red is the default fill. Spotify green, violet, amber and custom
colors remain available with unchanged browser preferences. Accent text and
button foregrounds adapt for contrast in both appearances. Green, amber and red
identify success, waiting and failure; activity follows the chosen accent.
Status always includes text. Theme values live in `web/src/style.css`, with
contrast adaptation in `theme-selector.tsx`.

**The Status Rule.** Never communicate artifact or job state by color alone.

## Typography

Use the system sans-serif stack for operational content and a monospace stack
only for paths, endpoints and diagnostics. Page headings use the headline role,
section titles the title role, and compact metadata the label or smaller role.
Page headings use a fixed 18px size, including mobile. Metric values use a
26px monospace face on desktop and 24px on phones.
Counters and durations use tabular numerals. Long names and paths wrap.
Pipeline stages lead with their headings, without repeated above-heading labels.

## Layout

Desktop has a fixed 232px sidebar, a fluid canvas and 28px horizontal insets.
Manual collapse retains a 76px icon rail. At 800px navigation is icon-only
(72px); at 600px it moves to a 60px bottom dock with four equal tracks.
Phone content reserves 76px plus the safe-area inset below the final item.
The floating frosted header spans the main area with backdrop blur, with title and actions in one row on
desktop. Phone overview actions wrap below the title to preserve readable text.
A compact top-right disclosure keeps appearance and language controls available
without adding a separate header row. Insets reduce to 20px, 16px and 14px.
The overview groups real build completion and active tracks above the recent
jobs and lyric/storage cards. Settings use two columns of stacked labels and
fields on desktop, and one column on narrow screens.

Library cover grids use square media and adaptive minimum tracks (100px small,
144px medium, 190px large). Cover size and grid/list controls live in the view
options disclosure; search shares the desktop toolbar and fills narrow screens. Phone grids use four/three/two columns respectively; small
grids use five columns on wider tablets. Cover size and grid/list preferences
persist in browser storage and remain available when returning to desktop.
Desktop list view aligns selection, artwork, album/artist, tracks/duration,
source audio, output, lyrics and status with one shared column header. Narrow
list rows wrap these real cached values into a cover-and-details layout. Missing
values remain unknown; mixed source specifications remain visible. Album details
use cover/metadata columns and a fixed-layout track table. Smaller screens hide
redundant artist/duration columns. Collapsed song rows show title, duration, actual
source audio/bitrate and source/output sizes; full paths, hashes and raw tags
stay in expanded details. Song details expand directly below their track on desktop and phones. Desktop
lyric selection stays inline; phone lyric actions open a separate browser tab
with the current album/filters/search. Candidate lists and lyric text scroll
independently while the preview and confirmation stay visible. A manual
instrumental choice previews and saves an owned local placeholder LRC. Task details expand within their task. Detail
drawers, overlays and full-screen inspectors are not used. Compact right-side
actions use localized icon names and title tooltips. Closing returns focus to
the current visible track; filter changes close the expansion, while polling
and reauthentication preserve lyric drafts.

## Elevation & Depth

Operational sections use translucent frosted glass surfaces (`--glass-surface` with `backdrop-filter: blur(20px) saturate(180%)`),
paired with subtle specular borders (`--glass-border`) and soft luminous elevation (`--shadow-card`).
Interactive elements feature subtle hover-lift micro-interactions (`translateY(-2px)`).
The cover grid stays on the atmospheric canvas, with 12px artwork corners and gentle lift on hover.
Toasts, the floating capsule batch-selection bar, and the sticky capsule save bar float over the content
with elevated frosted glass layers (`--glass-surface-elevated`) and diffused floating shadows (`--shadow-float`).

## Shapes

Geometry follows a cohesive rounded hierarchy:
- Primary interactive controls and inputs use 8px rounded corners (`--radius-control: 8px`).
- Artwork covers use refined 12px rounded corners (`--radius-artwork: 12px`).
- Operational panels, bento metric cards, and dialogs use 14px rounded corners (`--radius-card: 14px`).
- Badges, status pills, segmented switcher tabs, the search input capsule, and floating action docks use full pill geometry (`--radius-pill: 999px`).
Dots indicate status; square initials identify the administrator.
Repeated library items are unframed, not nested cards.

## Components

### Buttons And Inputs

Lucide icons name tools, paired with text for explicit commands. Icon-only
controls have localized names and native title tooltips. Primary buttons use
the accent with a specular top highlight, secondary buttons use a translucent surface and stroke, and destructive buttons
use danger tones. Focus uses a visible two-pixel accent outline. Controls retain
loading/disabled states, 32px standard and 28px compact controls. Phone track, lyric, detail and save
actions retain at least 40px touch targets.

Library filter and sort controls use a native details disclosure with a
localized icon summary on all screen sizes. Choosing an option closes it;
Escape closes it and restores focus to the summary. Density and grid/list
controls share the disclosure with filtering and sorting.

### Navigation

The active destination combines accent color, a luminous translucent pill background and
`aria-current`. Library hierarchy uses breadcrumbs and query-string URLs.
Theme and language selections preserve mounted forms, filters and selection.

### Library Artwork

Album imagery comes from authenticated, registered output covers. Artist
imagery is a mosaic of distinct album covers, not a portrait. Images lazy-load,
reserve their square footprint and fall back without blocking the library.
Successful loading resolves a short blur/opacity transition once per image;
polling retains the same image elements and does not repeat the reveal.

### Feedback

Retain loaded data during polling failure and show a retryable warning.
Distinguish waiting work from terminal failure. Confirmed session expiry locks
the background with a focused reauthentication dialog; temporary verification
errors do not remove the page. Technical diagnostics remain available.
Error toasts remain until dismissed rather than timing out; success toasts
expire after six seconds. Both retain an explicit dismiss control.

Failed native FLAC tracks without an existing playable output offer an
individually confirmed best-effort repair in the album track table. Successful
audio enters the normal output hierarchy and Navidrome scan flow, while an amber
"Possibly damaged" badge remains filterable in MusicForge across scans, moves
and restarts. Recovery diagnostics retain the original strict failure.
Confirmation names audio risks, normal import and preserved originals/outputs.
Running operations expose cancel; imported artifacts have no disposable-preview
download or removal controls. Strict rebuild success clears the warning, not a
UI dismissal. Controls wrap inside the existing artifact column, while repair
diagnostics expand across the full track table below their track rather than
wrapping hashes inside the narrow status column. Both retain mobile touch
targets and use neighboring bilingual labels and theme tokens.

The focal motion carries the clicked album cover into its detail header with a
320ms native View Transition. It is progressively enhanced, never required for
navigation, and not replayed for polling, search or sorting. Supporting feedback
uses 100-160ms control press/selection changes, 240ms image loading and toast
entrance, and progress-width interpolation. Reduced-motion preference skips
shared artwork transitions and disables CSS animation without removing feedback.

## Do's and Don'ts

### Do:
- Do preserve real artwork proportions and reserve media dimensions.
- Do use labels, keyboard focus and indeterminate partial-selection states.
- Do keep long translated text and file paths inside their layout.
- Do retain state and provide recovery during temporary failures.
- Do close compact library options on selection and restore summary focus on Escape.
- Do keep density choices on desktop and mobile while preserving stored density and layout values.
- Do retain error toasts until dismissal and expire success toasts after six seconds.
- Do lead pipeline stages with headings, without redundant above-heading labels.

### Don't:
- Don't invent artist portraits or playback features.
- Don't use decorative hero sections or nested floating cards for operations.
- Don't hide diagnostics or automatically replay rejected writes.
