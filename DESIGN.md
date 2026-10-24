---
name: MusicForge
description: An artwork-led operational interface for a self-hosted music library.
colors:
  primary: "#e6213c"
  primary-hover: "#cf1e36"
  primary-soft: "color-mix(in srgb, var(--accent) 8%, var(--surface))"
  canvas: "#f6f7f9"
  surface: "#ffffff"
  subtle: "#f0f2f5"
  ink: "#111215"
  muted: "#646874"
  line: "#e2e4ea"
  success: "#15803d"
  success-soft: "rgba(22, 101, 52, 0.07)"
  warning: "#b45309"
  warning-soft: "rgba(180, 83, 9, 0.07)"
  danger: "#b91c1c"
  danger-soft: "rgba(185, 28, 28, 0.07)"
  info: "var(--accent)"
  info-soft: "var(--accent-soft)"
  dark-canvas: "#09090b"
  dark-surface: "#121215"
  dark-subtle: "#19191e"
  dark-ink: "#f5f5f7"
  dark-muted: "#9e9ea8"
  dark-line: "#23232b"
  dark-primary: "#f06f81"
  dark-primary-hover: "#cf1e36"
  dark-primary-soft: "color-mix(in srgb, var(--accent) 16%, var(--surface))"
  dark-primary-ink: "#ffffff"
  dark-success: "#86efac"
  dark-success-soft: "rgba(34, 197, 94, 0.09)"
  dark-warning: "#e8c279"
  dark-warning-soft: "#342d1d"
  dark-danger: "#fca5a5"
  dark-danger-soft: "rgba(239, 68, 68, 0.12)"
  dark-info: "var(--accent)"
  dark-info-soft: "var(--accent-soft)"
typography:
  headline:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.3333333
    letterSpacing: "0"
  title:
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "0"
  body:
    fontSize: "13px"
    lineHeight: 1.5714286
    letterSpacing: "0"
  label:
    fontSize: "12px"
    fontWeight: 400
    letterSpacing: "0"
  mono:
    fontFamily: '"SFMono-Regular", Consolas, monospace'
    fontSize: "12px"
rounded:
  card: "9px"
  artwork: "8px"
  control: "6px"
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

A2 Studio Canvas is the active direction, defined in [the current design](designs/index.html).
The [A2 copy](designs/variant-a2-canvas.html), [A1](designs/variant-a1-compact.html)
and [A3](designs/variant-a3-sheet.html) retain the original explorations. The app
implements the Canvas surfaces, sidebar, operational cards and rich library views
with indexed application data. Showcase controls, sample tracks and invented
worker/rate values from the standalone mock do not enter the production UI.

**Key Characteristics:**
- Real artwork with explicit missing-image fallbacks.
- Quiet bordered panels, compact artwork grids and persistent operational feedback.
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

Desktop has a fixed 236px sidebar, a fluid canvas and 28px horizontal insets.
Manual collapse retains a 76px icon rail. At 800px navigation is icon-only
(72px); at 600px it moves to a 64px bottom dock with four equal tracks.
Phone content reserves 76px plus the safe-area inset below the final item.
The surface header spans the main area, with title and actions in one row.
A compact top-right disclosure keeps appearance and language controls available
without adding a separate header row. Insets reduce to 20px, 16px and 14px.
The overview groups real build completion and active tracks above the recent
jobs and lyric/storage cards. Settings use two columns of stacked labels and
fields on desktop, and one column on narrow screens.

Library cover grids use square media and adaptive minimum tracks (95px small,
136px medium, 185px large), with small/medium/large segmented controls in a separate toolbar row above
full-width search. Phone grids use four/three/two columns respectively; small
grids use five columns on wider tablets. The medium layout shows seven columns
at 1440px and ten at 1920px. Cover size and grid/list preferences
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

Operational sections use flat surface panels with thin borders and 9px corners.
The cover grid stays on the canvas, with 8px artwork corners and no enclosing
card around each album. Toasts retain the shared soft shadow. Settings keep their save/discard footer
inside the same bordered form box in both saved and dirty states.

## Shapes

Artwork uses gently softened square corners (8px), and controls use restrained
corners (6px), including the segmented-control shell. Dots indicate status;
square initials identify the administrator.
Repeated library items are unframed, not nested cards.

## Components

### Buttons And Inputs

Lucide icons name tools, paired with text for explicit commands. Icon-only
controls have localized names and native title tooltips. Primary buttons use
the accent, secondary buttons use a surface and stroke, and destructive buttons
use danger tones. Focus uses a visible two-pixel accent outline. Controls retain
loading/disabled states, 32px standard and 24px compact controls. Phone page
actions use 36px heights; the library options trigger retains a 44px target.

Library filter and sort controls use a native details disclosure with a
localized icon summary on all screen sizes. Choosing an option closes it;
Escape closes it and restores focus to the summary. Density and grid/list
controls stay outside the disclosure.

### Navigation

The active destination combines accent color, a tonal background and
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
