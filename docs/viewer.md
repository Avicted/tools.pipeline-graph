# The HTML viewer and Miro import

Open `pipelines.html` in any browser; it needs no server and no network.

- **Shareable URLs.** The address bar always holds the view and the selected
  node (`pipelines.html#overview/bt%3Aorders-service%2FImage`), so you can paste
  exactly what you are looking at. Browser Back/Forward step through views and
  selections. `L` copies the link.
- **Lineage.** Click a node to colour everything upstream and downstream of it.
  `B` (blast radius) and `D` (dependencies) keep one direction, `I` both, and
  `Shift+click` on a second job keeps only the path between the two. Right-click
  a job for these and copy-id / open-source actions. The details panel (docked at the bottom, drag its top edge to resize) lists neighbours by link type,
  including ones outside the current view, plus steps, scripts, triggers, params
  and artifact rules, each with a copy button. When the repo is on GitHub,
  GitLab, Bitbucket Cloud or Gitea, `file:line` links to the analysed commit.
- **Search and commands** with `/` or `Ctrl`/`⌘`+`K`. It matches names, TeamCity
  ids, scripts and param names in every view, and lists matching commands below
  (`>` shows commands only). `↑`/`↓` and `Enter` jump to a node (switching views
  if needed), and `N`/`⇧N` steps through the hits on the map.
- **Canvas.** Scroll or drag to pan; pinch or `Ctrl`+scroll to zoom. `F` fits,
  `0` is 100%, and double-click zooms in. When zoomed out, cards show only
  their titles, enlarged so they stay readable.
- **Sharing.** `P` is a presentation mode for screen sharing (no chrome, bigger
  text). **SVG** downloads the current view, with its filters and highlights,
  for slides and docs. `T` switches light/dark. The selected lineage's edges flow downstream; **Animate flow** in the sidebar turns that off (it is off by default with reduced motion).
- `?` lists every shortcut.

## Importing into Miro

Use **Upload → Visio (.vsdx)** with `pipelines.vsdx` and pick the pages to
import. Colours and formatted text are kept, and connectors stay glued to
their shapes. `pipelines.drawio` is the editable version for diagrams.net and
Confluence.
