### feat(dashboard): sidebar visibility fix, Advanced Features section, global search, pins, icon rail and counts

Four changes to the sidebar and its Settings panel, on one foundation
(`web/src/lib/nav.ts`) and mostly in one file (`Sidebar.svelte`).

**The Sidebar card's visibility switches answer again.** PR #601 rewrote the
row's `<Toggle>` to `disabled={true}`, so every switch rendered greyed out and
swallowed the click — the preset dropdown still worked, which is what made the
panel look healthy. The same PR dropped a `</div>` in the Display card, leaving
the "Advanced settings" row as the parent of the provider-toggle row, the
animation dropdown and the Currency block.

- The switch is disabled only for the entry the panel lives behind
  (`ALWAYS_VISIBLE_NAV_IDS`), which the server drops from the hide list on
  write as well, so hiding Settings is unreachable from either side.
- The change rides `markLayoutEdit`, like the preset, the arrows and a drop
  already do: without it a flipped switch raised no save reminder, and the
  edit was lost on the next navigation.
- The Display card's rows are siblings again.

**An `Advanced Features` section, above System.** It holds Security, Plans and
Persona — the entries an operator configures once and rarely revisits — so
System is left with the surfaces that run the gateway rather than govern it.
Media Providers became a real accordion rather than one hardcoded boolean:
`openAccordions` is a set of ids, so a second accordion in `nav.ts` needs no new
state variable, and the open state survives a reload through `localStorage`. The
header's active state is derived from its children instead of a `media-` prefix,
so it stays correct for any accordion. `aria-expanded` rides the button, which
the old markup lacked.

**Global search (`Ctrl+K` / `Cmd+K`).** One index over every model, provider,
page and settings section — 1,656 rows, built once and cached. The query is
*staged* in the input and only run when the operator presses Enter or the Search
button: a keystroke-driven filter over that many rows re-renders the list
between two keypresses, which reads as lag on the 1 vCPU box this gateway
serves. Editing the query retires the results on screen, so a stale list never
reads as a search that ran on its own. Pages and settings outrank models,
because an operator typing a tab name wants the tab, not the 400 models whose id
happens to contain the same letters. A nav row can be pinned from its result.

**Pinned entries (default on).** A Pinned section above the groups, holding up
to eight. On by default because the section is empty until something is pinned,
so an untouched install gains nothing and loses nothing. The list is a
per-operator `localStorage` preference rather than a server setting: an operator
who pins Usage on their laptop has not asked for it on the box in the rack. A
pin naming an entry a later build removes is dropped rather than rendered as a
dead row.

**Collapse to icons (default off).** The sidebar shrinks to a 56px icon rail,
every label replaced by its icon with a tooltip. Off by default because it hides
every label at once, which is a layout to opt into rather than one to discover.
The links stay in the DOM — only their labels go.

**Sidebar counts.** A badge only appears when the number is actionable:
Providers reads `3 / 12 active` rather than a bare total, a failing connection
earns the warning colour, and Combos and API keys show a plain count while the
list is short enough to read. Proxy Pools shows nothing because its list is
fetched inside its own view, and inventing a number for it would be worse than
an absent one.

**Labelled groups fold.** `Advanced Features` and `System` are now buttons, so
an operator on a short screen can press the sections they are not using. Groups
start open — the stored set holds the *collapsed* ids — and the group holding
the active tab stays open whatever was stored, because folding it would hide the
page the operator is looking at.

The two layout preferences live in a new `lib/sidebarPrefs.svelte.ts` rather
than in `displayPrefs.ts`, because the Settings switch and the sidebar are
siblings in the same tab: a `storage` event never fires for that write, and
polling `localStorage` to work around it is the kind of thing that quietly
becomes permanent. Pin rules are pure in `lib/navFavorites.ts` and reactive in
`lib/navFavorites.svelte.ts`, following the `usagePanelPlan.ts` /
`usagePanel.svelte.ts` split this repo already uses.

Verified in a real browser against a real gateway: the switch hides the entry,
Save publishes it, the sidebar drops the row, the switch brings it back, and the
Settings row stays disabled; the section renders with Security; the accordion
folds and unfolds with its children and survives a reload; typing produces no
results and Enter does; a model query ranks models first and a page query ranks
the page first; a result navigates and closes; a pin survives a reload and an
unpin removes it; the rail shrinks 288px → 56px with every link intact and
restores; groups start expanded, fold on click, survive a reload, and reopen on
navigation; a badge appears with the right sentence once an API key exists.
`bun test src/` 672 pass (24 new), the two e2e suites 12/12, svelte-check
baseline 77 → 76.
