---
name: MusicForge
description: An artwork-led operational interface for a self-hosted music library.
colors:
  primary: "#a53550"
  primary-hover: "#852b40"
  primary-soft: "#f8eaf0"
  canvas: "#f7f8fa"
  surface: "#ffffff"
  subtle: "#eff1f4"
  ink: "#202127"
  muted: "#646671"
  line: "#dce0e6"
  success: "#267447"
  success-soft: "#eaf5ee"
  warning: "#856015"
  warning-soft: "#fcf3df"
  danger: "#af3041"
  danger-soft: "#fbecef"
  info: "#326d9c"
  info-soft: "#eaf2fa"
  dark-canvas: "#141416"
  dark-surface: "#1b1b1f"
  dark-subtle: "#25252b"
  dark-ink: "#eeeef1"
  dark-muted: "#aaaab5"
  dark-line: "#37373f"
  dark-primary: "#ef9bb0"
  dark-primary-hover: "#ffc0ce"
  dark-primary-soft: "#36242d"
  dark-primary-ink: "#30161e"
  dark-success: "#88cea3"
  dark-success-soft: "#1f3027"
  dark-warning: "#e8c279"
  dark-warning-soft: "#342d1d"
  dark-danger: "#ffacb5"
  dark-danger-soft: "#39232a"
  dark-info: "#91bfe4"
  dark-info-soft: "#22313e"
typography:
  headline:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif'
    fontSize: "28px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "0"
  title:
    fontSize: "18px"
    fontWeight: 650
    lineHeight: 1.4
    letterSpacing: "0"
  body:
    fontSize: "14px"
    lineHeight: 1.5
    letterSpacing: "0"
  label:
    fontSize: "13px"
    fontWeight: 550
    letterSpacing: "0"
  mono:
    fontFamily: '"SFMono-Regular", Consolas, monospace'
    fontSize: "12px"
rounded:
  artwork: "8px"
  control: "6px"
spacing:
  compact: "8px"
  control: "12px"
  group: "16px"
  section: "24px"
  page: "36px"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.surface}"
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
    padding: "10px 12px"
---

# Design System: MusicForge

## Overview

An artwork-led, task-focused library workspace. Real output covers supply the
visual variety; controls stay compact, consistent and quiet. This records the
current source implementation, not user-approved branding or rendered approval.

**Key Characteristics:**
- Real artwork with explicit missing-image fallbacks.
- Unframed sections, clear hierarchy and persistent operational feedback.
- Equivalent light/dark surfaces and bilingual content.

## Colors

The primary accent is muted burgundy in light mode and pale rose in dark mode.
Neutral surfaces carry the interface; green, amber, red and blue communicate
success, waiting, failure and activity. Status always includes a text label.
Theme values map directly to the custom properties in `web/src/style.css`.

**The Status Rule.** Never communicate artifact or job state by color alone.

## Typography

Use the system sans-serif stack for operational content and a monospace stack
only for paths, endpoints and diagnostics. Page headings use the headline role,
section titles the title role, and compact metadata the label or smaller role.
Mobile page headings use the implemented fixed size (25px), not viewport scaling.
Counters and durations use tabular numerals. Long names and paths wrap.
Pipeline stages lead with their headings, without repeated above-heading labels.

## Layout

Desktop has a fixed sidebar (216px) and an unframed content region with a maximum
width (1540px). At 1100px the sidebar contracts (192px); at 800px navigation is
icon-only (72px); at 600px it moves to the bottom with four equal tracks.
The mobile header wraps when appearance controls cannot fit beside the brand.
Page insets reduce from 36px to 24px to 16px.

Library cover grids use square media and adaptive minimum tracks (144px small,
180px medium, 232px large), then two equal columns at 800px and below. The medium
layout shows five columns at 1440px and seven at 1920px; columns are never fixed
to three. Desktop grids retain small/medium/large density choices; compact
two-column grids omit that inert control. Cover size and grid/list preferences
persist in browser storage and remain available when returning to desktop.
List view uses compact square artwork. Album details
use cover/metadata columns and a fixed-layout track table. Smaller screens hide
redundant artist/duration columns; file details remain accessible.

## Elevation & Depth

Sections are flat, separated by space and thin rules. Tonal layers distinguish
controls and feedback. The toast alone uses the offset, soft shadow defined in
the sidecar; page sections have no floating-card treatment.

## Shapes

Artwork uses gently softened square corners (8px), and controls use restrained
corners (6px), with an 8px outer segmented-control shell. Dots indicate status;
circular initials identify the administrator.
Repeated library items are unframed, not nested cards.

## Components

### Buttons And Inputs

Lucide icons name tools, paired with text for explicit commands. Icon-only
controls have localized names and native title tooltips. Primary buttons use
the accent, secondary buttons use a surface and stroke, and destructive buttons
use danger tones. Focus uses a visible two-pixel accent outline. Controls retain
loading/disabled states and mobile touch heights (44px).

At 800px and below, library filter and sort controls use a native details
disclosure with a localized icon summary. Choosing an option closes it; Escape
closes it and restores focus to the summary. Desktop options stay inline.

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
- Do keep density choices in desktop grids while preserving density and layout preferences.
- Do retain error toasts until dismissal and expire success toasts after six seconds.
- Do lead pipeline stages with headings, without redundant above-heading labels.

### Don't:
- Don't invent artist portraits or playback features.
- Don't use decorative hero sections or nested floating cards for operations.
- Don't hide diagnostics or automatically replay rejected writes.
