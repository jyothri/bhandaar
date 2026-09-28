# UI Refresh: One Look for Every Page

**Status:** proposed. Written 2026-09-28, after [browse.md](browse.md) was built.

## Problem

The UI grew a page at a time, each styled on the spot:
- **No shared look.** Buttons are blue in three different ways (`bg-blue-500` with bold text on Request, `bg-blue-700` on Login, bordered on Browse), links are plain `underline`, and panels are `border-8 border-gray-200` boxes.
- **No shell.** A large centred "Storage Manager Web App" heading, then bare text links for the nav, then an `<hr>`. The user and Log out float under the heading.
- **Forms are two-column grids** of right-aligned labels (`justify-self-end`) that don't reflow on a phone.
- **Tables** (`Table.tsx`) are wide, with `px-6 py-4` cells and fractional widths (`w-7/8`, `w-5/8`), and scroll sideways on a phone.
- **Nothing shows size at a glance.** Browse lists numbers, and a folder's share of its parent has to be worked out.

## Goals

- One clean app shell and one set of components, used by every page: Browse, Request, Request History, a scan's results, and Login.
- Light and dark, both designed, following the system theme, with one blue accent.
- Every page fully usable at 375 px wide.
- Storage at a glance in Browse: size bars, file-type icons, and a summary card per source.
- No new runtime dependency: Tailwind (already v4) and a few components of our own.

Out:
- Behaviour changes. Every page does what it does now, with the same routes, search params and API calls. The one exception: the backend adds a folder's own totals to `FolderPage` (see [Size bars](#size-bars)).
- A manual theme switch, charts, and an overview dashboard.
- Photos (still disabled, review item 7.15).

## Design tokens

Defined once in `App.css` with Tailwind v4's `@theme`, as CSS variables, and redefined for dark under `@media (prefers-color-scheme: dark)`. Components use the tokens, never raw palette classes (`bg-surface`, not `bg-white dark:bg-gray-900`; classes read `bg-canvas`, `border-line`, `text-fg`, `text-muted`), so each theme is fixed in one place.

| Token | Light | Dark | For |
|---|---|---|---|
| `canvas` | gray-50 | gray-950 | page background |
| `surface` | white | gray-900 | cards, bar, menus |
| `surface-muted` | gray-100 | gray-800 | table headers, hover, tracks |
| `line` | gray-200 | gray-800 | dividers, card and input borders |
| `fg` | gray-900 | gray-100 | body |
| `muted` | gray-500 | gray-400 | secondary: dates, counts, hints |
| `accent` | blue-600 | blue-400 | links, selection, focus ring, size bars |
| `accent-solid` | blue-600 | blue-600 | filled primary buttons (white text in both themes), `-hover` blue-700 |
| `accent-soft` | blue-50 | blue-950 | selected tab and row backgrounds |
| `danger` | red-600 | red-400 | errors, failed |
| `danger-solid` | red-600 | red-600 | filled danger buttons |
| `success` | green-600 | green-400 | completed, the Mask switch when on |
| `warning` | amber-600 | amber-400 | running, "updating totals…" |

Every text and background pair meets WCAG AA contrast (4.5:1 for body text) in both themes. The spec's PRs check this with the browser's contrast checker.

**Type:** the system font stack; 14 px base (`text-sm`) for tables and trees, 16 px for body; page titles `text-xl font-semibold`, card titles `text-base font-semibold`. Sizes and counts use `tabular-nums` so columns line up.

**Spacing and shape:** a 4 px grid (Tailwind's default); cards `rounded-lg`, with `p-4` (`p-3` on phones); inputs and buttons `rounded-md`, 36 px high (40 px on touch screens, via `pointer-coarse:`); one shadow (`shadow-sm`) for cards and menus.

**Motion:** 150 ms transitions on colour and transform only, and none under `prefers-reduced-motion`.

## Components

In `ui/src/components/ui/`, one file each, styled with the tokens:

| Component | Replaces | Notes |
|---|---|---|
| `Button` | the ad-hoc blue buttons | `variant`: primary (accent), secondary (bordered), ghost, danger; `size`: sm, md; `loading` shows a spinner and disables |
| `Card` | `border-8` panels | title, optional actions on the right, body |
| `Field` | label cells in form grids | label above the control on phones, beside it from `sm`; hint and error text under it, linked by `aria-describedby` |
| `Input`, `Select`, `Checkbox` | `Input.tsx`, bare `<select>`/checkboxes | one height and border; `Select` keeps the native `<select>`, which phones handle best |
| `Tabs` | Browse's service buttons, Request's scan-type radios | `role="tablist"`, arrow keys move between tabs; each tab can carry a sub-line (totals) and be disabled |
| `Switch` | the Mask switch | the existing `role="switch"` look, generalised |
| `Badge` | status text | Completed, Running, Failed, Interrupted, "linked copy" |
| `Table` | `Table.tsx` | compact cells (`px-3 py-2`), sticky header, right-aligned numbers; below `sm` each row becomes a stacked card (see [Phones](#phones)) |
| `Pager` | the two hand-written pagers | Previous · Page n of m · Next, above the table it pages |
| `EmptyState`, `ErrorState`, `Skeleton` | "Loading…" and red `<p>`s | one message style; skeleton rows while a table or tree loads |
| `Icon` | — | inline SVGs, `currentColor`, 16 and 20 px; the set in [File-type icons](#file-type-icons) plus a few for the UI (chevron, external link, menu, user, check, warning) |

`Breadcrumbs`, `FolderTree`, `Masked` and `ScanProgress` stay and are restyled with these components.

## App shell

A slim top bar on `surface`, 56 px high, with a bottom border, above a centred content column (`max-w-6xl`, `px-4`):

```
 ┌──────────────────────────────────────────────────────────────────────┐
 │ ▣ Bhandaar   Browse   Request   Request History          jyothri ▾ │
 └──────────────────────────────────────────────────────────────────────┘
   Browse › jyo****ri@gmail.com › Google Drive › My Drive
   ┌ page content ─────────────────────────────────────────────────────┐
```

- **Name:** "Bhandaar", with a small logo mark, linking to Browse. The page `<title>` is `Bhandaar`, or `<page> · Bhandaar`; a new favicon to match the mark.
- **Nav:** Browse · Request · Request History. The active tab gets the accent colour and a 2 px underline; a scan's page counts as Request History, as now.
- **User menu:** the username, opening a small menu with Log out. The menu closes on Escape and on a click outside.
- **Phones:** the tabs move into a menu behind a ☰ button, next to the name; the user menu stays.
- **Trail:** the breadcrumbs sit under the bar, in `muted`, on every page that has one.
- The Login page and the OAuth callback have no nav: just the name, centred above their card.

## Pages

### Browse

```
 ┌ jyo****ri@gmail.com ▾ ┐                              Google accounts · Drives
 ┌──────────────────────────────────────────────────────────────────────┐
 │ ☁ jyo****ri@gmail.com                                                │
 │ 3.7 GB · 504 files          Updated 2h ago                           │
 └──────────────────────────────────────────────────────────────────────┘
 [ Google Drive 504 · 3.7 GB ] [ Gmail 5,744 · 493 MB ] [ Photos (soon) ]
 ┌──────────────────────────────────────────────────────────────────────┐
 │ ▸ 📁 from Apple Mac        ████████████████████  3.7 GB   402 files │
 │   📄 notes.txt             ▏                     2.0 KB   Sep 1     │
 └──────────────────────────────────────────────────────────────────────┘
```

- **Source picker:** a `Select` with the two groups, as now, in a row above the summary card.
- **Source summary card:** one per selected source.
  - A Google account: its name, then for the selected service the total size (large) and file or message count, and "Updated <ago>". A service not granted or not scanned shows the reason and a primary button to the Request page instead of totals.
  - An agent drive: name and host, total size and file count, "Updated <ago>" (last sync), a "Linked copy of physical drive N" badge when linked, and the status line (`<details>`) as its footer.
  - "Updating totals…" becomes a `warning`-coloured badge on the card.
- **Service tabs:** `Tabs`, under the source picker and above the summary card, each with its totals as a sub-line; Photos disabled with "Soon".
- **Tree:** in a `Card`. Rows are 36 px high, 40 px on touch, with a hover background. Each row: chevron (folders), icon, name, size bar, size, then file count (folders) or modified date (files). "More" is a secondary `Button` under the rows. The Drive ↗ link becomes the external-link icon, shown on row hover and always on touch.
- **Gmail:** the list in a `Card`, with the sort as a two-item segmented control and the Mask `Switch` in the card's header, and a `Pager` above the table.

### Request

- The scan type (Gmail, Google Drive) becomes `Tabs` at the top of one `Card`, under a "New request" heading.
- The account row: the account `Select`, and "Link another Google account" as a secondary `Button`. A service the account hasn't granted shows in a tinted box, with "Grant … access" as the primary button.
- Every row is a `Field` (or, for checkboxes, a `FieldGroup`), with its label beside the control from `sm` and above it on phones:
  - Gmail: Messages (Inbox, Unread), Date range, and Query filter.
  - Drive: Files (Owned by me, Include trash), File types (a wrapping row of `Checkbox`es), Modified (hint: dates are in UTC), Folder (hint on what to paste; Include subfolders beside it), and Query.
- The queries show in monospace, read-only until "Edit query" is ticked, as now.
- "Submit" is the primary `Button`, full width on phones, in the card's footer, with the result beside it: `danger` with a warning icon, or `success` with a check and "View results".
- Live progress (`ScanProgress`) becomes a `Card` under the form, titled "Scan N" with a status `Badge` (Running, Completed, Failed). It shows the bar (indeterminate until a percentage arrives), or the outcome that replaces it, then Elapsed, Processed and Processing as figures.
- The OAuth callback's error (linking cancelled, or a state mismatch) is a small card with "Try again" as the primary button.

### Request History

- A "Request History" heading, then a `Card` with the account `Select`, labelled "Select an account" beside it (above it on phones), so the label and the dropdown share a row.
- The chosen account's scans in a "Scans" `Card`, as a `Table`: Scan (a link; "Scan N" as the card title on phones), Type, Filter (monospace, with the folder for folder scans), Started, Duration, and Status as a `Badge` (a spinner while Running). The account's masked name, already chosen above, is no longer a column. Polling is unchanged.

### A scan's results

- The summary is a `Card` titled "Scan N · Google Drive", with the status `Badge` and "Browse this account's …" as a small secondary button in its header, and the fields as a definition list (one column on phones; queries in monospace).
- Files and folders, or new messages, in a second `Card`: `Pager` above a `Table`. Drive and local rows get file-type icons (folders in `accent`), with Name as the title on phones. Messages keep the Mask switch in their card's header.

### Login

- A centred `Card` (`max-w-sm`) on `canvas`, with the name and mark above it: Username, Password, and a full-width primary "Log in" button with a spinner while it signs in.
- Errors show in the card, in `danger`, with `role="alert"` as now; "Too many attempts" gives its wait time.

## Storage at a glance

### Size bars

Each row in the tree gets a thin bar (4 px high, `rounded-full`, `accent` on a `surface-muted` track) filled to the row's share of the folder it's in: its bytes divided by that folder's total bytes.

The UI can't work out that total itself: a page holds only 200 entries. So the backend adds the folder's own totals to `FolderPage`:
- `totals: {files, bytes}`, from the same cache as its subfolders' (`browse_folder_totals`), or added up live before the first build.
- At a Drive account's roots (`folder` empty), the account's total, which the roots already split.

A row under 0.5% gets a 2 px sliver, so it still reads as present. The bar is decorative (`aria-hidden`); the size next to it is the value. On phones the bar moves under the name, full width.

### File-type icons

A small inline-SVG set in `Icon`, chosen by MIME type for Drive (`mime_type`) and by extension for agent files:

| Icon | Drive MIME types | Extensions |
|---|---|---|
| folder | `application/vnd.google-apps.folder` | (folders) |
| image | `image/*` | jpg, jpeg, png, gif, heic, webp, tiff, raw, cr2, nef |
| video | `video/*` | mp4, mov, m4v, avi, mkv, m2ts, mts, wmv |
| audio | `audio/*` | mp3, m4a, wav, flac, aac, ogg |
| document | `application/pdf`, Google Docs, Word | pdf, doc, docx, txt, rtf, pages, md |
| sheet | Google Sheets, Excel, CSV | xls, xlsx, csv, numbers |
| slides | Google Slides, PowerPoint | ppt, pptx, key |
| archive | zip, tar, gzip types | zip, tar, gz, tgz, 7z, rar, dmg, iso |
| code | JSON, HTML, JavaScript types | json, html, js, ts, py, go, java, xml, sh |
| file | anything else | (anything else) |

The mapping is one table in `ui/src/fileTypes.ts`, with unit tests. Icons are `muted`, and folders are `accent`. They're decorative too: the name carries the meaning.

### Source summary cards

See [Browse](#browse). The card's totals are the ones `/api/browse/sources` already returns, and its "Updated" time is `updated_at`.

## Phones

Every page works at 375 px wide with no sideways page scroll:
- The shell's tabs move into the ☰ menu.
- Forms are one column, with labels above their controls.
- **Tables become stacked cards** below `sm`. Each row is a small card: the main cell (name, subject, or scan) as its title, then the rest as label and value lines. Wide values (paths, queries, senders) wrap (`wrap-anywhere`).
- **The tree** keeps its rows but drops the date column. Size and count stack on the right, and the bar sits under the name. Indentation is 12 px per level, instead of 20.
- Tap targets are at least 40 px high.
- The source picker and service tabs scroll sideways inside their own row when they don't fit, never the page.

## Accessibility

- A visible focus ring (2 px `accent`, offset) on everything focusable but text inputs, which instead get a darker border and a faint `accent` halo (a full ring beside the text looked too loud). Nothing relies on hover alone: the Drive link and masked cells have tap and keyboard equivalents, as now.
- The existing roles stay: tablist, tree and treeitem, switch, alert, and the `aria-current` trail. `Tabs` add arrow-key movement.
- Colour is never the only signal: statuses carry their word, and the switch its position.

## Testing

- The route tests find elements by role and name, so they keep passing as markup changes. Where one relies on markup (the tree's summary element), it's updated to use roles.
- New unit tests: `fileTypes.ts` (MIME and extension mapping), the size-bar share (including the sliver and a folder of 0 bytes), and `Tabs`' keyboard movement.
- Backend: `FolderPage.totals` in `be/db/browse_test.go`, cached and live.
- By hand on dev.sm, per PR: every page in both themes, at 375 px and at desktop width, keyboard only once.

## Implementation order

One PR each, each checked on dev.sm:

1. **Foundation:**
   - the tokens in `App.css`
   - the `ui/` components and icons
   - the app shell: bar, nav, user menu, phone menu
   - name, title and favicon
   - Login restyled
2. **Browse:**
   - `FolderPage.totals` in the backend
   - the summary card and tabs
   - the tree with icons and size bars
   - the Gmail card
3. **Request:** the tabs, fields and progress card.
4. **Request History and scan results:** the tables as stacked cards on phones, badges, and the summary card.
5. **Clean-up:** remove `Input.tsx`, the old `Table.tsx` styles and any raw palette classes left, and update `CLAUDE.md` and `docs/architecture.md`.

## Decisions

Made 2026-09-28:
- **Direction:** a clean app shell with a neutral palette and one accent.
- **Components:** Tailwind with a few components of our own; no new UI library.
- **Navigation:** a top bar; a menu on phones.
- **Name:** Bhandaar.
- **Theme:** follows the system, light and dark; accent blue; no manual switch.
- **Phones:** every page fully usable.
- **Extras:** size bars in the tree, file-type icons, and source summary cards.
- **Scope:** Browse, Request, Request History and scan results, and Login.
