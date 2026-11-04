# MusicForge interface

MusicForge is a place to check progress and look after a music library. Keep the collection and the next useful action easy to find. The current interface uses album artwork, compact rows, rounded controls and translucent surfaces in light and dark themes.

The implemented tokens in [style.css](web/src/style.css) are the source for colors, spacing, sizes and motion. Use current Actions screenshots to review appearance. Do not maintain a second set of pixel values or a separate HTML mockup.

## Structure

- Reuse the shared page heading, controls, inline details and existing components. Keep one title/action area per page.
- Desktop navigation can collapse; phones use bottom navigation. Language, appearance and palette controls belong in the page heading. Account/security remains accessible in either layout.
- Library browsing keeps hierarchy, filters, sorting and selection visible. Use real album covers; artist imagery is an album-cover mosaic. Missing artwork must not block browsing.
- Track and task details expand next to their row. Phone lyric selection uses its existing separate tab with candidates, preview and confirmation in reach.
- Settings keep drafts when changing sections, language or theme, and use the shared save area. Long paths and translated labels must fit without hiding actions.

## Controls and feedback

- Use the accent for primary actions, restrained warning/error colors for problems, and text or icons as well as color to convey state.
- Icon-only controls need a localized accessible name and a useful tooltip. Preserve visible keyboard focus, partial selection, disabled/loading states and usable touch targets.
- Keep text contrast at least 4.5:1 for preset and custom accent colors. Theme changes preserve mounted state; invalid custom colors do not apply.
- Temporary network failures keep loaded data visible with a retry option. Confirmed session expiry locks the page for reauthentication without losing local form drafts. Do not silently repeat failed writes.
- Error notices stay until dismissed; success notices expire after six seconds. Keep technical diagnostics available without making them the main explanation.
- Artwork transitions enhance navigation but never delay it. Polling must not replay entrance animations. Honor reduced-motion preferences.

## Writing

Say what happened and what the user can do next. Prefer “Queued. Work will start when a slot is available” to references to workers or internal phases. Keep button labels specific: saving settings, searching again and replacing a file are different actions.

Use everyday language in both English and Chinese. Avoid slogans in operational hints, repeated explanations and release-history phrases such as “in this version.” Keep actual limits, destructive-action consequences and file-protection rules clear. Tags, paths and raw diagnostics retain their original text.

Translations live in [the two catalogs](web/src/locales). Keep keys and placeholders aligned. English `error.*` messages also map backend diagnostics in [i18n.tsx](web/src/i18n.tsx); changing them requires checking the backend wording, not just polishing the translation.

## Review

Follow [change-scoped acceptance](AGENTS.md#change-scoped-acceptance). Use representative desktop and narrow-screen screenshots, including the affected theme or long translation. Existing browser checks should protect navigation, density, overflow, focus and interaction; avoid tests that merely freeze decorative pixel values. Functional and persistence behavior is defined in the [behavior reference](docs/mvp-design.en.md).
